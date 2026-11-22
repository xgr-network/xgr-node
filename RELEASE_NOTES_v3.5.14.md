# XGRChain v3.5.14 — Generic Direct-BLS Observer Catch-up (light release)

**Baseline:** immutable v3.5.13. **Change:** [xgr-node PR #110](https://github.com/xgr-network/xgr-node/pull/110).

## Why

The XITA SQL indexer was already caught up, but the independently scanning validator-side Direct-BLS Gateway observer on Base still lagged by 147,034 confirmed blocks. Four windows every ten minutes could only process 3,256 blocks per scan while Base produced about 3,000 new blocks during that interval. This is a background availability problem, not an independent witness proof failure.

## Changes

- Apply source-agnostic catch-up to **all configured external EVM source chains**, not just Base.
- When a previously verified source head is ahead of its persisted observer cursor, scan that source once per **60 seconds**, with up to **eight** bounded Gateway log windows per scan (a maximum 7,256-block work budget).
- After the first successful 256-block window, scan subsequent windows up to 1,000 blocks; request exactly the remaining confirmed block span for the final window instead of issuing unnecessary small reads.
- Use the existing single shared Gateway log filter across every route belonging to the same source chain, including routes with different starting cursors.
- Continue scanning **through the final confirmed block**, without a high-lag threshold. Once caught up, stop accelerated scans and retain the existing **ten-minute independent fallback**.
- Preserve all provider timeout/range fallback, 429 cooldown, cursor-persistence, journaled retry and source finality rules.
- Preserve native XGR observer operation, primary XITA source transaction hints, all BLS quorum/receipt witnesses and transaction authorization checks.

**No changes:** consensus, fork, IBFT, PoS, native chain data, validator keys, BLS signatures, on-chain contracts, wallet, payment, XITA SQL, broadcaster or source-provider trust policy.

## Release verification and deployment

The manual `release-light-v3514.yml` workflow must run from reviewed `XGR3.0` HEAD with exact 40-character `expected_commit` and explicit `RELEASE-v3.5.14`. It checks ancestry, exact source diff, all targeted regressions, and builds an immutable Linux amd64 binary with `version.txt`, `commit.txt`, `toolchain.txt` and SHA-256 checksums.

**Do not publish unless PR CI, Direct-BLS candidate CI and guarded release verification succeed.** No validator deployment is performed by the workflow. Roll out to validators one at a time; preserve all chain databases, interchain observer cursors, durable votes, request journals and validator keys. Monitor per-source lag, provider rate-limit cooldowns, BLS quorum and successfully delivered transfers. Rollback binary: v3.5.13.
