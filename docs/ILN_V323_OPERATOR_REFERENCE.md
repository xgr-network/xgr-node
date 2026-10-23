# XGR-Node v3.2.3 — Interchain validator, CLI, RPC and operations reference

**Status:** reference for the shipped `v3.2.3` tag; updated 2026-10-23. **Implementation source:** [xgr-node](https://github.com/xgr-network/xgr-node/tree/v3.2.3) and [xgr-interchain](https://github.com/xgr-network/xgr-interchain). `xgr-hyperlane` is retired/legacy, not the current protocol repository.

## 1. Separation of responsibilities

XGRChain is an EVM-compatible IBFT PoS L1 (chain ID/domain 1643). Its ordinary consensus validators and *interchain signing* are related but distinct: interchain participation is an opt-in subset of eligible PoS validators, and message attestations do not become IBFT state transitions. XGRChain is the mandatory hub between spokes, currently including Base (8453) and Arbitrum (42161). Source protocols and destination ISMs, not a single relay operator, authorize delivery.

The **node** provides eligible-validator BLS votes and quorum assembly, P2P Merkle leaf replication, independently checked source Hook checkpoints, public quorum/proof RPCs and on-chain source-fee governance. **xgr-interchain** provides actual route Registry, Factory, ILNGateway, FeeVault, guarded token routers, Mailbox/Hook, destination ISM, optional paid ExecutionGateway, optional native gas sponsor, read-only transfer indexing, front end and unsigned `recover-iln.mjs` helper. Neither relay services nor the commercial ExecutionGateway are consensus requirements. The user retains wallet signing authority; a collateral router may nevertheless lock original assets in public on-chain escrow.

## 2. Runtime components and data flows

```text
Source user wallet -- ILNGateway.bridge + validator fee --> guarded source router
    | original ERC-20 lock / wrapped burn / native lock
    v
Source Mailbox.Dispatch -> MerkleTreeHook.InsertedIntoTree (source chain)
    | confirmed source RPC reads (independently verified)
    v
XGR interchain validator worker -- P2P gossip, local Merkle replica, BLS quorum
    | xgr_getILNQuorumAttestation / xgr_getILNReplicaProof
    v
Any independent, gas-paying wallet --> destination Mailbox.process(metadata,message)
    | current-set destination ISM verifies exact message, root, route, set and BLS quorum
    v
Destination guarded router --> mint wrapped / unlock original
```

Two external-chain hops repeat the operation, **source → XGRChain → destination**; neither a direct spoke-spoke route nor an automatic second signing is assumed. An optional commercial `XITAExecutionGateway` adds a separately disclosed service fee and journey linkage, but direct `ILNGateway.bridge` remains permissionless.

The v3.2.3 worker discovers **new directed routes on already configured chains** from confirmed source `ILNRegistry.RouteAdded` events, validates their current on-chain registry/Gateway binding and persists a route catalog. `LoadAll()` in `interchain/evm/config.go` reads `XGR_INTERCHAIN_<NAME>_CHAIN_ID` at startup; `New()` in `interchain/runtime/worker.go` creates the destination map at worker construction. **A completely new chain requires an operator-approved config change and currently a worker/node restart**, unlike addition of a route on an existing configured chain. Dynamic hot-adding of a new physical chain is *not* a v3.2.3 capability.

## 3. Mainnet chain configuration

Use the checked-in [mainnet environment template](https://github.com/xgr-network/xgr-interchain/blob/main/runtime/xgr-node-interchain-mainnet.env.example); never copy secrets to Git. Template names for chain configuration are `XGR_INTERCHAIN_BASE_*`, `XGR_INTERCHAIN_ARBITRUM_*` and `XGR_INTERCHAIN_XGR_*` with `CHAIN_ID`, `DOMAIN`, `RPC`, `REGISTRY_ADDR`, `ILN_REGISTRY_ADDR`, `CONFIRMATIONS`, `VERIFIER_FORMAT`, `MEMBERSHIP_VALIDITY_SECONDS`, `EXECUTOR_STEP_DELAY_SECONDS` and optional reserve values. Relevant deployment manifests are under `deployments/chains/{base,arbitrum,xgrchain}.json`. **Do not create per-token `XGR_INTERCHAIN_ROUTE_*` variables:** route ID discovery is on chain. Verify actual RPC `eth_chainId`, canonical registry addresses, validator membership and destination module at runtime.

Example **read-only** identity/status check (on the node's own restricted JSON-RPC):

```sh
curl -sS http://127.0.0.1:8545 -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' | jq .
curl -sS http://127.0.0.1:8545 -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"xgr_getILNIndexerStatus","params":[]}' | jq .
```

## 4. Validated CLI commands (Cobra subcommands)

The executable name is `xgrchain`, built from the [v3.2.3 source](https://github.com/xgr-network/xgr-node/tree/v3.2.3). Always verify `./xgrchain version`, and follow the executable's own `--help` for supported flags.

| CLI | Purpose / parameters |
| --- | --- |
| `xgrchain ibft interchain bootstrap-proof --data-dir /ABS/PATH --origin-chain-id 1643 --destination-domain 8453` | Produces validator BLS possession proof for **bootstrap**; `--config` may be used instead of `--data-dir`, never both |
| `xgrchain ibft interchain set-active --chain base --active true --data-dir /ABS/PATH` | Queues opt-in membership change via running validator worker and verifies on-chain destination state |
| `xgrchain ibft interchain set-active --chain base --active false --data-dir /ABS/PATH` | Explicit opt-out; safety/reserve/preconditions still apply |
| `xgrchain ibft interchain fee create --source base --fee-wei AMOUNT --data-dir /ABS/PATH` | Proposes chain-wide source native validator fee **without automatically approving**; optional `--nonce`, `--ttl`, `--timeout` |
| `xgrchain ibft interchain fee approve --proposal-id 0xHASH --data-dir /ABS/PATH` | Explicitly BLS-approves proposal with the local validator key |
| `xgrchain ibft interchain fee execute --proposal-id 0xHASH --data-dir /ABS/PATH` | Submits fully approved fee quorum to source-chain Registry; on-chain TX |
| `xgrchain ibft interchain storage-status --data-dir /ABS/PATH` | Read-only local sizes/counts of archive, votes, request and governance files |
| `xgrchain server --chain /PATH/genesis.json --data-dir /ABS/PATH --seal=true ...` | Starts full node/validator; see `xgrchain server --help` and standard chain operation guide |

Membership requests and fee governance are different operations. **Fee governance requires explicit validator approvals** and a 2/3 quorum; **route registration is permissionless** and uses the public Factory/Registry, not these fee CLI commands. Never publish BLS private keys or wallet transaction keys.

## 5. Public JSON-RPC reference

All calls below use JSON-RPC 2.0 on an appropriately restricted trusted XGRChain node. Parameter shapes and semantics are from the v3.2.3 handlers under `jsonrpc/`.

| RPC method | Parameters | Meaning |
| --- | --- | --- |
| `xgr_getInterchainValidators` | `["base"]` | Reads destination current validator set, set ID and unweighted threshold |
| `xgr_requestILNQuorum` | `[routeName, messageId, sourceOperationBlockNumber]` | **Public hint only**; queues worker's independent read/verification; caller cannot supply what validators sign |
| `xgr_getILNQuorum` | `[routeName, messageId]` | Status such as `PENDING`, `PARTIAL_QUORUM`, `RETRYING_VALIDATION`, `AVAILABLE_UNVERIFIED`, `NOT_FOUND` or `EXPIRED_RETRY` |
| `xgr_getILNQuorumAttestation` | `[routeName, messageId, currentSetId]` | Full attestation with route, source checkpoint, canonical root, BLS aggregate and signer bitmap; verify set ID |
| `xgr_getILNReplicaProof` | `[routeName, messageId]` | Read-only proof with 32 Merkle siblings and attestation from independently verified local replica |
| `xgr_getILNIndexerStatus` | `[]` | Worker heartbeat and per-route status with `state`, `leaves`, `localLeaves`, `lagLeaves`, `pendingPeers`, `error` |
| `xgr_getILNGovernanceQuorum` | `[proposalId]` | Read-only finalized source fee quorum |

Read-only method call example:

```sh
curl -sS http://127.0.0.1:8545 -H 'content-type: application/json' --data \
'{"jsonrpc":"2.0","id":1,"method":"xgr_getInterchainValidators","params":["base"]}' | jq .
```

**Interpret `AVAILABLE_UNVERIFIED` precisely:** the local archival quorum file exists; the caller still must check current destination set, current ISM and `Mailbox.process` simulation. A JSON-RPC response alone is not evidence that destination delivery succeeded.

## 6. Merkle replicas and P2P recovery

The node persists ordered **leaf hashes**, checkpoint count, block and root under `<DATA_DIR>/interchain/iln/replicas/<sourceChainId>/<hook-address>.json`. Each validator anchors its candidate to a confirmed source `MerkleTreeHook` root and count. Peers exchange at most **256 leaves per chunk**, across up to **three peer attempts**, as untrusted data. The receiving node rehashes the full tree and checks the canonical root before accepting the snapshot; raw P2P bytes are never proof of authorization.

A verified existing prefix can be extended from confirmed source Hook events within the **128-block recent catch-up window**; older missing prefixes require another complete verified peer snapshot or a deliberately seeded backup/reconstruction. Do not assume that the root alone can produce historical leaves. If all peers are missing the same prefix, v3.2.3 does **not** automatically retrieve arbitrary historical Hook logs. This is a known recovery/operational limitation, not permission to bypass verification.

Manual bootstrap is provided by [`xgr-interchain/manage.sh init-merkle`](https://github.com/xgr-network/xgr-interchain/blob/main/docs/ILN_V320_MERKLE_BOOTSTRAP.md):

```sh
# On the selected node; full canonical reconstruction, no writes:
./manage.sh init-merkle --chain xgrchain \
  --data-dir /ABS/PATH/TO/NODE/DATA --rpc https://rpc.xgr.network \
  --chunk-size 100 --dry-run
```

If valid and complete, follow the safety instructions to back up, **stop only the target validator**, and rerun without `--dry-run`; if existing replica is divergent, `--replace` is explicit and requires stopped-validator checks. The script verifies Hook event ordering, checkpoint root and count before writing; it does not change chain state, contracts, fee claims or BLS attestations. Once seeded, restarted nodes can advertise the independently verified replica to peers. Do not take all consensus validators offline at once.

**Health metrics caveat:** v3.2.3's `recordILNReplicaFailure` sets `localLeaves=0` on verification/RPC errors for fail-closed readiness reporting; that alone does **not** prove the persisted file physically contains zero leaves. Compare the actual verified replica and source checkpoint when diagnosing.

## 7. Transfers, fee vaults and recovery

For direct transfers query `ILNGateway.quoteILN(uint32,bytes32,uint256)`, grant bounded ERC-20 allowance to **the protocol ILNGateway**, and sign `ILNGateway.bridge` with exact `msg.value`. The source Gateway routes to the asset router and immutable FeeVault allocation. Validators independently verify the source `ILNOperation`, Dispatch, Hook root, original fee allocation and destination set before signing. The public `runtime/native-relayer/recover-iln.mjs` helper validates original source evidence, current BLS quorum and Merkle siblings, runs `Mailbox.process.staticCall` and emits **unsigned** `{to,value,data}` only. The **user** submits it to destination Mailbox; no XITA relayer, optional Fee Execution Gateway or company private key is needed.

Direct spoke-to-spoke transfers consist of two independently wallet-signed hops. For fees, the chain-wide source validator fee is distinguished from the optional commercial XITA service fee and native-chain gas. Validators can read `FeeVault.claimable(validator)` and submit `FeeVault.claim()` using a protected wallet on the **source chain**. Do not confuse validator rewards with customer services or quote current fees as historical payment evidence. See [end-to-end guide](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_v3.2.3_Operations.md).

## 8. Operations, security and failure matrix

| Symptom | Interpretation | Operator response |
| --- | --- | --- |
| `workerAlive=false` / stale heartbeat | Worker stopped or status over two minutes old | Verify service process, local logs, data-dir, secrets permissions and chain sync |
| `catching-up`, `lagLeaves>0` | Source head/root advanced beyond locally verified replica | Examine RPC chain ID, confirmed state, P2P connectivity, 128-block catch-up window |
| `SOURCE_MERKLE_RECONSTRUCTION` | Not enough verified canonical leaves for message proof | Preserve source TX and message ID; verify peers, seed one correct replica only if needed |
| `PARTIAL_QUORUM` | Fewer than threshold independent eligible BLS votes | Check source/destination RPCs, validator set membership, confirmed source evidence |
| `AVAILABLE_UNVERIFIED` | Archival signatures exist | Verify current set ID and destination `process.staticCall`; do not assume final delivery |
| RPC 403/429/503, lagging receipts | External provider degraded or state-pruned | Switch to another trusted chain-ID-verified RPC, avoid unconstrained historical scans in normal worker |
| Validator-set rotation while pending | Earlier set signatures may be invalid at destination | Request fresh quorum for **original** message under current set, not new source bridge |
| Destination `delivered(messageId)=true` | Canonical message already processed | No replay or additional source lock; confirm balances/receipt and stop |
| No complete peer prefix anywhere | P2P cannot reconstruct missing hashes | Verified one-off source Hook event reconstruction or restorable backup; no automatic v3.2.3 fallback |

Minimum backup scope: validator/secrets manager, full ILN data directory including `replicas/`, `attestations/`, route catalogs/cursors and relevant source dispatch evidence. Validate restore against canonical root before returning to service. Keep wallet/private signing keys out of diagnostic output and GitHub. Limit public JSON-RPC exposure to intended read-only interfaces, and never expose management filesystem or operator keys.

## 9. Version provenance and tests

`v3.2.1` decoupled fast worker processing from slower RPC polls; `v3.2.2` added generic P2P Merkle replica synchronization but had a confirmed checkpoint comparison issue; `v3.2.3` corrected that comparison to permit block height advance while stable root/count remain independently verified. P2P replication is not equivalent to arbitrary all-peers-lost source reconstruction.

Source tests: `go test ./interchain/... ./jsonrpc/... ./command/ibft/interchain/...`; full node `go test ./...` and consensus-sensitive suites before release. The repository's `docs/ILN_V320_*.md` describe the v3.2.0 **design history**, not new v3.2.3 deployment requirements. Never silently re-deploy audited smart contracts just to change node runtime binaries.

### Code index

- [`interchain/evm/config.go`](https://github.com/xgr-network/xgr-node/blob/v3.2.3/interchain/evm/config.go) — destination configuration and LoadAll
- [`interchain/runtime/worker.go`](https://github.com/xgr-network/xgr-node/blob/v3.2.3/interchain/runtime/worker.go) — worker and poll scheduling
- [`interchain/runtime/iln_discovery.go`](https://github.com/xgr-network/xgr-node/blob/v3.2.3/interchain/runtime/iln_discovery.go) — permissionless route event discovery
- [`interchain/runtime/iln_replica_network.go`](https://github.com/xgr-network/xgr-node/blob/v3.2.3/interchain/runtime/iln_replica_network.go) — authenticated P2P Merkle chunks
- [`interchain/evm/iln_replica_catchup.go`](https://github.com/xgr-network/xgr-node/blob/v3.2.3/interchain/evm/iln_replica_catchup.go) — strictly recent Hook catch-up
- [`jsonrpc/xgr_iln_*.go`](https://github.com/xgr-network/xgr-node/tree/v3.2.3/jsonrpc) — read-only source evidence status and public quorum API
- [`command/ibft/interchain/`](https://github.com/xgr-network/xgr-node/tree/v3.2.3/command/ibft/interchain) — command parser authoritative flag names

