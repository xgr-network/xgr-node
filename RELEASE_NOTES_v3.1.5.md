# XGRChain / XITA v3.1.5 — permissionless routing, source-native fees

Status: **node-protocol development** on PoS_3. Not a deployable bridge release until matching contracts and full E2E tests pass.

## One identity per asset and directed chain pair

Canonical asset ID:

    keccak256(abi.encode(keccak256("XITA_ASSET_V315"), uint64(originChainID), address(originToken), uint8(tokenKind)))

Native coin uses zero address and token kind 0; ERC20 uses canonical address and kind 1.

Directed route ID:

    keccak256(abi.encode(keccak256("XITA_ROUTE_V315"), assetID, uint64(sourceChainID), uint32(sourceDomain), uint64(destinationChainID), uint32(destinationDomain)))

One endpoint must be XGRChain domain 1643. Reverse direction is a different directed ID. Only one XITA canonical representation per asset and physical chain, maintained by standard permissionless Factory deployments.

Neither token contract, source/destination router, gateway nor validator fee belongs in the ID. Those addresses are bound immutably once during registration; the destination router address is still needed for Hyperlane delivery. Source registry MUST verify the standard Factory origin and counterpart binding. Arbitrary first-claim remote routers are forbidden, without introducing a privileged route operator.

## Authority

- ROUTE_ADD/ENABLE/DISABLE and their quorum payloads are removed from the XGRChain v3.1.5 protocol and CLI.
- Route registration is append-only and does not need validator approval. The Factory determines asset identity and router addresses; GitHub verified listing is unrelated to consensus permissions.
- Validator membership and BLS transfer quorums are unchanged.
- Fee governance alone uses a current local Interchain validator set BLS quorum (two-thirds). One positive native-source-chain amount applies across ALL assets and destinations on that chain.
- Message domain XITA_SOURCE_FEE_V315; packed bytes: domain || uint64(sourceChainID) || uint32(sourceDomain) || address(sourceRegistry) || uint64(setID) || uint64(nonce) || uint64(validUntil) || uint256(feeWei).
- Contract must expose sourceFeeNonce(), use replay protection, reject expired or obsolete validator sets, and preserve block-historical getRoute() fee values for checkpoint signatures.
- Fee values are deliberately NOT set in this patch. They will be decided separately.

## CLI

    xgrchain ibft interchain fee create --source base --fee-wei <native-smallest-unit> --data-dir <node-data-dir>
    xgrchain ibft interchain fee approve --proposal-id <proposal-hash> --data-dir <node-data-dir>

Proposal reception is NOT implicit consent or a BLS signature; approval remains an explicit validator action. Quorum output stays under the existing local governance-quorums directory so operational scripts can discover it.

## Bridge safety preserved

Historical validator checkpoint wire format remains XGR_ILN_CHECKPOINT_V2 while migrating; the source route snapshot and ILNOperation event still bind exact gateway, source router, destination router, fee and Hyperlane message ID to the confirmed block.

## Release blockers

Contracts must be updated together: ILN Registry, Factory, Gateway, Fee Vault and guarded routers; secure XGR and arbitrary ERC20 mint/burn/collateral flows, unique representations, counterparty binding, native per-source fee and source-block history. Do not deploy or release node v3.1.5 until protocol/contracts E2E, recovery of pending messages and backwards-compatibility boundaries are tested.
