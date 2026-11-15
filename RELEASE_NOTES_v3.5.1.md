# XGRChain v3.5.1 — F-20 Direct-BLS Recovery Light Release

**Scope:** Recovery-only maintenance of the existing XGRChain v3.5.0 / XITA V1 deployment. No protocol fork, consensus, staking, BLS PoP, smart-contract or XITA wire-format change.

**Source baseline:** [v3.5.0](https://github.com/xgr-network/xgr-node/releases/tag/v3.5.0) at `b3717a44b5ec5faa19fabdd88bd4b91f54659983`. Reviewed fix: [PR #91](https://github.com/xgr-network/xgr-node/pull/91), merged into `XGR3.0` as `d5e8977a0fa1059b2ccf25ecefc360c7454e39df`. The release commit also adds this document and the dedicated guarded build workflow.

## F-20 — Retain old validator-set recovery evidence until replacement is durable

- An obsolete Direct-BLS locally signed vote is no longer retired merely because the replacement-signing function returned without error.
- Require a replacement vote from the same local signer, same route and complete authenticated message, with the current destination validator-set ID.
- Check exact replacement payload/signature in both the in-memory journal and persisted on-disk evidence before old-set retirement.
- Synchronize the signed replacement file and parent directory to disk (`fsync`) before reporting successful persistence.
- Do not delete old vote evidence when the local signer is ineligible, new set unavailable, or replacement persistence fails. No automatic duplicate signing, nonce replacement or user transfer execution.
- Dedicated negative tests cover absent successor, memory-only record, wrong set/signer/route, altered signed fee, and corrupt journal.

## Light-release validation (no full EEST)

The `release-light-v351.yml` workflow is manually dispatched **only from `XGR3.0`** and requires the exact reviewed 40-hex commit plus `RELEASE-v3.5.1` confirmation. The workflow fails closed on existing `v3.5.1` tag, non-descendant v3.5.0 ancestry and scope violations beyond the named Interchain recovery, release-note, prior-version documentation and workflow paths.

Focused tests:
- `go test -count=3 -timeout 12m ./interchain/... ./consensus/ibft/interchain ./jsonrpc`
- `go test -count=8 -timeout 8m ./interchain/runtime -run 'TestF20'`
- `go test -count=3 -timeout 5m ./interchain/evm ./consensus/ibft/interchain -run '^(TestDirect|TestXITADirectMessage)'`
- Build `xgrchain-v3.5.1-linux-amd64`, verify embedded release version and exact commit, provide `version.txt`, `commit.txt`, `toolchain.txt`, `sha256sums.txt`, and immutable GitHub release assets.

Existing [PR #91](https://github.com/xgr-network/xgr-node/pull/91) already passed the Direct-BLS Release Candidate and PR CI on the exact merged fix. The public v3.5.1 release must additionally pass the guarded light-workflow and asset-checksum validation.

## Operator deployment (manual, after release publication)

1. Review GitHub release assets, `commit.txt`, `version.txt`, `toolchain.txt`, `sha256sums.txt` and compare the published SHA against the pinned release commit.
2. Roll out through the established validator/operator binary update procedure, using the **unchanged existing Mainnet genesis** and v3.5.0 PoP fork configuration. No key rotation, migration, restaking or new activation block.
3. Update one node at a time according to the existing validator quorum/availability runbook; keep a verified v3.5.0 binary rollback copy and protect persistent vote/attestation journals. Do not delete old journal records to fix a startup failure.
4. After each restart verify binary version/commit, peer sync, block progression, IBFT quorum, interchain worker status and Direct-BLS vote/recovery behavior. If failed, stop rollout and restore the previously verified executable without modifying chain data.
5. Record observed deployment versions separately from the GitHub release; published release does not prove production installation.

**Security scope note:** This patch addresses XITA delta Finding F-20 only. It neither verifies remediation of F-08/F-19/F-36 in optional services nor independently closes other remaining audit findings. See the 2026-11-15 XITA V1 audit handoff for protocol/service/UI separation. Solana excluded.
