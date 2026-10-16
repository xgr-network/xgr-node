# XGRChain v3.1.6 - Interchain validator admission (Registry V3)

**Status:** Release candidate. Not published until tests and mandatory release gates pass.
**Target:** `xgr-network/xgr-node`, branch `XGR3.0`.
**Baseline:** `99b7b915fb09adaf67c1c3527c8ad45522e0414a` (v3.1.5).

## Fix A-02: one-command validator admission

`xgrchain ibft interchain set-active --chain <destination> --active true --data-dir <data-dir>` now completes the additional Registry V3 prerequisite itself:

1. Validate XGR PoS eligibility and candidate BLS identity as before.
2. Reuse the node's local validator BLS private key to sign the immutable bootstrap payload for the destination domain.
3. Read `possessionVerified` on the destination ValidatorRegistry V3. If already verified, do not submit another registration transaction.
4. Otherwise sign and send the nonpayable, permissionless `provePossession` transaction using the **candidate's own local validator wallet**, require a successful receipt and prescribed confirmations, then verify the mapping.
5. Only then collect existing interchain membership quorum votes and perform the unchanged `applyMembership(ADD)` transaction.

REMOVE, existing active validators, and existing validator quorum signatures are unchanged. Failure or RPC ambiguity stops the process before a new membership request; re-running the same CLI command re-checks confirmed PoP state.

## Compatibility

- No changes to EVM consensus, chain state transition rules, transaction format, the P2P wire protocol, membership BLS message bytes, quorum threshold, validator fee accounting, or token routing.
- No smart contract deployment/upgrade, no XITA service dependency and no per-token validator environment entries.
- Existing validators remain active; first-time candidates on Registry V3 use the new CLI workflow.
- There is no consensus hard fork. Existing validators do not need to be upgraded simultaneously to continue block production or to sign the unchanged interchain membership payload. New entrants should use v3.1.6.

## Required test/release gates (not yet satisfied)

- [ ] `gofmt`/`git diff --check` and `go test -count=1 ./interchain/evm ./interchain/runtime ./command/ibft/interchain`.
- [ ] Verify bootstrap bytes, BLS PoP signature formats (compressed and EIP-2537), and canonical `keccak256(abi.encode(validator,blsKey,blsKeyEIP2537))` mapping key.
- [ ] Confirm no duplicate `provePossession` on retry, fail-closed behavior after RPC failure, wrong chain, invalid proof and stale membership set.
- [ ] Start with a non-member PoS validator: single CLI command should yield confirmed PoP and quorum-approved V3 membership, with valid current Set-ID.
- [ ] Ensure no PoP transaction is sent for REMOVE or for an already-active validator.
- [ ] Run complete node CI and release workflow: integration/build, EEST, ForkGate; publish tag `v3.1.6` only on success.
- [ ] Re-run the interchain in-flight validator rotation and message re-attestation acceptance test without a duplicate source lock or fee.

**Operational note:** This is a client/worker integration correction for new validator admission. Do not mark the audit item VERIFIED until a real destination EVM registration has been demonstrated with the tested v3.1.6 binary.

## Read-only RPC: destination-scoped active interchain validators

XGR-Node now exposes `xgr_getInterchainValidators(destination)` through the
existing XGR JSON-RPC service. No new configuration or per-route environment
variables are introduced. Example:

```bash
curl -sS -X POST http://127.0.0.1:8545 \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"xgr_getInterchainValidators","params":["base"]}'
```

The latest on-chain result contains `destination`, `chainId`, `domain`, `registry`, `setId`,
`count`, `quorumThreshold`, and an **ordered** `validators` array of
`{address, blsPublicKey}`. The order exactly follows `getValidatorSet()` on
the canonical destination ValidatorRegistry and therefore agrees with BLS
signer-bitmap positions. The latest-state query is not a finalized/historical snapshot. The RPC returns an error for an unconfigured
or unreachable destination, a chain-ID mismatch or an invalid registry
response. It never silently falls back to XGR PoS membership or a cached list.

**Important:** `validators` describes the **on-chain active interchain set**
on that destination. It is not a live-health or XGR PoS eligibility report;
an individual validator may be offline or awaiting removal.

Additional v3.1.6 release gates:

- [ ] `go test -count=1 ./jsonrpc` and JSON-RPC request/response test on a configured destination.
- [ ] Verify `xgr_getInterchainValidators("base")` matches the exact Base Registry `getValidatorSet()` result including set ID and ordering before and after a successful ADD/REMOVE.
- [ ] Test missing destination config, unavailable RPC, wrong chain ID, malformed BLS key and duplicate validator fail closed.
