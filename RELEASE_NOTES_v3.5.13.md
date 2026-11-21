# XGRChain v3.5.13 — Receipt-pinned vote recovery (light release)

This release builds on v3.5.12 and includes [xgr-node PR #109](https://github.com/xgr-network/xgr-node/pull/109).

- A validator counts its own Direct-BLS vote using the evidence it has already verified. Peer validators still independently verify the source evidence before counting a vote.
- When available, an optional source transaction locator travels with the vote and is retained after restart. Previously stored votes can recover the locator from an existing queued request matching the route, message and block. The locator remains untrusted and the signed payload format is unchanged.
- These changes apply across Direct-BLS routes. The original source proof, destination validator-set checks and BLS signature verification remain required.

Run the manual Light Release workflow on `XGR3.0` with the reviewed merge commit and confirmation `RELEASE-v3.5.13`. The workflow checks the scope against v3.5.12 and publishes the Linux amd64 binary with SHA-256 checksums. Rollback binary: v3.5.12.
