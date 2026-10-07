# XGRChain v3.1.3 — canonical multi-route ILN

v3.1.3 extends the unreleased v3.1.2 ILN design so multiple independent
canonical routes can coexist between the same source and destination chains.

## Route identity

Every ILN route now has an explicit non-zero `bytes32 routeId`.

The canonical route key is:

```text
sourceChainId + sourceDomain + destinationDomain + routeId
```

The node does not assign asset semantics to `routeId`. It may identify an
XGR route, XGA route, USDC route, EURC route, or another future route type.

This permits, for example, all of the following to coexist:

- XGRChain -> Base / XGR
- XGRChain -> Base / XGA
- XGRChain -> Base / xUSDC
- Base -> XGRChain / wXGR
- Base -> XGRChain / USDC

without changing Interchain validator membership semantics.

## Protocol domains

The route ID changes the signed binary format, so the ILN protocol domains are
versioned:

- `XGR_ILN_ROUTE_V2`
- `XGR_ILN_GOVERNANCE_V2`
- `XGR_ILN_CHECKPOINT_V2`

`routeId` is cryptographically bound into route identity, governance
proposals and message-specific checkpoint attestations.

## Source registry ABI

The canonical source ILN registry is now read with:

```solidity
getRoute(uint32 destinationDomain, bytes32 routeId)
```

The returned route continues to define:

- source chain ID and source domain;
- canonical Gateway;
- canonical source router;
- Mailbox;
- MerkleTreeHook;
- canonical destination router;
- native source-chain validator fee;
- enabled state.

## Fee-qualified operation

The canonical Gateway operation event is now route-specific:

```solidity
event ILNOperation(
    bytes32 indexed routeId,
    bytes32 indexed messageId,
    uint32 indexed destinationDomain,
    uint256 validatorFeeWei
);
```

Validators scan only the configured `routeId` and reject zero or mismatched
route IDs.

The operation fee remains the actual native source-chain validator fee emitted
by the canonical Gateway. That value is bound into the signed checkpoint and
re-verified from the exact source operation before peer votes are accepted.

## Validator membership

Interchain validator membership remains destination-chain scoped.

A validator activates for a configured destination such as Base. The same
destination validator set can secure multiple canonical routes to that chain.
Route ID selects the operation being authorized; it does not create a separate
validator set.

## Gossip isolation

Because v3.1.3 ILN payloads are binary-incompatible with the prior unreleased
v3.1.2 format, the Interchain gossip topic is:

```text
/xgr/interchain/3.0.0
```

This prevents mixed-version Interchain workers from exchanging incompatible
votes.

## Route configuration

Explicit route configuration now requires:

```text
XGR_INTERCHAIN_ROUTE_<ROUTE>_SOURCE_NETWORK
XGR_INTERCHAIN_ROUTE_<ROUTE>_DESTINATION
XGR_INTERCHAIN_ROUTE_<ROUTE>_ROUTE_ID
```

The node rejects:

- missing or zero route IDs;
- malformed route IDs;
- duplicate route names;
- two configured route names resolving to the same source, destination and
  route ID;
- legacy route-local source configuration.

Canonical contract addresses and fee state remain on-chain registry truth.

## RPC and persistence

Completed message-specific attestations expose `routeId` and use
`XGR_ILN_CHECKPOINT_V2`.

The read-only RPC remains:

```text
xgr_getILNInterchainAttestation(route, messageId)
```

Completed governance quorums also expose `routeId`.

## Required validation before release

Run:

```bash
go test -count=1 ./consensus/ibft/interchain ./interchain/evm ./interchain/runtime ./command/ibft/interchain ./jsonrpc
go test -count=1 ./e2e -run '^TestInterchainILNRoutesE2E$' -v
go test -run '^$' ./e2e/...
go build ./...
```

The CI workflow also runs the targeted ILN route E2E test explicitly so the
E2E package cannot be skipped by the normal unit-test package filter.

## Scope

v3.1.3 changes the Interchain worker/protocol only. It does not modify IBFT
block validity, EVM execution or ordinary XGRChain consensus.

Smart-contract updates for the multi-route registry, Gateway, destination ISM
and source-chain native-fe settlement are separate deployment artifacts and
must match the v3.1.3 binary encodings exactly.
