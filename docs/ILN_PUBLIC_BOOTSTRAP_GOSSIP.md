# Public ILN bootstrap proofs over validator P2P

The three existing XGRChain PoS / ILN validators are the sole origin of
cryptographic bootstrap Proofs of Possession (PoPs). **XITA is installed only on
validator 1.** Validators 2 and 3 do not run any XITA service or new HTTP daemon.

## Opt-in for a new domain

On each of the three existing validator nodes, set the same local operator
allowlist in the XGR node service environment:

```ini
XGR_INTERCHAIN_BOOTSTRAP_DOMAINS=42161
```

Multiple independently approved destinations may be comma separated. Updating
the variable requires rolling the individual node service so its environment
is reloaded; do not restart all IBFT nodes together. No private key, genesis
snapshot, or on-chain Registry is needed for these PUBLIC proof messages.

When the ILN worker is synchronized, each participating active validator
constructs `XGR_INTERCHAIN_BOOTSTRAP_V1` with immutable XGR origin chain ID
1643, configured destination domain (42161 for Arbitrum), its own validator
address, and its own existing compressed/uncompressed BLS public key. It
signs locally with the EXISTING BLS secret. Only the public signature,
public keys, and exact payload are published on the existing authenticated
`/xgr/interchain/3.0.0` gossip topic.

Every receiving node independently verifies:

- its own explicitly enabled destination domain;
- canonical binary bootstrap payload and signature wire-format correspondence;
- BLS Proof of Possession over the exact domain-bound payload;
- an **active** XGR PoS validator with an exactly matching BLS public key.

Validated public records are atomically persisted under
`<node-data-dir>/interchain/bootstrap-public/42161/<validator-address>.json`.
A disconnected node can cold-recover records as peers rebroadcast them every
two minutes. Missing, invalid or incomplete signatures are never filled in,
guessed, or accepted with an insufficient quorum.

## XITA collector on validator 1 only

The same node's existing read-only XGR JSON-RPC exposes:

```json
{"jsonrpc":"2.0","id":1,"method":"xgr_getILNBootstrapProofs","params":[42161]}
```

The response is a public list, possibly incomplete, and is **not itself
deployment authority**. XITA matches all three identities in
`config/validators/initial.json`; each proof is rechecked against the
destination domain and the independently documented destination BLS verifier,
then the ValidatorRegistry constructor verifies the same proofs again.
Deployment remains wallet-signed and requires a verified on-chain receipt.

No validator secret keys, XITA databases, private HTTP endpoints or
Interchain wallet permissions are ever shared with peers.

Only XGR nodes require the new gossip feature on all three validators.
XITA's Admin is installed and updated on validator 1, as before.
