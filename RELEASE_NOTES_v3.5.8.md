# XGRChain v3.5.8 — Interchain RPC budget (light release)

This release builds on v3.5.7 and includes [xgr-node PR #104](https://github.com/xgr-network/xgr-node/pull/104).

- Each validator reuses its independently verified route discovery observation for sent and received notices for up to 15 minutes. Concurrent notices share a lookup. The first lookup and the lookup after expiry still verify the source registry. An announced gateway mismatch is rejected.
- The node preserves RPC responses from numeric block verification and respects provider cooldown after rate limits on source verification calls, including FeeVault reads. Direct-BLS source verification still requires two matching, complete witnesses from independent operators.
- With three mutually connected validators, two BSC source routes and notices exchanged every five minutes, the steady-state route-notice budget falls from about 32.4 to 3.6 BSC calls per minute across those validators.

To publish, run the Light Release workflow on `XGR3.0` with the exact reviewed merge commit and confirmation `RELEASE-v3.5.8`. The workflow verifies the scope against v3.5.7, runs the affected regression suites and publishes the Linux amd64 binary with SHA-256 checksums. Verify the release commit and hashes before upgrading validators one at a time. Preserve chain and Interchain state. The rollback binary is v3.5.7.
