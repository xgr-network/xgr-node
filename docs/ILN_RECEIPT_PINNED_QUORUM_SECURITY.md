# Generic receipt-pinned Direct-BLS source verification (review candidate)

## Scope
Receipt-pinned source transaction hints apply to **all externally verified EVM
sources** supported by the compiled chain policy: Base (8453), Arbitrum (42161),
Optimism (10), and BSC (56). They are not BSC-specific. The native XGRChain
(1643) source remains validated from the node's locally committed IBFT state,
without an external source RPC or a substitute trust path.

## Trust model (unchanged)
A source transaction hash is a **locator only**. It is never an attestation,
authority to sign, source finality proof, or permissionless route admission.
The validation sequence for each external source is:

1. Load the immutable compiled chain/domain/Registry policy and current
   on-chain append-only source route with the original Gateway, source router,
   Mailbox, dispatch hook, destination router and source Registry bindings.
2. Open **two independently operated** source RPC witnesses, each pinned to one
   endpoint for the entire verification. Confirm the chain identity and the
   configured chain-specific finality anchor (BSC finalized; Base/Arbitrum/OP
   safe). Do not downgrade to latest, confirmations or a single operator.
3. On each independent witness, fetch exactly the hinted source transaction
   receipt (no independent historical `eth_getLogs` lookup). Require the
   provided transaction hash AND exact RPC-native source block, a successful
   canonical receipt and matching finalized/safe block hash.
4. Parse the receipt's original `ILNOperation` and `Mailbox.Dispatch` logs.
   Require exactly one registered Gateway operation and matching dispatch for
   the requested Message ID, destination, route, fee, sender, recipient and
   immutable router/Mailbox bindings. Reject missing, duplicate, reverted or
   mutated receipts. All logs must belong to the same original transaction.
5. Independently verify the canonical Gateway FeeVault, its immutable route,
   governance authority, `allocatedOperation(messageId)==1` and a valid
   allocated set epoch, as well as the original source route.
6. Compare the **complete** independently verified operation, message,
   canonical block hash, receipt identity, source route and FeeVault snapshot
   across providers. The existing `sameILNWitness` comparator and quorum gate
   are reused without relaxation.
7. Revalidate the destination effective ISM, current BLS validator set,
   membership, synchronization and `Mailbox.delivered` status before signing
   the existing Direct-BLS wire payload. Preserve all durable votes, retries,
   recovery and replay protection.

## Data paths
- The chain observer already sees `ILNOperation.TransactionHash`; it forwards
  this as an untrusted hint to the existing signer. Durable observer retries
  already store `SourceTxHash`; retries reuse the bound original identity.
- `xgr_requestILNQuorumWithSourceTx(route,messageId,sourceBlock,txHash)` adds
  an optional *new* public hint path. The existing
  `xgr_requestILNQuorum(route,messageId,sourceBlock)` remains available for
  older/unknown transaction discoveries and continues its original verification.
- Verified hints propagated through authenticated validator gossip include the
  source transaction hash; each peer independently repeats the entire proof.
- XITA recovery uses the transaction hash already extracted and finalized from
  the original source receipt, never a user-provided signature or witness result.

## Non-goals / operational release gates
- No consensus, fork, PoS, BLS encoding, wallet key, token deployment,
  on-chain contract or signature threshold modification.
- No new source of trust in XITA indexers, an external DB, a web service,
  another validator's observation, or an unsigned hint.
- Discovery of unknown transfers still uses bounded `eth_getLogs`; receipt
  pinning removes the extra log lookup only for a known source transaction.
- This improvement does **not** remove dependency on independent providers for
  receipts, finality and current Registry/FeeVault state, and cannot make two
  unavailable providers magically available.
- Do not merge or release until focused Go/node tests, negative/Byzantine
  fixtures, one-provider failure checks, and a real **read-only** historical
  transaction replay on each supported external source pass. Deploy validators
  one at a time only after explicit operator approval. Preserve the pending
  53,703,582 XGR BSC transfer and its original journal throughout.
