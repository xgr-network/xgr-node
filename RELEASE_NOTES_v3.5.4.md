# XGRChain v3.5.4 — Direct-BLS RPC backpressure and receipt-first verification (light release)

**Baseline:** v3.5.3. **Scope:** External EVM-source Interchain availability only. No IBFT, PoS, hardfork, genesis, validator-set, cryptographic, contract, source policy, token, asset or transfer changes.

## Changes
- Persist per-message / exact source-transaction proof-attempt leases and exponential 2–30 minute retry backoff with deterministic per-validator jitter. Shared across quorum requests, source observers, durable observer retries and validator peer requests through the original BLS signing entry point.
- Preserve original quorum request, observer cursor, retry journal and locally persisted signatures during temporary failure. Existing sidecars fail closed on corruption. Restarting a validator does not bypass the lease. Native XGR source keeps original locally committed IBFT verification without the external RPC throttle.
- Explicitly identify upstream quota/plan errors (including 1RPC “usage limit”); apply 30-minute cooldown to plan-exhausted endpoints and 10-minute cooldown to rejected HTTP 403 reads. Keep 429 backoff and per-provider isolation; one failed operator never supplies partial evidence to another.
- For externally sourced transactions with a known original hash, fetch the exact receipt after verified chain ID/finality head and before costly Registry and FeeVault reads. Eliminate a duplicate single-provider finalized-head preflight in the public quorum worker because the two independent source witnesses each check their own finalized/safe head.
- **Unchanged security:** The complete source Gateway/Dispatch/Registry/FeeVault, source canonical block, chain-specific finality and **two independent matching full RPC witnesses** are still required for every Direct-BLS vote. Known-hash receipt verification never falls back to `eth_getLogs`; unknown-hash discovery is unchanged.

## Tests and release gate
- GitHub [PR #98](https://github.com/xgr-network/xgr-node/pull/98): full Go CI, IBFT/validator E2E and Direct-BLS release-candidate checks must all complete successfully before merge.
- New tests cover durable retry across process restart, retry in-flight dedup, bounded jitter, invalid sidecar fail closed, independent source-hash identity, explicit provider quota/403 cooldown, and receipt-first fail-fast ordering for BSC, Base, Arbitrum and Optimism. Existing four-chain two-independent-provider/security-negative RPC matrix remains mandatory.
- The light-release workflow only accepts an exact reviewed `XGR3.0` merge commit whose diff versus `v3.5.3` is limited to the reviewed Interchain files and this release workflow/notes. Build outputs include Linux amd64 binary, embedded version/commit, `sha256sums.txt`, `commit.txt`, `version.txt` and `toolchain.txt`.
- This change improves availability and protects public RPC quotas. It **does not create new independent RPC operators or guarantee that BSC providers will serve the original transaction receipt**. The original BSC → XGR message can only complete once two independent fully valid matching witnesses are available.

## Controlled manual validator deployment
1. Download only the immutable published `v3.5.4` asset; compare `commit.txt`, embedded `version` and `sha256sums.txt`. Preserve the prior known-good v3.5.3 binary as rollback.
2. Upgrade validators **one at a time**; keep chain sync and IBFT quorum intact. Do not modify genesis, chain databases, interchain journals, source cursors, BLS keys or vote stores.
3. Inspect validator sync, shared backpressure and ILN quorum status. Verify two independent complete BSC source receipts before signing.
4. The original BSC source TX `0x6e6a7c3ff28cb9fce8db40230891f3e34dd756568167b0943f6e717c5c277dd7`, block `134450134`, message `0x83ec6a35146a7dbab41f8472dc3262320a8d1003462e260e891ade4db69e534d` must **never** be relocked or resent. Existing XITA main includes the v3.5.3 sourceTxHash recovery integration.
5. Stop rollout and revert **binary only** on regression; preserve all on-disk state and witness requirements.

**No automatic deployment, wallet action or on-chain transfer is performed by publishing this release.**
