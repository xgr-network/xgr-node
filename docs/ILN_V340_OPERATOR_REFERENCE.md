# XGR Interchain — v3.4.0 operator reference

**Reviewed:** 2026-10-27. Source: tagged public node v3.4.0, `interchain/config.go`, `command/ibft/interchain/` and `jsonrpc/xgr_*`. The [complete protocol reference](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_v3.4.0_Reference.md) and [independent execution runbook](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_v3.4.0_Operations.md) describe the corresponding public contracts and recovery helper.

## Configuration and authority

The optional Interchain worker uses reviewed compiled chain IDs, domains, deployment bindings, RPCs and finality policy from `interchain/config.go`. It does not accept per-validator route/fee/finality `XGR_INTERCHAIN_*` overrides. Validator signing keys come from the existing secrets manager. Route identities are discovered from the canonical Registry; adding a token on a supported chain is not a validator restart or discretionary route-admission procedure. A wholly new physical chain still requires reviewed infrastructure/configuration and a release rollout.

XGRChain chain/domain is 1643; Base 8453 and Arbitrum 42161 are configured destinations. XGRChain committed finality and Base/Arbitrum checked `safe` finality are different policies. Unknown chains, unavailable finality and inconsistent source evidence must not silently fall back to a weaker rule.

The runtime uses **Direct-BLS per-message authorization**. Old replica-prefix and checkpoint-root repair procedures do not apply to current message delivery. This does not remove the chain's consensus state/transaction tries. Weighted IBFT consensus, Interchain validator membership and optional transaction delivery have distinct authority boundaries.

## CLI

Inspect the installed binary's `--help` before state-changing commands. The following command families exist in this release:

```sh
xgrchain ibft interchain --help
xgrchain ibft interchain bootstrap-proof --help
xgrchain ibft interchain set-active --help
xgrchain ibft interchain fee create --help
xgrchain ibft interchain fee approve --help
xgrchain ibft interchain fee execute --help
xgrchain ibft interchain storage-status --help
```

`bootstrap-proof` creates the BLS bootstrap proof from the validator's existing secrets, selecting `--origin-chain-id` and `--destination-domain`. It is not a token route-admission vote. `set-active --chain base --active true --data-dir /ABS/PATH` queues this validator's participation request to the running node, which validates eligibility, gathers quorum, submits the destination membership transaction and verifies final state. Explicit deactivation uses `--active false`. Do not infer completed membership from command submission alone.

The `fee` family creates, explicitly signs and executes **source-chain-wide** native validator fee proposals. Its quorum is not a route-listing committee. Read the proposal identity, source and amount before approval; do not substitute a per-route fee.

`storage-status --data-dir /ABS/PATH` is read-only offline diagnostics for persisted votes, attestations, requests and governance usage. It does not repair the database or require a running wallet. Preserve secrets and a consistent node backup before maintenance; never restore a second concurrently signing copy of the same validator identity.

## JSON-RPC

| Method | Parameters | Purpose |
| --- | --- | --- |
| `xgr_getILNIndexerStatus` | `[]` | Worker heartbeat and per-route source/RPC catch-up state |
| `xgr_requestILNQuorum` | `[route, messageId, sourceBlockNumber]` | Hint for independent validation of an original canonical message |
| `xgr_getILNQuorum` | `[route, messageId]` | Quorum progress, not destination settlement |
| `xgr_getILNQuorumAttestation` | `[route, messageId, setId]` | Current-set message attestation |
| `xgr_getInterchainValidators` | `[destinationName]` | Configured destination's canonical validator set |
| `xgr_getILNGovernanceQuorum` | `[proposalId]` | Source fee-governance quorum |

Use the full discovered route name, original message ID and actual source block; do not replace the route name with a chain ticker or arbitrary route ID. A quorum hint is not permission to sign caller-selected bytes.

```sh
curl -fsS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"xgr_getILNIndexerStatus","params":[]}'
```

Health schema version 2 includes worker timestamp/aliveness, aggregate state and per-route state, source head, scan cursor, lag and errors. A live worker is not evidence that every source is healthy. Distinguish `unavailable`, `catching-up` and stale/stalled monitoring. A missing snapshot can return an error instead of a fabricated healthy result.

## Delivery and failure isolation

Validators verify canonical source receipts, chain-native finality, route/Gateway/Mailbox bindings and durable fee allocation before attesting. Destination registry/ISM checks authenticate exact message and current set. A healthy managed relayer is not part of the signature trust model.

The public helper in xgr-interchain prepares unsigned destination `Mailbox.process` calldata. The user or any independent executor can fund and sign destination delivery without XITA's commercial gateway, sponsor or relayer. Required validator quorum, protocol fees, chain RPC access and gas remain dependencies. A relay cannot repair missing quorum by assuming authorization.

Preserve original source transaction/message IDs across outages. If the set rotates, obtain eligible authorization for the same message; never repeat the source lock/burn to repair delivery. Verify `Mailbox.delivered` and recipient balances after destination confirmation. Failed destination simulation is a reason to diagnose, not bypass router/ISM checks.

## Consensus release boundary

v3.4.0's common `securityAccounting` fork activates native accounting, checked BaseFee and transaction-local SSTORE/refund fixes at the configured block, inclusively. It does not deploy Interchain contracts or change the Direct-BLS payload. Follow the [release/activation runbook](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_v3.4.0_Release_and_Activation.md); Mainnet canonical activation is 12,186,213. An absent fork entry means disabled, even with the new binary installed.
