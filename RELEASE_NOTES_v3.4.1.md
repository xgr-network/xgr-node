# xgr-node v3.4.1 — BSC configuration preparation

**Status:** Preparatory branch, not a tagged or deployed v3.4.1 release.

## Objective

Prepare BNB Smart Chain (BSC Mainnet) as a future XITA Direct-BLS
source/destination without modifying the working XGRChain, Base and Arbitrum
deployments or the v3.4.0 consensus/accounting activation.

## Immutable network policy

- Name: `bsc`.
- Chain ID / XITA domain: `56 / 56`.
- Native currency: BNB.
- EIP-2537 verifier format: `eip2537`, enabled on BSC by Pascal/BEP-439.
- Finality rule: `ethereum-finalized`:
  `eth_getBlockByNumber("finalized", false)`; numerically re-read the block
  and require identical canonical block hash. All source receipts and
  observations remain bounded by the finalized head. No fallback to
  `safe`, `latest` or fixed confirmation depth.
- Contract placeholders:
  - `Registry: 0x0000000000000000000000000000000000000000`
  - `SourceRegistry: 0x0000000000000000000000000000000000000000`
  - `SourceActivationBlock: 0`

**These are deliberately NOT deployed addresses.** The code explicitly treats
zero-address registry placeholders as unbound. `Policy("bsc")` and
`evm.Load("bsc")` can describe BSC, but `ActivePolicies()` and
`evm.LoadAll()` continue to include only the three deployed chains.
Membership reads/submissions and ILN source registry reads must reject
the zero addresses. No validator signing or route admission is enabled.

## RPC candidates (NOT production-certified)

- PublicNode: `https://bsc-rpc.publicnode.com`
- dRPC: `https://bsc.drpc.org`
- OnFinality: `https://bnb.api.onfinality.io/public`

These three independently branded services are candidates only. Validate
their actual operator independence, chain ID 56, `finalized` and numbered
canonical block queries, historical `eth_getLogs` and receipts, BLS verifier
`eth_call`, rate limits, and recovery/backfill behavior before activation.
Do not use the official BSC dataseed public RPCs as event-log witnesses:
BNB Chain documents that `eth_getLogs` is disabled on those endpoints.

## Fast finality

BSC Fast Finality (BEP-126 and subsequent improvements) requires
supermajority validator voting. BNB Chain reported typical finality of
approximately 0.65 seconds in June/August 2026. This is an operational
expectation, **not** a contractual timer, consensus proof supplied to XITA,
or a replacement for the finalized RPC tag. If the fast-finality quorum
is unavailable, new blocks can exist without new finalized blocks; XITA
must wait, not downgrade finality.

## Deployment and subsequent activation

After the operator deploys and verifies the BSC contract suite in the
xgr-interchain deployment inventory:

1. Record transaction hashes, verified code hashes, deployed
   ValidatorRegistry and SourceRegistry addresses and finalized receipt
   heights. The SourceRegistry activation height must be the RPC-native
   BSC deployment block, not an estimate.
2. Replace both zero-address placeholders and activation height in
   `interchain/config.go` on this branch, with independent receipt evidence.
3. Verify BLS verifier operation, registration/membership and independent
   witness quorum. Confirm runtime service limits and `eth_getLogs`.
4. Run Go unit/regression tests and complete CI; prove XGR↔BSC transfers,
   safe failure modes, restart/recovery and source-history catch-up.
5. Only after these checks, merge and run the established versioned node
   release and controlled validator rollout. Do not represent the
   preparatory branch as a deployed BSC integration.

## Regression tests

Focused tests check BSC identity, zero-address gating, unchanged three
active chains, immutable `finalized`-only policy, rejection of an
unfinalized receipt, unsupported finalized tags, canonical hash mismatch,
and rejection of incomplete receipt bindings.

## Primary references

- [BNB Chain BEP-126](https://github.com/bnb-chain/BEPs/blob/master/BEPs/BEP126.md)
- [BNB Chain BSC Finality API](https://docs.bnbchain.org/bnb-smart-chain/developers/json_rpc/bsc-api-list/)
- [BSC Pascal / BEP-439](https://docs.bnbchain.org/announce/pascal-bsc/)
- [BSC public RPC limitations](https://docs.bnbchain.org/bnb-smart-chain/developers/json_rpc/json-rpc-endpoint/)
