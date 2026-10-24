# XGR Interchain v3.3.2 — release boundary and security rationale

## Scope and hard exclusions

This release refactors **Interchain only**. It does not modify IBFT block
proposals, block voting, proposer rotation, EVM execution, PoS staking,
slashing, chain state or consensus network protocol. The only file directly
inside the IBFT package changed by the runtime migration is
`consensus/ibft/interchain_runtime.go`: it starts the optional Interchain
worker separately from consensus, and skips it while no contract bindings exist.

The Direct-BLS signing payload, Ethereum contracts, destination ISM, 2/3 quorum
and permissionless token/route registration are NOT changed.

## No validator-specific Interchain environment

- `interchain/config.go` is the source of chain IDs, Hyperlane domains,
  allowed BLS verifier format, RPC endpoint and native finality mode.
- No `XGR_INTERCHAIN_*` variable is consulted by the runtime to configure
  signing, routing, validator membership, confirmation depth or fees.
- Every validator obtains ECDSA/BLS secrets from the existing XGR secrets
  manager. Secrets MUST NOT be compiled into the Go binary.
- ValidatorRegistry V3 minimum reserve and reimbursement limits are queried
  from on-chain contract state, never from ENV.
- Interchain route discovery is permissionless and uses the source registry,
  NOT static route IDs or route ENV.
- The admin/user-initiated chain transactions remain wallet/validator-owned.

## Direct-BLS finality (PRE-S02)

- XGRChain 1643: the IBFT-committed latest head.
- Base 8453 and Arbitrum 42161: `eth_getBlockByNumber("safe", false)`.
- Polygon and unknown chains: NOT enabled without a separately reviewed
  finality policy.
- A finality block must have a valid nonzero height/hash, and the numbered
  canonical block must match the returned finality block hash.
- A source receipt must have the original successful tx/hash/block and lie
  at or behind that chain-native finality head, with matching canonical block hash.
- Finality RPC unavailability, unsupported `safe` tags, mismatched block
  hashes or chain IDs **must not fall back to latest or N confirmations**.
- `safe` represents the rollup's L1-posted/canonical safety boundary.
  It is NOT equal to Ethereum `finalized`. The remaining settlement/finality
  risk is documented and accepted until a future explicit policy upgrade.
- Quorum members each check source evidence independently; no central route
  admission authority is introduced.

## v3.3.2 / v3.3.3 deployment separation

**v3.3.2 contains ZERO deployed contract addresses.** Every chain policy
has empty ValidatorRegistry and ILN source Registry bindings. Hence
`LoadAll()` returns no active destinations, the Interchain worker does not
start or sign, and XGR PoS/IBFT keeps operating normally.

**v3.3.3 will pin new, audited deployment receipt data**: one ValidatorRegistry
and one source Registry per chain, together with exact RPC-native deployment
block heights. Never hard-code addresses in tests and never infer Arbitrum RPC
block heights from Solidity `block.number` (which may reflect an L1 origin).

Contract code and source receipt lineage must be cross-checked before
re-enabling Interchain. An unset or inconsistent receipt binding fails closed.

## Cleanup

Legacy MerkleTreeHook/Replica, checkpoint-root signing, per-validator route
ENV, and unused standalone Merkle tree implementation are removed from the
Interchain release. The generic XGR EVM state and transaction Merkle/Patricia
trie and consensus roots are **not** removed. Retained
`ilnCheckpointVote` / `CheckpointRoute` wire/API names are historical labels
for Direct-BLS message votes and dynamically discovered route identities, not
Merkle commitments; renaming the P2P wire name would require an independent
protocol migration and is intentionally out of scope.

## Release acceptance

1. Full XGR Go compilation and CI.
2. Interchain tests: unsupported safe, unavailable safe, canonical block hash,
   wrong chain ID, unknown chain, and no-ENV deployment activation.
3. Inspect `git diff v3.3.1..HEAD` for modifications to consensus-critical
   components. They are forbidden.
4. Validate no Interchain signer/workers can start without v3.3.3 bindings.
5. **No automated deployment or tag publication without reviewed manual workflow.**
