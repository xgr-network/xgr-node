# XGRChain v3.1.2 — ILN Interchain security cutover

v3.1.2 is a breaking Interchain security release. It removes the generic v3.1.1 checkpoint signer and replaces it with message-specific, fee-qualified ILN authorization.

## Breaking Interchain changes

- Interchain gossip topic: `/xgr/interchain/2.0.0`.
- Legacy `checkpoint_vote` messages are rejected.
- The v3.1.1 generic checkpoint signer is removed from the runtime.
- Legacy origin Mailbox / MerkleTreeHook ENV configuration is removed.
- There is no implicit checkpoint-route fallback.
- v3.1.2 routes are explicit:
  - `XGR_INTERCHAIN_ROUTE_<ROUTE>_SOURCE_NETWORK`
  - `XGR_INTERCHAIN_ROUTE_<ROUTE>_DESTINATION`
- Canonical route contracts and fee state are read from the source network ILN registry.
- Registry bootstrap is network-scoped:
  - `XGR_INTERCHAIN_<NETWORK>_ILN_REGISTRY_ADDR`.

With no explicit ILN routes configured, Interchain checkpoint signing is idle and ordinary XGRChain consensus/EVM operation is unaffected.

## Canonical source route

The v3.1.2 node expects the source-chain ILN registry to expose:

```solidity
getRoute(uint32 destinationDomain)
```

returning:

- source chain ID,
- source domain,
- canonical ILNGateway,
- canonical source Warp router,
- canonical Mailbox,
- canonical MerkleTreeHook,
- canonical destination Warp router,
- configured native validator fee,
- enabled state.

The source Warp router remains the actual Hyperlane message sender.

## Fee-qualified operation

The canonical ILNGateway must atomically:

1. read the canonical route;
2. require / escrow the route's native validator fee;
3. invoke the canonical source Warp router;
4. receive the returned Hyperlane `messageId`;
5. emit, only after successful dispatch:

```solidity
event ILNOperation(
    bytes32 indexed messageId,
    uint32 indexed destinationDomain,
    uint256 validatorFeeWei
);
```

A reverted bridge dispatch therefore also reverts fee escrow and the authorization event.

The Gateway must expose:

```solidity
ilnRegistry()
warpRouter()
mailbox()
merkleTreeHook()
activationBlock()
```

The validator verifies that these getters match the source registry route.

## Message-specific BLS authorization

Validators scan only confirmed `ILNOperation` events from the canonical Gateway.

For each operation, the signed `XGR_ILN_CHECKPOINT_V1` payload binds:

- source chain ID and domain,
- destination domain,
- destination Interchain validator set ID,
- exact confirmed source block,
- source ILN registry,
- ILNGateway,
- source Warp router,
- Mailbox,
- MerkleTreeHook,
- destination Warp router,
- actual escrowed native validator fee from the Gateway operation,
- authorized Hyperlane `messageId`,
- checkpoint root and index.

Incoming peer votes are independently re-verified against the exact source block and the exact canonical Gateway operation before their BLS signature is accepted.

This closes the v3.1.1 generic-root problem: a direct call to the Warp router can enter the normal Hyperlane Merkle tree, but it does not produce the canonical Gateway `ILNOperation` record and therefore cannot obtain an ILN quorum for its message ID.

## Same-block governance safety

The operation's fee is taken from the atomic Gateway operation rather than blindly comparing against the registry's end-of-block fee value. This preserves a valid operation when a quorum-governed fee update executes later in the same source block.

Route addresses are still re-read from the exact source block and must match the signed payload.

## Persistent operation scanning

Per route, the validator persists:

- the confirmed source-block scan cursor;
- locally signed ILN votes that have not yet reached quorum.

The initial cursor is derived from `ILNGateway.activationBlock()`. Event scans are bounded to 1,000 blocks per pass. Pending local votes are recovered and rebroadcast after restart. If the destination validator set changes before quorum, the same confirmed message operation is re-verified and re-signed against the new set ID instead of being stranded.

Completed attestations are stored per authorized Hyperlane message ID.

Read-only RPC:

```text
xgr_getILNInterchainAttestation(route, messageId)
```

returns the message-specific attestation.

## Governance

ILN governance uses the same unweighted two-thirds Interchain BLS quorum.

Proposal types:

- `FEE_UPDATE`
- `ROUTE_ADD`
- `ROUTE_ENABLE`
- `ROUTE_DISABLE`

A proposal binds the exact source ILN registry address, validator set ID, nonce and expiry.

Proposal creation never approves automatically.

CLI:

```text
xgrchain ibft interchain proposal create ...
xgrchain ibft interchain proposal show --proposal-id 0x...
xgrchain ibft interchain proposal approve --proposal-id 0x...
```

Completed governance quorums are exposed read-only through:

```text
xgr_getILNGovernanceQuorum(proposalId)
```

An eventual on-chain `execute(...)` transaction is separate and permissionless. The executor supplies the quorum proof and pays gas on the affected source chain.

## Destination-side contract requirement

The v3.1.2 node can be rolled out before ILN contracts are activated.

Before an ILN route is activated, its destination security module must verify at minimum:

- the `XGR_ILN_CHECKPOINT_V1` BLS quorum;
- the current/historical validator set referenced by `setId`;
- the Merkle proof for the actual Hyperlane message;
- actual Hyperlane `messageId == authorizedMessageId`;
- expected source Warp router;
- expected destination Warp router.

This prevents a signed root from authorizing any other leaf in that root.

## Rollout order

1. Build and test v3.1.2.
2. Upgrade all XGR Interchain validators to the v3.1.2 binary.
3. Verify chain consensus and ordinary EVM operation.
4. Deploy ILN registry / Gateway / fee settlement and destination verification contracts.
5. Configure network-scoped `ILN_REGISTRY_ADDR` values and explicit ILN routes.
6. Activate routes only after contract and E2E validation.

Do not mix v3.1.1 and v3.1.2 Interchain validators. The P2P topic change intentionally isolates them.

## Required validation before production rollout

Run:

```bash
pkgs=$(go list ./... | grep -vE '(/e2e($|/)|/e2e-polybft($|/)|/tests($|/)|/tracker($|/)|/command/rootchain/deploy($|/)|/consensus/polybft($|/)|/state/runtime/evm($|/))')
go test -count=1 -short $pkgs
go build ./...
```

The GitHub-connected editing session that produced this patch does not itself execute the repository build. A green build/test run is mandatory before validator rollout.
