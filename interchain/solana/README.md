# Solana RPC adapter (development only)

This package is the first read-only Solana-specific adapter on the XGR3.0-based
`feature/solana-interchain` branch. It leaves the active EVM worker, chain
policies and Direct-BLS consensus path unchanged.

- Solana cluster identity: **genesis hash**, not EVM `eth_chainId`.
- XITA/Hyperlane Solana messaging domain: **1399811149** (not a Solana chain ID).
- Independent operators: no counting two endpoints of one company twice.
- Source read: `getTransaction` with `commitment: finalized`, raw base64
  transaction, matching exact bytes/slot, verified signature position.
- No `confirmed`, `processed`, or synthetic confirmation fallback.

**NOT an XITA proof and NOT enabled for signing.** A Solana RPC response alone
does not prove the instruction came from the canonical XITA program, that
token custody changed correctly, that fees were allocated or that a route
matches the destination. The release-reviewed programs, escrow/state proofs,
wallet/account bindings, source fee proof, and destination current-set Direct-BLS
bindings must be implemented and audited BEFORE connecting this adapter to
the signing worker. Do not add Solana to `interchain/config.go` until that gate.

The XITA repository hosts the matching `config/chains/solana/` plan and
`chains/solana/` program/deployment structure.

## Native XITA source-evidence candidate (still NOT a BLS signature)

The `evidence.go`, `pda.go` and `account_witness.go` helpers provide a
strict, read-only candidate checker for the matching Solana SVM program.
They reject noncanonical transaction encodings, program IDs, SPL Token
program or Rent sysvar substitutions, wrong 16-account BridgeSourceV1
instruction layouts, forged canonical message IDs, mismatched Borsh source
dispatch and fee receipts, inconsistent locked/burned principal or SOL
validator-fee amounts, and incorrect PDA derivations.

Receipt account bytes, owner and rent balance are compared independently
across at least two finalized RPC operators. The program-derived addresses
use Solana's SHA256 / Ed25519 off-curve derivation with canonical seeds and
the actual pinned 32-byte Program ID. Solana's System Program is the
**all-zero** 32-byte Pubkey; the zero address is mandatory for this role.

This is still only **candidate structural verification**, and its input
balance movement data is not yet sourced from the independently verified
transaction meta CPI execution record. Before any validator can sign:

1. Cross-check transaction meta token and native SOL pre/post balances,
   executed inner token CPIs and exact account indexes at independently
   finalized source slots.
2. Verify original Route/Router accounts, source fee policy and current XGR
   validator-set PDA ownership, content and canonical route/asset identity.
3. Pin the immutable deployed SBF ELF/ProgramData and origin program ID in
   release receipts, independently verified by the validators.
4. Cover versioned/lookup-account transactions safely or explicitly reject
   them. Current observation intentionally rejects them.
5. Require the full policy to pass at two independent RPC operators and
   record explicit evidence/decision receipts, and only then connect the
   result to the Direct-BLS worker. No optional UI sponsor can bypass it.

Neither the current source candidate checker nor the Solana RPC observer is
called by the production XGR node Direct-BLS signer. The XGR Mainnet and
existing EVM policies remain unchanged.
