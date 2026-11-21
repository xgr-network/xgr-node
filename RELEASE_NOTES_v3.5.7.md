# XGRChain v3.5.7 — Interchain health (light release)

- `xgr_getILNIndexerStatus` includes passive provider observations from the node's existing interchain operations. The snapshot exposes compiled public provider hosts, observed state, time, and active cooldown. Serving status reads the existing local health file.
- Gateway scan freshness follows the ten-minute fallback cadence with a two-minute allowance. Explicit scan failures retain their original diagnosis. Worker heartbeat freshness is still checked separately.
- The Linux amd64 Light Release uses the exact reviewed `XGR3.0` commit and the confirmation `RELEASE-v3.5.7`. Verify the SHA-256 checksums and commit before rolling validators one at a time. Preserve chain, vote, and cursor state when upgrading or rolling back to v3.5.6.
