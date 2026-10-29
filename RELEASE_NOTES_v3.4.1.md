# XGR Node v3.4.1 — BSC Direct-BLS light release (candidate)

**Status: release candidate preparation only. Not tagged or deployed.**

## Scope

- Add BNB Smart Chain chain/domain 56 to the compiled Interchain chain policies and destination readers.
- Finality: canonical `eth_getBlockByNumber("finalized", false)` with numbered-block hash agreement; no downgrade to `safe`, `latest` or confirmations.
- Receipt-pinned BSC ValidatorRegistry: `0x70f5752326735b31641f21d174ba035e904db93c`.
- Receipt-pinned BSC SourceRegistry: `0x14d5d33a65a39f96665ed631b04fecf357bcb576`.
- BSC source activation height: `130525341` from the recorded deployment transaction.
- Native XGRChain and existing Base/Arbitrum bindings are unchanged.
- No consensus, fork schedule, EVM state, staking, genesis or custody change is intended in this version.

## Release gates

1. CI: all Go tests plus repeated Direct-BLS security regression, compiled chain policy and BSC finality tests must pass.
2. Confirm that the BSC deployment manifest, receipt hashes, deployed bytecode and registry configuration match the compiled bindings.
3. Live-check two independent BSC RPC operators for chain ID 56, finalized canonical block agreement, historical receipts/logs and source activation history. No assumption that distinct RPC URLs equal independent operators.
4. Verify the existing three public BLS possession proofs for destination domain 56 and current validator membership; run failure and recovery tests before onboarding production routes.
5. Confirm the XITA route manifests and UI executionGateway requirement; route activation occurs via independently signed Admin transactions, not this release.
6. Publish only from a reviewed immutable `XGR3.0` commit with versioned checksums, then perform an operator-controlled rollout with a data-dir backup and measured health/recovery checkpoints.

## Monitoring acceptance

XITA System Health dynamically lists chain RPC status from approved chain catalog; BSC is visible after the Admin/Web catalog update even before v3.4.1 deployment. It does **not** mean the validator is serving BSC routes. A separate validator `xgr_getILNIndexerStatus` entry is expected only after v3.4.1 workers discover verified, on-chain registered BSC route gateways. Unregistered routes must not be shown as active.

## Decision record

This is an Interchain-only light candidate compared with the deployed v3.4.0 accounting-fork release. Do not release from this branch; the release workflow must run on `XGR3.0` with an explicit commit fingerprint. No claim of mainnet rollout or operational verification is made by these notes.
