# XGRChain v3.5.5 — Indexer-first ILN source observer fallback (light release)

**Baseline:** immutable v3.5.4. **Scope:** only the background source Gateway observer and Registry route discovery polling intervals. No chain state, IBFT consensus, PoS, fork, genesis, cryptography, contract, fee policy, public RPC interface, signing format, quorum logic or source evidence verification change.

## Changes
- Adopt XITA's persistent finalized-transfer indexer as the primary source for untrusted transfer discovery hints. The indexer-to-XGR quorum handoff was implemented independently in [xgr-interchain PR #442](https://github.com/xgr-network/xgr-interchain/pull/442), merged to main and deployed separately.
- Reduce each validator's independent **background source Gateway scan** from every 30 seconds to every **10 minutes**, as a fail-safe fallback against indexer outage, censorship or missed events.
- Reduce periodic permissionless Registry route discovery from every 60 seconds to every **5 minutes**. The existing on-demand route hint and sourceTx-pinned quorum admission paths stay operational.
- Keep membership and reserve checks at 60 seconds, local worker ticking at 2 seconds and retry/rebroadcast polling at 30 seconds. Per-message evidence backoff and per-provider cooldowns introduced in v3.5.4 are unchanged.
- The two independent source-RPC operators must still provide **matching complete finalized Gateway/Dispatch/Registry/FeeVault evidence** for every externally sourced validator vote. The centralized indexer only suggests a source transaction locator; it cannot authorize signatures or execution.

## Release verification
- Source is limited to `interchain/runtime/worker.go`, `interchain/runtime/iln_v321_polling_test.go`, these release notes and `.github/workflows/release-light-v355.yml`; exact-commit preflight rejects all other differences against the published v3.5.4 tag.
- Full Go interchain, consensus interchain and JSON-RPC regression tests run repeatedly. Observer timing tests repeat eight times; independent operator and quorum fail-closed matrices remain mandatory.
- GitHub Actions manually dispatched on the reviewed `XGR3.0` HEAD must receive the exact 40-character SHA and `RELEASE-v3.5.5`. It rejects attempts to overwrite an existing tag.
- Linux amd64 release contains `xgrchain-v3.5.5-linux-amd64`, `version.txt`, `commit.txt`, `toolchain.txt`, `sha256sums.txt` with independently checkable embedded version and source commit.

## Sequenced rollout and rollback
1. Deploy and verify the separate XITA indexer handoff from PR #442 first. Confirm the operator's existing BSC→XGR message is handled through local `xgr_getILNQuorum` and untrusted `xgr_requestILNQuorumWithSourceTx` without repeated recovery source reads.
2. **Only after the real indexer handoff is confirmed**, manually dispatch this light release for the exactly reviewed XGR3.0 commit. Publishing does not deploy any validator.
3. Verify release asset checksums, commit and embedded version; preserve the v3.5.4 known-good binary. Upgrade validators **one at a time** while maintaining IBFT quorum, sync and journal continuity. Do not delete or reset chain DB, ILN cursors, local votes, attestations or BLS keys.
4. Monitor indexer freshness, gossip, quorum status and finality. A missing indexer delays ordinary independent source discovery up to approximately 10 minutes (plus bounded catch-up/finality). The fallback must never bypass source evidence validation.
5. For any regression, restore the v3.5.4 **binary only**. Keep all persistent state and pending messages intact. Never relock/re-send the existing source transaction `0x6e6a7c3ff28cb9fce8db40230891f3e34dd756568167b0943f6e717c5c277dd7`.

**No automatic node deployment, on-chain transfer, wallet operation or key manipulation is performed by preparing or publishing this release.**
