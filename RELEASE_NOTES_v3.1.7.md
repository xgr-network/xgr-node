# XGRChain v3.1.7 - Archive-independent ILN source verification

**Status:** Draft release candidate. Deploy only after CI, independent signer review and two-of-three validator testing.
**Repository:** `xgr-network/xgr-node`; target `XGR3.0`.
**Baseline:** v3.1.6 validator admission / Registry V3 fix branch.

## Mandatory invariant for all supported chains

**No XITA/ILN validator may require historical EVM contract state.** This holds for XGRChain as a source and for Base, Polygon, XDC and future EVM destinations when acting as sources. This does **not** eliminate the requirement to obtain canonical historical receipts, logs and block headers; RPC providers with pruned receipt/log history may need a separately verifiable data-availability mechanism. Never bypass evidence checks.

### Archive-free route identity

- Use the live `getRoute()` from the canonical append-only V315 registry, gated by its immutable `factory()` getter.
- Verify immutable Gateway/router/Mailbox/Hook bindings via current calls.
- Authenticate each original source operation by a confirmed **same-transaction** Gateway `ILNOperation` and Mailbox `Dispatch` receipt. The validator fee in the original event (not today's global fee) is authoritative for that transfer.
- Mutable/unknown registry implementations without an authenticated append-only guarantee are rejected. Do not silently apply this shortcut to mutable V314 deployments.

### Archive-free checkpoints

- Reconstruct the Merkle hook's incremental tree from **canonical** `InsertedIntoTree(bytes32,uint32)` logs at or before the signed block.
- Enforce contiguous insertion indices beginning at zero, exact hook address/topic, non-zero message IDs and stable canonical log order.
- Replay from the immutable source registry activation height first; if the hook predates that registry, safely retry the reconstruction from block 1.
- Apply the same proof rules in all directional routes; each validator independently reconstructs and checks.

### Remaining rollout requirements

1. Run `go test -short ./interchain/evm ./interchain/runtime ./jsonrpc` and the repository CI suite.
2. Review historical log availability and rate limits for all configured EVM RPCs; do not claim universal archival receipts/blocks.
3. Upgrade the public source RPC worker and **at least two of three** participating Base validators; ideally all validators.
4. Patch the XITA unsigned recovery helper's separate historical `getRoute(..., blockTag)` call before retrying the existing XGR->Base message.
5. Do not submit a new source lock. Simulate destination `Mailbox.process()` before sending any Base transaction.

This release contains off-chain ILN validation changes, **no chain consensus rule or deployed smart-contract change**.
