# Solana-native Direct-BLS codec (development only)

The sibling `xgr-interchain` branch defines fixed-width canonical Solana
asset, route and message hashes in `chains/solana/CODEC.md` and
`chains/solana/test-vectors.json`. This package mirrors the exact same
network genesis, 20/32-byte address distinctions, XGR hub restriction and
501-byte candidate BLS preimage.

These functions do NOT modify the EVM V315 codec, do not join existing
validator signing paths, and are NOT proof of valid source escrow/fee,
on-chain program identity, message delivery or replay resistance. Only
properly authenticated source observations and independently verified
destination rules can eventually authorize a BLS vote.

`go test ./interchain/solana` runs entirely offline. No SOL purchase is
required for codec or mock/locally executed Solana SVM validation tests.
