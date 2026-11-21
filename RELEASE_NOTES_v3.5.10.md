# XGRChain v3.5.10 — Interchain catch-up and BSC witnesses (light release)

This release builds on v3.5.9 and includes [xgr-node PR #106](https://github.com/xgr-network/xgr-node/pull/106).

- Receipt-pinned BSC source verification prefers BNB Chain and bloXroute. Both produced matching complete source evidence for the transfer at BSC block 134450134 during preflight. These endpoints are used for receipt-pinned verification with a known transaction hash.
- After two independent source witnesses agree, the node caches nine immutable contract reads per endpoint for ten minutes. Two warm witnesses need 20 source RPC calls instead of 38. The node continues to verify each provider's finality, receipt, mutable route fields and per-message fee allocation on every attempt.
- External Gateway catch-up can scan up to 3,256 blocks per ten-minute poll using four regular scan windows. An explicit provider range-limit rejection triggers a narrower window. The validator health status now preserves committed cursor progress if a subsequent window fails.

Run the manual Light Release workflow on `XGR3.0` with the reviewed merge commit and confirmation `RELEASE-v3.5.10`. The workflow checks the release scope against v3.5.9 and publishes the Linux amd64 binary with SHA-256 checksums. Verify the release commit and checksums before upgrading validators one at a time. Preserve chain and Interchain state. The rollback binary is v3.5.9.
