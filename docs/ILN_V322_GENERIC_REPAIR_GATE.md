# XGR-Node v3.2.2 — chain-agnostic ILN checkpoint repair (release gate)

Status: INCOMPLETE — review/design and first narrowly scoped fix only.
Do not merge this candidate as a complete ILN repair, tag v3.2.2, or deploy a node based solely on this branch.

## Observed failures
- A source MerkleTreeHook can have **the same** canonical root and count across thousands of blocks. Nevertheless, the current replica file and BLS vote validation tie identity to a moving block epoch.
- Other nodes cannot load a valid, unchanged local replica because its stored checkpoint block differs.
- Receipt-verified source state is not necessarily available as historical eth_call on public RPCs, and a peer's unverified claim is never canonical authority.
- GossipSub request selection pins the first three peers and can starve the one complete honest replica provider.
- The current BLS vote receiver recomputes a NEW head-aligned 32-block epoch and rejects payloads whose checkpoint block differs. A bigger epoch window would only conceal the underlying race.

## Generic correction invariants
1. Merkle-content identity = source chain ID + canonical Hook address + root + count; checkpoint block is separate evidence metadata and must not identify leaf content.
2. Each replica re-anchor, cold join and extension **independently** reconstructs the complete 32-depth root from ordered leaves and checks against the canonical source Hook. Never accept a peer-supplied root without on-chain corroboration.
3. Confirmed source finality is verified through source-appropriate confirmation/finality rules; no fixed wall-clock assumptions, no hard-coded Arbitrum/Base special cases.
4. A BLS payload is immutable per proposal. Validators must converge on an explicitly selected *source-verifiable* checkpoint/validator-set tuple. Validation of an older proposal MUST verify that exact tuple, not a freshly selected later epoch. If a proof is not independently available, fail closed and re-attest safely; do not make historic claims using latest state.
5. Avoid routine historical **state** eth_call dependencies. Latest append-only values may be used only after proving immutability and canonical event inclusion/confirmation. Source block hashes, finalized logs/receipts or equivalent source consensus proofs anchor any old state claims.
6. Preserve all 2/3 BLS quorum checks, current destination setId, unique canonical message insertion, fee allocation, asset/route binding and destination-delivery constraints.
7. Peer selection must rotate across authenticated peers with bounded retries/backoff and avoid unbounded GossipSub RPC fanout. Requests/chunks for another recipient are benign off-target messages, not accepted data.
8. A wallet transfer and message ID MUST remain reusable through node restarts or re-attestation; no second burn or custody operation.
9. Record source RPC errors, quorum, peer attempts and confirmation states in truthful health diagnostics.
10. A completed release gate needs: fast/slow continuously inserting chains, quiet chains, different source RPC head views, old/stale checkpoint payloads, reorgs, pruned historical state, Byzantine P2P chunks, cold join, 3-validator 2/3 quorum, validator-set rotation and end-to-end delivered two-hop settlement.

## Changes already in this branch
- Reuse an unchanged local Merkle prefix across newer *independently sourced and confirmed* checkpoint epochs only after strict full-prefix hash reconstruction.
- Rotate bounded P2P replica peer attempts after retry deadlines, including peers outside the initial three.
- Preserve in-flight authenticated P2P Merkle chunks when a newly independently observed checkpoint changes only the block epoch, not the canonical root/count.
- Serve strictly reconstructed, verified earlier Merkle prefixes from newer complete provider replicas without trusting the peer as checkpoint authority.
- Query immutable source FeeVault allocation mappings using latest-state calls instead of historical `eth_call` state; message inclusion, destination validation and source finality checks remain mandatory.
- Regression coverage across arbitrary EVM chain IDs, increasing block heights, tampered leaves and wrong-chain/hook substitution.

## Missing before release
- Stable source-verifiable checkpoint proposal/attestation convergence without re-reading another epoch.
- Generic archive-independent source finality / fee qualification policy and corresponding fail-closed tests.
- End-to-end multi-peer partial-prefix completion / cold-join recovery verification (unit coverage for identity, peer traversal and earlier-prefix provider added; full cross-epoch network E2E still required).
- CI pass and independent security diff review.
- Merge with observational PR #42 and gated release PR #43 only after above checks.
- Staged validator rollout and live verified 2/3 quorum plus transfer settlement, not simply published binaries.

Do not downgrade or remove canonical, cryptographic or fee assertions to force a green test.

## Release review on 2026-10-22
- PR CI previously passed for the P2P fix and simulated quorum acceptance. This does **not** certify historical source checkpoint correctness.
- A later identical Hook root/count does not prove the claimed earlier block's root/count when insertion ordering or finality differs. The relaxed receive-side epoch comparison was removed (fail-closed restored).
- An immutable per-insertion Merkle prefix constructor and strict unit tests have been added, but **the production signing and receiving paths do not yet bind that prefix to the ORIGINAL successful Gateway/Dispatch/Hook receipt and its canonical block hash**. A block taken from a moving checkpoint is not an operation locator.
- The divergent-epoch BLS regression is currently a RED RELEASE GATE until correct source receipt lookup, independent source finality, validator-set rotation, quorum convergence and actual two-hop settlement are demonstrated.
- Do not merge PR #44 or #43, do not start workflow_dispatch, and do not tag v3.2.2 while these prerequisites are open. Existing deployed code must not be replaced by this branch.

## Generic Hyperlane-style simplification (2026-10-22)
- The 32-block checkpoint epoch is removed from runtime signing and P2P replica refresh. Chain identity is source ChainID + Hook, Merkle content identity is the independently read Hook root/count. This applies to arbitrary EVM sources; Arbitrum is only a regression fixture.
- The Hook state reader uses only `eth_call(..., latest)` for mailbox/count/root, reads count twice and refuses a block-height change during the read. This current-tree observation is an **untrusted transport anchor**, NOT a finality attestation for tip-only leaves.
- A validator reconstructs every leaf in the locally assembled/P2P-supplied tree, and signing/receiving require an independently confirmed original Gateway/Dispatch/Hook receipt, the deterministic insertion prefix, FeeVault allocation and the CURRENT destination BLS set. A peer never supplies finality or authorization.
- This branch still queries **historical transaction receipts and a one-block Gateway event range** for the original operation. It does NOT use historical `eth_call` state for the Hook or route, but cannot claim literally zero historical RPC reads yet. Removing receipt lookups safely requires a proven alternative original-operation source.
- CI and the strict multi-validator settlement proof must pass before merging or triggering v3.2.2. DO NOT release from a draft branch or failing CI.
