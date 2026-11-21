# XGRChain v3.5.6 — Interchain RPC deduplication (light release)

This light release builds on v3.5.5 and includes [xgr-node PR #102](https://github.com/xgr-network/xgr-node/pull/102).

## Changes

- The three periodic ValidatorRegistry reads share a single chain ID and finality verification. A healthy external chain now uses six RPC calls per validator per minute for this poll, down from twelve. Provider cooldown stops the remaining calls to that provider after a rate limit.
- Each route announcement uses one confirmed source route lookup. The receiver checks the announced gateway against the source registry and reuses that verified identity when attaching the route. On a healthy external chain, a published route uses nine calls instead of twelve. An accepted announcement for a configured destination uses nine calls instead of twenty-one.
- Destination and confirmed route read failures are attributed to the RPC endpoint that served the request. The Direct-BLS transfer path still requires matching complete finalized source evidence from two independent RPC operators.

## Release and rollout

- Manually dispatch the Light Release workflow on `XGR3.0` with the reviewed 40-character branch SHA in `expected_commit` and `RELEASE-v3.5.6` in `confirmation`. The workflow checks the exact commit and release file scope against v3.5.5, runs interchain, Direct-BLS and JSON-RPC regression tests, verifies the built version, and publishes the release.
- Verify `sha256sums.txt` and `commit.txt` before upgrading. Roll validators one at a time while maintaining quorum and preserving chain and interchain state. The v3.5.5 binary remains the rollback target. Publication does not deploy validators.
