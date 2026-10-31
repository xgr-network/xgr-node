# XGR Node v3.4.3 — OP Mainnet Direct-BLS interchain light release (preparation only)

**Status: PREDEPLOY / NOT RELEASED / NOT DEPLOYED.** No release tag, node upgrade, or validator activation is authorized by this branch.

## Proposed chain policy

- OP Mainnet chain/domain 10, native ETH, EIP-2537 Direct-BLS.
- Compiled policy requires `ethereum-safe` with canonical numbered-block hash agreement, with no latest/confirmation fallback, analogous to Base.
- Four proposed HTTPS RPC endpoints from independent operators (OP official, PublicNode, dRPC, Tenderly). Live witness independence, archival log support, and `safe` tag/positive-and-negative BLS precompile vectors must be proven before use.
- **Do not activate** OP Mainnet while ValidatorRegistry/SourceRegistry addresses or SourceRegistry receipt-native deployment block are unknown.
- The `ProposedOptimismPolicy` helper is deliberately absent from active `compiledChains`. Fail-closed unit tests prove it cannot be used by validator workers.

## Completion gate for the actual 3.4.3 light release

1. Merge the declarative OP chain/bootstrap config into xgr-interchain main; independently check 4 RPC chain IDs, `safe` block hashes, and EIP-2537 vector responses on OP Mainnet.
2. Have operator review initial Registry reserves and source-native fee. Admin deploys the missing EIP-2537 verifier, RegistryV3, ISM, transport, router deployers, Factory, Factory-owned SourceRegistry and optional ExecutionGateway via MetaMask.
3. Admin must publish real transaction receipts, runtime code hashes, exact SourceRegistry creation height and canonical contract binding proofs into `deployments/chains/optimism.json`.
4. Replace this non-executable candidate with an actual `compiledChains` entry only after verifying the immutable receipt-backed Registry and SourceRegistry values and RPC-native activation block.
5. Add tests for `Policy("optimism")`, `LoadAll` count five, finalized/safe header agreement, independent 2-operator witness quorum, duplicate/incorrect proofs and graceful RPC failures. Preserve currently deployed chain policies exactly.
6. Add `release-light-v343.yml` with permitted interchain-only changes, tests, signed versioned artifacts and existing v3.4.2 baseline. Node/PoS/consensus sources remain out of scope.
7. Explicitly roll out the new binary to validators, verify quorum and source scanning, then perform small XGR ↔ OP Mainnet transfers and an external→XGR→external two-hop regression.

**Never treat a prepared PR or zero-address placeholder as an installed production contract.**
