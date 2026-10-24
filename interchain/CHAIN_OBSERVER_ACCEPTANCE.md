# ILN ChainObserver – required follow-up after v3.3.3

**Status:** OPEN / NOT IMPLEMENTED in PR #62. This is not a completed feature.
**Target:** independent follow-up PR on XGR3.0. No change to normal IBFT consensus.

## Scope and architectural invariants

- Exactly one ILN ChainObserver per **source chain per validator node**, never a network-wide shared provider. Every validator independently discovers and verifies messages.
- XGRChain (1643): consume each locally committed canonical IBFT block/receipt once and demultiplex valid Gateway/Registry logs by route ID. No XGR external RPC, no extra XGR node.
- Base/Arbitrum: group gateway addresses in bounded `eth_getLogs` calls, respecting per-provider address/range limits, `safe` finality and provider cooldown. Full block downloads are not a replacement for receipt logs.
- Route discovery remains permissionless; integrate with existing persistent source-registry discovery and late-joiner route catalog instead of duplicating it.
- Optional P2P/sequencer listeners later only as source adapters, never substitutes for two independent complete EVM source witnesses.
- Observer output is strictly **untrusted discovery hints**. BLS signing MUST continue to call the independent full receipt + canonical Mailbox Dispatch + Gateway/Registry + FeeVault checks; no combining fragments across RPC witnesses.
- No central custody, centralized service, new Interchain ENV, external full node, or change to ordinary IBFT consensus.
- Chain-specific finality stays in compiled chain policy.

## Required acceptance criteria

1. One read/scan of each canonical XGR source-block window per validator regardless of number of routes. Tests cover at least 3 Gateway routes plus RouteAdded in the same block and prove only one read per block.
2. External source scan uses a single bounded `eth_getLogs` request with multiple gateway addresses for compatible providers; reliable splitting if provider rejects batch limits; no route-by-route duplicate scanning.
3. Events are routed by canonical source domain, destination domain, gateway, route ID, message ID and block/log identity. Different routes with similar message IDs never collide.
4. One durable, registry-version-qualified **source-chain cursor** per observer; atomically persist events/delivery work before advancing. Retry and restart never lose events; duplicate delivery is idempotent.
5. New routes backfill from their canonical activation/deployment block. Disabled/retired routes may still require historical proof. Chain reorgs or regressed `safe` heads fail closed and are not silently skipped.
6. Adaptive catch-up from PR #62 is retained and integrated once at observer level. Tests simulate fast Arbitrum block growth and show the observer converges with finite scan budget and provider limits; 429/403/503, timeout and RPC failover preserve cursors.
7. Health metrics expose per-chain finalized head, cursor, lag, query rate, 429/backoff, provider identity and per-route processing counts. Source-head lag is distinct from block production/validator synchronization lag.
8. Security regression: forged/inconsistent receipt, different independent RPC operators, mismatched route/gateway, wrong Mailbox Dispatch, missing FeeVault fee and replay all fail closed, regardless of observer hints.
9. Integration test with 3 ILN validator nodes and representative XGR->Base, Base->XGR and Arbitrum->XGR transfers including validator restart and recovery.
10. No changes outside `interchain/` except narrowly justified read-only ILN adapter and dedicated workflows; no ordinary PoS/IBFT consensus, TxPool or EVM-state-transition changes.

## PR #62 / v3.3.3 evidence gate – separate from ChainObserver

- Native XGR source receipt, Mailbox Dispatch, FeeVault and Registry evidence: deterministic positive and negative tests (`interchain/evm/iln_native_positive_test.go`, `iln_native_evidence_test.go`).
- Native destination read and TxPool errors: `interchain/evm/iln_native_destination_test.go`; integration proof of a signed XGR local TxPool admission, canonical committed receipt and post-state validation is **still required**.
- Actual bidirectional transfer E2E: XGR->Base/Arbitrum and Base/Arbitrum->XGR must be exercised before **production deployment**; existing 3-validator replica tests alone do not prove all these destination contracts.
- A green candidate build or grep-based scope check must never be reported as an on-chain E2E success.
- PR #62 adds bounded per-route catch-up in `interchain/runtime/iln_scan_catchup.go` and tests. Route-based duplicate scanning is intentionally not claimed to be fixed. The ChainObserver follow-up supersedes the per-route scanner without relaxing full signing evidence.
