# XGRChain v3.5.11 — Passive RPC health (light release)

This release builds on v3.5.10 and includes [xgr-node PR #107](https://github.com/xgr-network/xgr-node/pull/107).

- The Interchain health snapshot lists the public scanner and receipt witness endpoints from the active node policy and includes their existing observations. Providers that the node has yet to use appear with an unknown status.
- Health reporting exports public hostnames, operator IDs and roles. Collecting the inventory requires no provider RPC calls.
- [XITA Admin PR #456](https://github.com/xgr-network/xgr-interchain/pull/456) uses that inventory through the existing XGR status RPC. Receipt-only providers appear separately from scanner health.

Run the manual Light Release workflow on `XGR3.0` with the reviewed merge commit and confirmation `RELEASE-v3.5.11`. The workflow checks the scope against v3.5.10 and publishes the Linux amd64 binary with SHA-256 checksums. Verify the release commit and checksums before upgrading validators one at a time. Preserve chain and Interchain state. The rollback binary is v3.5.10.
