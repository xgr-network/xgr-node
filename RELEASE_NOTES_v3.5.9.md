# XGRChain v3.5.9 — Interchain scan finality (light release)

This release builds on v3.5.8 and includes [xgr-node PR #105](https://github.com/xgr-network/xgr-node/pull/105).

- External EVM Gateway catch-up scans reuse one finality-verified provider across their bounded log windows. In the healthy four-window case, this reduces RPC calls from 16 to 7. A replacement provider undergoes its own chain identity and finality checks.
- Route discovery reuses the verified head for the RouteAdded log scan, reducing its ordinary RPC budget from 7 to 4 calls. Canonical route binding, cursor checks and native XGR scanning remain in place.
- Direct-BLS transfer signing still verifies two independently sourced, complete matching witnesses.

To publish, run the Light Release workflow on `XGR3.0` with the exact reviewed merge commit and confirmation `RELEASE-v3.5.9`. The workflow verifies the release scope against v3.5.8, runs the affected tests and publishes the Linux amd64 binary with SHA-256 checksums. Verify the release commit and hashes before upgrading validators one at a time. Preserve chain and Interchain state. The rollback binary is v3.5.8.
