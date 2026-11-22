# Shared interchain protocol — BLS V3 reference

The reference is the released BLS V3 behavior. ECDSA is a cryptographic
adapter for the same destination-local membership and direct-message
protocol. It does not create a second worker or import the whole XGR PoS set.

## Ownership of decisions

| Layer | Responsibility |
| --- | --- |
| `interchain/runtime` | Explicit destination opt-in, XGR PoS eligibility and synchronization, candidate consent, deadlines, current set, source receipt/route/fee evidence, explicit governance approval, gossip, persistence and recovery |
| `interchain/authorization` | Explicit scheme selection, typed operation encoders/decoders, possession proofs, individual signatures, canonical signer indices, quorum assembly and verification |
| `interchain/evm` | Configured registry bindings, normalized ordered sets, contract ABI encoding, native/external reads, gas/reserve checks, submission and confirmed post-state |
| `interchain/config.go` | Immutable release policy and activation bindings; an adapter's existence does not activate a chain |
| `jsonrpc` and `command/ibft/interchain` | The existing read/control surface, including `set-active`, bootstrap proof, and fee create/approve/execute |

Transfer authorization uses the **destination** registry and its configured
scheme. Source-fee governance uses the **source** registry and its configured
scheme. Source finality remains a separate chain policy.

## Common lifecycle and authorization

Every destination has its own explicitly bootstrapped, ordered validator set.
Being active in XGR PoS alone grants no destination membership. ADD requires
the candidate's consent and possession proof, current-set quorum and funded
reserve. Voluntary REMOVE requires consent; forced REMOVE requires loss of
XGR eligibility. Neither scheme may remove the last member.

Every individual vote and completed quorum is checked against the canonical
registry order and set ID. The threshold is `ceil(2*n/3)`, including 2 of 3.
Candidate requests, public quorum hints and peer messages cannot bypass the
same eligibility and evidence checks. Governance proposal receipt/recovery
never approves a fee change; approval remains an explicit local action.

There is one direct-message signing path. It independently verifies the
source operation, confirmed receipt, fee allocation, route, destination ISM,
current destination set and undelivered message. It rechecks synchronization,
current membership and local signing identity before signing. A local signed
vote is persisted before it is published.

After rotation, historical votes remain evidence but cannot authorize current
delivery. A replacement uses the same source message with the current set ID.
Recovery retains the old vote until an identical successor, except for epoch,
is present in the durable journal. Both schemes use this same procedure.

## Cryptographic differences and compatibility

| Item | BLS V3 | ECDSA adapter |
| --- | --- | --- |
| Local secret | Existing `consensus/validator-bls.key` | Existing `consensus/validator.key` |
| Identity | Validator address plus registered compressed BLS key | Validator address recovered from the signature |
| Proof | Compressed aggregate, expanded to EIP-2537 when required | Ascending registry-index `r || s || v` signatures, 65 bytes each |
| Bitmap | Existing minimal big-endian integer bytes | Fixed `ceil(n/8)` byte array, increasing index by byte and bit |
| Membership/PoP | Existing V3 domains and exact signing bytes | Explicit ECDSA domains; membership and join PoP bind the destination registry |
| Direct message | `XITA_DIRECT_MESSAGE_V1` | `XITA_DIRECT_MESSAGE_ECDSA_V1` |

Direct-message fields are encoded and validated once. ECDSA does not build a
BLS payload and replace its prefix. The authorization API signs typed
membership, possession, direct-message and source-fee operations only; it
exposes no sign-arbitrary-bytes entrypoint.

The existing source-fee domain is intentionally shared. The signed source
chain/domain/registry and its trusted configuration select the adapter; the
peer never selects the scheme. No parser failure or unavailable precompile
causes a fallback to another scheme.

Existing BLS payloads, ABI calls, public commands, gossip topic and archive
fields remain compatible. BLS archives retain `aggregateSignature` and
`aggregateSignatureCompressed`. ECDSA archives require
`signatureScheme: "ecdsa"` and `signatures`, and reject mixed BLS fields.
Registry RPC reads retain `blsPublicKey` for BLS and return `ecdsaSigner` for
ECDSA. Raw bitmap bytes are never converted through `big.Int` for ECDSA.

The ECDSA codec retains its explicit 64-member limit. This extraction does not
silently change the existing BLS wire or validator limits. Its existing V5
contract bootstrap bounds and registry replay protection remain explicit
adapter constraints, not duplicated lifecycle engines.

## Verification and activation boundary

Relevant test suites:

```sh
go test ./interchain/... ./consensus/ibft/interchain ./command/ibft/interchain ./jsonrpc
go test -race ./interchain/runtime ./interchain/authorization ./interchain/evm
```

Parity tests include both schemes, nine-validator bitmaps crossing the byte
boundary, canonical signature encoding, duplicate identities/indices, wrong
epochs/domains/registries, candidate consent and eligibility, explicit fee
approval, completed archives and durable replacement after rotation. Existing
BLS golden payload and ABI tests remain in place.

XDC Mainnet's V5 ValidatorRegistry and SourceRegistry are receipt-pinned in
`XDCMainnetCandidate()`, including SourceRegistry creation block 109627114.
The candidate remains outside `compiledChains` pending a reviewed activation
release. The configured HTTPS RPCs passed finalized-header, canonical receipt,
historical log range and block-pinned source fee checks. The XDC-published RPC
URLs are grouped conservatively as one operator for witness selection, with
Ankr as the second operator. These checks use deployment transactions. A
route-level integration test needs a real source Gateway receipt, matching
destination registry state and a completed ECDSA quorum.

The shared worker retains the source transaction lookup hints, source evidence
retry budget, receipt validation, membership poll budget and adaptive scan
catch-up from XGR3.0. The configured adapter selects cryptographic encoding,
key validation and signing for each destination.
