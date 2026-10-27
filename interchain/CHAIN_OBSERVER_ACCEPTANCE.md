# ILN ChainObserver implementation and acceptance boundaries

Current implementation includes grouped per-source observation in `runtime/iln_observer_worker.go`, native/multi-Gateway readers, deterministic dispatch, bounded adaptive catch-up and durable failed-operation retries. The old blanket “not implemented in PR #62” statement is obsolete.

## Implemented behavior

- Each validator independently observes configured source networks. XGR uses its own locally committed native chain; external sources use bounded multi-Gateway log windows.
- Grouped windows demultiplex by Gateway, destination domain, route and canonical transaction/log/message identity. Observer results are untrusted hints.
- Every signing attempt still verifies complete original source receipt/dispatch, route and FeeVault evidence; external signing requires two matching independent operator witnesses.
- Failed operations are fsynced into identity-qualified retry records before cursors advance past them. Full/conflicting/unwritable journal holds progress; retry reruns full verification.
- Source catch-up is bounded/adaptive; regressed heads fail closed. Current health remains per-route with worker/schema-2 source lag diagnostics.

## Important difference from the original target

Durable scan cursors remain **per-route**, and shared source windows begin from the minimum relevant route cursor. One durable registry-version-qualified source cursor was an architectural target, not the current storage model. Do not claim all original acceptance criteria are complete merely because grouped scanning exists.

## Evidence and remaining deployment gates

Deterministic suites include `runtime/iln_observer_worker_test.go`, `iln_observer_retry_test.go`, `iln_chain_observer_test.go`, `iln_scan_catchup_test.go`, `evm/iln_multi_gateway_scan_test.go`, `rpc_evidence_quorum_test.go` and native evidence/destination tests. Read each assertion for its precise coverage.

Actual bidirectional XGR/Base/Arbitrum execution, a three-validator deployment with restart/recovery, destination native TxPool commitment, full source-history catch-up and all required provider-limit/failover scenarios need separately recorded evidence. A green unit suite does not establish those production results. Additional per-chain query/provider counters are not implied by current per-route health output.

[Complete runtime, persistence, monitoring and failure semantics](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_Validator_Runtime.md).
