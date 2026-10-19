# XGRChain v3.1.8 - Native ILN Recovery and Incremental Merkle Replay

This release hardens the **permissionless native ILN** validator path across **all configured EVM chains**. It does not modify IBFT consensus, destination smart contracts, chain-specific route admission rules or custody semantics.

## Source proof and performance
- Persist each independently reconstructed incremental Merkle accumulator per validator and source-chain/hook/registry start block, including all 32 branches and insertion count.
- Anchor every reusable checkpoint to the canonical source block hash; reject corrupt cache records, invalid metadata and changed block hashes. Reconstruct again from canonical logs after cache invalidation.
- Store checkpoints atomically after complete log batches, allowing an interrupted historical replay to resume after a validator restart.
- Preserve the existing strict ordered `InsertedIntoTree` checks. Cached state accelerates replay only; it is never external attestation evidence or a replacement for the full validated source operation, BLS signer set and destination delivery checks.
- Cold caches still require a one-time full canonical scan. The source RPC must expose historical receipts/logs. Any unavailable canonical evidence continues to fail closed.

## Quorum diagnostics and retryability
- Keep verified quorum admission hints until P2P gossip accepts the request, rather than silently discarding errors.
- Persist validator-local quorum progress through request deletion and process restarts. `xgr_getILNQuorum(route, messageId)` distinguishes `PARTIAL_QUORUM`, `RETRYING_VALIDATION`, and `AVAILABLE_UNVERIFIED` (plus legacy `PENDING`/`NOT_FOUND`), reporting validated local vote counts and required threshold when available.
- Expose bounded failure **categories**, not caller-supplied claims or signing authority. The actual immutable `xgr_getILNQuorumAttestation` endpoint and destination ISM remain authoritative.
- Existing local BLS votes remain durable and periodically rebroadcast. A failed or restarted worker must never require re-locking or re-burning tokens.

## Release acceptance criteria
- Go unit tests, contract/route ILN E2E, binary build, EEST and ForkGate must all pass before the release is published.
- Regression tests cover 2 of 3 independent BLS votes, restart-safe partial quorum status, cached replay, canonical source-hash reorg invalidation, cache corruption and invalid source logs.
- On the live Base -> XGRChain recovery, use the **original** source transaction and message ID. Only `Mailbox.delivered(messageId)` establishes destination delivery.

## Operational notes
- No new per-route ENV entries, source-chain approvals, smart-contract deploys or hardfork are required.
- Validators can be updated one at a time after release completion and verified chain synchronization; do not restart all PoS validators simultaneously.
- The release GitHub Action remains a **manual operator action**. Do not start it from this PR.
