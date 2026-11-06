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
