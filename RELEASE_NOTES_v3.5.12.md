# XGRChain v3.5.12 — Quorum request retry (light release)

This release builds on v3.5.11.

- Quorum requests admitted for known routes remain queued through evidence backoff and transient validation failures. Deferred requests do not consume the worker's per-pass validation slots.
- The read-only status reports an orphaned retry as eligible for re-admission and retains the last diagnostic fields. Completed and partial attestations keep their existing status.
- Public discovery hints for unknown routes retain their expiry and admission caps. Validation and signing requirements remain in effect.

Run the manual Light Release workflow on `XGR3.0` with the reviewed merge commit and confirmation `RELEASE-v3.5.12`. The workflow verifies the release scope against v3.5.11 and publishes the Linux amd64 binary with SHA-256 checksums. Rollback binary: v3.5.11.
