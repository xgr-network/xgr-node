# E2E Test Catalog (PoS_3 / StakingV2)

This catalog documents the currently maintained E2E test surface under `e2e/...` (including `e2e/framework`) for the current PoS_3 / StakingV2 stack.

## Status legend

| Status | Meaning |
|---|---|
| active | Production E2E test currently exercised in the repository. |
| skipped | Test exists but is intentionally skipped (`t.Skip(...)`). |
| framework-only | Verifies E2E harness behavior rather than chain domain logic. |
| diagnostics | Observation-oriented test; useful for behavior evidence, not always a strict product invariant in every path. |

## Core chain / RPC / tx / sync

Representative active coverage:

- `e2e/discovery_test.go` → peer discovery and convergence
- `e2e/jsonrpc_test.go` → JSON-RPC baseline behavior
- `e2e/transaction_test.go` → premine, transfer, IBFT transaction loop
- `e2e/txpool_test.go` → txpool error codes, coalescing, pending retrieval
- `e2e/logs_test.go` → filter creation and log/block retrieval
- `e2e/websocket_test.go` → websocket RPC behavior
- `e2e/genesis_test.go` → genesis gas limit and predeploy checks
- `e2e/ibft_test.go` → IBFT transfer and fee-recipient semantics
- `e2e/syncer_test.go` → cluster catch-up / block synchronization

## PoS_3 / StakingV2 (selection, lifecycle, operations)

Primary coverage areas:

- Delegation pool defaults and open-flow (`pos_stakingv2_delegation_pool_test.go`)
- Validator lifecycle around min-stake, epoch gating, unstake/withdraw boundaries
- Delegator lifecycle (pool gating, active/inactive transitions, withdraw/exit)
- Tier transitions and fallbacks (Tier1 → Tier2 → Tier3 and recovery)
- Max-validator cap behavior and deterministic tie-breaking
- Small validator set liveness documentation (1/2/3 validators)
- Restart/resync behavior across Tier1/Tier2/Tier3 setups

## Beacon recovery & diagnostics

Beacon recovery diagnostics were removed from the maintained E2E surface because they exercised obsolete recovery-only paths.


## Intentionally skipped tests

- `TestBroadcast` (`e2e/broadcast_test.go`): skipped because topology/connectivity timing assertions are currently unstable in the harness.
- `TestPoS_StakingV2_FilteringAndFallback` (`e2e/pos_stakingv2_filter_test.go`): skipped due to known CI flakiness from startup timing around initial Tier1 set size.

## Framework-only tests

Harness tests in `e2e/framework/*` verify server manager behavior and binary resolution logic.

## Removed legacy tests

Legacy Polygon-origin PoS test suites are intentionally removed from the active catalog and are no longer part of the current PoS_3/StakingV2 baseline.
