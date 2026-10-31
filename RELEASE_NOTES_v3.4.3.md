# XGR Node v3.4.3 — OP Mainnet Interchain light release

**Status: release candidate, not tagged or deployed.** Intended baseline: `v3.4.2`. No consensus, PoS, genesis or general node changes.

## Added chain

- OP Mainnet: chain ID 10, domain 10, native ETH, EIP-2537 Direct-BLS, finality `ethereum-safe` (no latest fallback).
- Four proposed distinct operators: OP official, PublicNode, dRPC, Tenderly. Verify independent `safe` headers and precompiles before operator rollout.
- Registry receipts have been recorded in `xgr-network/xgr-interchain/main` after on-chain deployment:
  - ValidatorRegistry `0x70f5752326735b31641f21d174ba035e904db93c`, receipt `0x93178b2146f22a362febb59b03fca3df4cf8599e07ffd011b9adcba76918b582`, OP L2 block 158929035.
  - SourceRegistry `0x14d5d33a65a39f96665ed631b04fecf357bcb576`, receipt `0xee77ab2fdde50b697e950c97322ffcf0fea81b9364458e70e049db8a10f896d4`, OP RPC-native L2 block **158929283**.
- These hexadecimal contract addresses are the same as those recorded on BSC, but are separate deployments on different EVM chains. Source activation heights must not be confused.

## Release safeguards

- Pin both contract addresses and **OP RPC-native** source activation block directly in the node's compiled Interchain policy; no zero addresses or runtime ENV overrides.
- Keep XGRChain, Base, Arbitrum and BSC values unchanged.
- Validate chain identity, `safe` finality without confirmation fallback, canonical safe block hash, independently operated RPC witnesses and EIP-2537 precompile responses before enabling signing.
- Run repeated focused `./interchain/... ./consensus/ibft/interchain ./jsonrpc` tests, plus targeted OP policy/negative tests.
- Publish versioned amd64 binary with SHA-256 manifest, commit hash and immutable `v3.4.3` release tag from a reviewed `XGR3.0` commit only.
- No auto rollout: operator updates each validator and verifies quorum, source-observer catch-up, and a small direct + two-hop transfer afterwards.

## Important

Publicly documented on-chain deployment is not the same as a running validator release. Do not start OP validator jobs before release and operator preflight.
