# ILN validator-owned Merkle replica, v3.2.0 (experimental)

## Scope
This patch adds an isolated, fail-closed replica verification and atomic persistence
primitive in `interchain/evm`. Validators may ingest 1024-leaf ordered chunks from
untrusted peers and independently reconstruct the Hyperlane Merkle root before
accepting them. It is not yet network-wired and does NOT replace historical Gateway
scans, relayer recovery or provide any RPC endpoint.

## Requirements before enablement
- Fetch the canonical confirmed checkpoint independently from the correct
  source chain and verify its finalized block hash/finality as appropriate.
- Authenticate bounded chunk requests and peers; allow multi-peer retry,
  enforce download and disk quotas, TLS/libp2p identity and no DoS amplification.
- Make proof data and exact Dispatch bytes available through read-only node RPC
  without depending on a company-operated relayer.
- Add an untrusted candidate/exact fee-qualified operation evidence layer;
  validate direct fee payment by immutable registered gateway and FeeVault.
- Never treat local Merkle checksums or 2 peer signatures as an alternate
  source of trust; verify canonical on-chain root.
- Sync route discovery and operation logs too; cold start cannot require
  archived eth_getLogs. Support independent full backups to avoid correlated
  validator loss.
- Test peer join, multiple corrupt peers, disconnect, reorg, quota, 2/3 quorum,
  10k concurrent operations and destination settlement on a full local fixture.

## Hard limitations
An authenticated root can validate a COMPLETE Merkle prefix but cannot
recover the missing messages. A new node must receive authentic data from at
least one available storage peer or backup; a historical node full reset
without any surviving replicas remains information-theoretically unrecoverable.
This patch deliberately remains off the consensus execution hot path.
