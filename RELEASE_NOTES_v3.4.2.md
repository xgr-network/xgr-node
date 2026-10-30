# XGR Node v3.4.2 — Interchain Direct-BLS remediation light release

**Status:** candidate until explicit review, merge into XGR3.0, successful tagged GitHub Action, and operator rollout.

## Changes relative to v3.4.1

- F-04: Rotate retry scheduling over all source routes within the existing per-route (2) and per-poll (24) bounds. Invalid durable retry entries are retained rather than signed; healthy independent siblings continue.
- F-20: Preserve undelivered quorum/attestation evidence when routes disappear, validator membership rotates, or periodic cleanup runs. Remove superseded local votes only following successful successor signing, and remove completed caches only after authenticated delivery.
- F-32: Bound on-disk admission of unknown/unverified route request directories to 128 and prune only expired requests for unregistered routes. Verified BLS votes/attestations and active recovery obligations remain intact.
- Existing BSC chain/domain 56 and receipt-pinned bindings, as well as Base, Arbitrum and XGRChain, stay unchanged.

## Required checks

Run repeated targeted Interchain, Direct-BLS, JSON-RPC quorum and F-04/F-20/F-32 regressions from immutable XGR3.0 commit; verify negative tests, no early deletion of active recovery evidence, and healthy sibling progress under corrupt journals. Check versioned build and embedded commit, signed release artifact checksums and scope gate. Full consensus/PoS/E2E suites are intentionally outside this Interchain-only light workflow.

## Audit and operations

This release provides candidate fixes, **not** a finding closure or independent audit sign-off. Retest the original F-04/F-20/F-32 audit reproductions after publication, and retain installation/health evidence. The three potential Medium downgrades require independent delta-audit confirmation. Publishing assets does not deploy validators; rollout requires a backup and controlled validator upgrade.
