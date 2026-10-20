# XGR-Node v3.2.0 — validator-native Hyperlane Merkle transport

**Status: draft security release candidate, not production-deployed.**
Node PR #38 and XITA PR #175 remain Draft until the live chain-by-chain
source-root, fee allocation, ISM and wallet-to-Mailbox transfer tests pass.
Do **not** redeploy current audited Solidity contracts solely for this patch.

## Security invariants
- Hyperlane Mailbox and MerkleTreeHook provide the source messages and
  append-only 32-sibling Merkle root. The destination ISM verifies root
  inclusion of the **exact** authorized message, 2/3 BLS aggregate of the
  current validator set, source/destination domain, and route binding.
- The ILNGateway is a permissionless source contract. Every direct transfer
  atomically charges the validator fee, locks/burns and dispatches, then
  calls its immutable FeeVault.allocate(messageId). The optional company's
  XITAExecutionGateway, quote and gas sponsorship are NOT consensus inputs.
- Validator fee eligibility now comes from the registered source gateway's
  `feeVault()`, immutable `gateway/routeId/destinationDomain/validatorRegistry`
  and `allocatedOperation(messageId)` + `allocatedSetId(messageId)`
  **at the confirmed checkpoint block**. Replaying original Gateway fee
  logs or comparing the old paid amount to a later governance fee is wrong.
- Signer sourceBlockNumber is the confirmed Hook checkpoint epoch in v3.2.0,
  *not* the earlier wallet transaction block. This stays within the
  already deployed `XGR_ILN_CHECKPOINT_V2` ABI; no Solidity change is required.
  The signed nonzero validatorFeeWei reflects source policy read for the
  checkpoint; the V3.1.5 FeeVault does not store exact per-message fee Wei.
- Source checkpoint is at floor((sourceHead-confirmations)/32)*32. A BLS vote
  is valid for signing only after independent source RPC verification of the
  Hook mailbox, root and count, plus locally verified full Merkle prefix and
  inclusion path for the exact message.
- 2/3 malicious validators remain a systemic risk inherent to this chosen
  BLS trust assumption. A corrupt single peer or a corrupt local file
  cannot cause signing of an invented Merkle root.

## Validator-owned data and peer recovery
- Each `data/interchain/iln/replicas/<sourceChainId>/<hook>.json` contains
  ordered Hyperlane message *hash leaves*, its root, count and checkpoint block.
- libp2p GossipSub sends bounded 256-leaf chunks, addressed to a specific
  authenticated network peer. Unknown, unsolicited, truncated and out-of-order
  responses are rejected. Assemble each responder's candidate independently.
  Only a 100% complete candidate matching the source Hook root is stored
  with atomic fsync+rename. A corrupt peer cannot poison another peer's
  independently verified snapshot.
- A continuously running validator can append to its verified prefix using
  strictly recent, confirmed Hook insertion logs (at most 128 blocks).
  Missing/stale history requires an intact verified peer snapshot or backup;
  a historical archive RPC is not the mandatory source for normal transfers.
- The source Hook root DOES NOT contain leaf history or complete serialized
  messages. Initial seeding MUST have intact data, and normal data-dir
  backups must include these replica files and the corresponding message
  history. All peers and backups lost means irrecoverable history; there is
  no cryptographic inverse of the Merkle root.
- Existing v3.1.x node data-dir backups do **not necessarily** contain these
  new files. Do not roll all validators in parallel before one complete
  source-anchored seed for every active Hook has been verified.
- Public `xgr_getILNReplicaProof(route,messageId)` returns canonical Merkle
  siblings plus the BLS quorum from the node's read-only files. It does not
  sign, transfer, sponsor gas or depend on XITA's relayer. Clients must get
  the full original serialized Hyperlane message independently (e.g. the
  confirmed source Mailbox.Dispatch transaction receipt or preserved copy),
  ABI-encode the returned fields into ISM metadata and send
  `Mailbox.process(metadata,message)` with their OWN wallet. The destination
  contract remains the verifier; a node's JSON response is not settlement
  proof by itself.

## Validator execution
- Worker scans the verified Hook insertion prefix using a per-route,
  per-validator-set `leaf-cursors` file. Unallocated leaves from other routes
  are skipped **only** on a definitive FeeVault absence result.
  Transient source RPC errors fail closed without advancing the cursor.
- Public quorum requests are hints; their old `sourceBlockNumber` is not
  authority. The signer independently checks the confirmed source root,
  message inclusion, FeeVault and destination delivery state before BLS.
- On validator-set or root epoch rotation, pending undelivered votes are
  re-attested against the current canonical source Hook.
- No validator signs based only on a peer's mutable Merkle index.
- Local BLS signing and fee checks are NOT part of IBFT PoS block consensus.
  Interchain errors must never block L1 block production.

## Automated regression evidence
- Go tests for 2/3 signing, gateway fee allocation and mismatches,
  unallocated messages, recent-only update, root/inclusion proof sizes,
  tampered persistent snapshots, history-pruned failure, and restart.
- Real three-validator libp2p test for authenticated chunk transport,
  honest cold join despite malicious peers, proof construction and
  failed restore from a corrupted data-dir replica.
- XITA Foundry tests for mandatory protocol fees, permissionless
  direct gateway transfers, and separation of optional company charges.
- The XITA compatibility CI verifies that production Solidity sources
  remain unchanged and existing documented deployment receipts retained.

## Outstanding mainnet release checks
1. Verify or seed **actual production** replicas for both XGRChain and Base
   from complete canonical leaf/message history, independently checking the
   source Hook roots. Do not assume an old data-dir backup already contains
   the new Merkle prefix format.
2. Verify that local public RPC serves a valid proof/attestation for the
   already pending message, and that both 2-of-3 BLS votes and the deployed
   destination V3.1.5 ISM accept it.
3. Execute and assert full source gateway fee+lock → confirmed checkpoint →
   quorum → ISM/Mailbox.process → delivered and token settlement, in both
   directions, with XITA's relayer and service fee disabled.
4. Simulate one validator offline, corrupted/disconnected peer, reboot,
   source RPC pruned 403/429/503 and validator-set rotation on the actual
   network with consistent data-dir backups.
5. Bound per-Hook CPU/memory RPC amplification and evaluate large Merkle
   prefixes; a 1,000,000-leaf cap is currently enforced by snapshot format.
6. Re-review the implementation/security diff and verify contract runtime
   hashes onchain. CI passing alone is not production approval.

**No v3.2.0 tag, merge or coordinated deploy until this gate passes.**
