# Candidate: XGRChain v3.5.2 BSC RPC witness availability (NO RELEASE YET)

**Status:** review candidate only. Do not tag, merge, deploy or publish a node executable without source-witness preflight and passing Go CI.

## Scope

- The compiled BNB Smart Chain (BSC, chain 56) source-RPC policy gains the existing keyless `https://public.1rpc.io/bnb` endpoint as an additional operator, ordered behind PublicNode and ahead of dRPC/OnFinality.
- Operator IDs remain aligned exactly with URL positions: `publicnode`, `onerpc`, `drpc`, `onfinality`.
- Existing receipts, source activation height, validator-registry addresses, BLS payloads, finality `finalized`, RPC cooldown, 2-independent-operator quorum and transaction recovery logic remain unchanged.
- Other external chains and XGR local IBFT source verification remain unchanged.
- This change is **binary node policy**, not an admin UI, token-catalog, contract, staking, consensus, genesis or fork change. Validators only see it after a controlled binary rollout.

## Incident evidence, 2026-11-16

BSC confirmed source TX: `0x64b73c6ea882ec6f5e73f9f405845b572a7d58bce8aa826937d8fd0e3e535121`

Message: `0xb9a701628bfc3176d70cfc1583c331937b67aff26694c5c707945073dcea62ef`

BSC source block: `133969943`; block hash `0xc7b66f38ef0627477558ed4610dbf180ab6c88516876cfd5fbb8531ee61f3d38`.

The independent read-only operator probe observed matching canonical source block hashes and a matching Gateway event/log from **PublicNode + 1RPC**. dRPC and OnFinality returned HTTP 429; NodeReal `eth_getLogs` returned `limit exceeded`; BlastAPI returned HTTP 429.

**This preliminary transport/receipt/log check does NOT establish** complete source FeeVault allocation, canonical SourceRegistry binding, correct Dispatch event, final signed BLS quorum, or successful destination `Mailbox.process`. The source request remains pending until the actual validator worker authorizes it.

**Operator-independence caveat:** 1RPC is a distinct public service/operator but may aggregate third-party upstream RPC nodes. Its upstream infrastructure has not been independently audited. A one-time success cannot establish ongoing `eth_getLogs` availability or a provider SLA.

## Extended keyless RPC candidate results (2026-11-16)

The actual original confirmed BSC transaction was queried sequentially against seven additional operators via `npm run probe:extended`, with the following results. **None passed** all transport/receipt/finality/Gateway-log checks:

| Operator | Observed failure | Decision |
| --- | --- | --- |
| bloXroute | `eth_getLogs` HTTP 403 | Excluded |
| Nodies/POKT | HTTP 403 (chain ID unavailable) | Excluded |
| MeowRPC | HTTP 429 | Excluded |
| Ankr keyless | API-key authorization required | Excluded |
| LlamaNodes | Fetch/network failure | Excluded |
| OMNIA public | HTTP 521 | Excluded |
| Blockmachine public | Historical RPC window restricted to most recent 100 blocks; original block 133969943 inaccessible without key | Excluded |

PublicNode passed and matched the previously tested 1RPC operator's historical source block/receipt/log data. This does **not** certify 1RPC's FeeVault/Registry/Dispatch evidence, continuous availability, or independent upstream node ownership. Public 1RPC routes requests randomly among upstream providers, so operator branding is not a proof of underlying infrastructure separation. Separate external monitoring has shown inconsistent keyless 1RPC `eth_getLogs` behavior; the user's successful one-block probe is not a long-term SLA.

**Candidate disposition:** retain only `1RPC` as the fourth *proposed* endpoint; keep dRPC and OnFinality as degraded-but-potentially-recovering fallbacks. Do not add any of the seven failed endpoints or paid/API-key URLs. An operator run of complete Direct-BLS evidence verification remains a gating requirement.

## Required pre-merge / pre-release gates

1. Run the separate read-only `xita-roundtrip-worker` `npm run probe:extended` tool from its confirmed original transfer journal. Review additional independent keyless public candidate RPC endpoints. Add **only** sources that pass finalized receipt + historical Gateway `eth_getLogs` on the real source block and are genuinely separate operators. Mark rate-limited providers as unavailable.
2. Repeat candidate source-witness checks at different times; explicitly observe provider staleness, rate-limit responses and failures. Do not count URLs from one operator twice.
3. Run `go test ./interchain/... ./jsonrpc ./consensus/ibft/interchain` with PR CI, including existing witness-consensus and BSC source-binding tests. Fail closed on missing proof, wrong hash, wrong registry or insufficient independent operators.
4. Before production adoption, test actual XGR-node Direct-BLS verification of the historical source **including FeeVault/SourceRegistry and destination validator set**, in controlled staging or a read-only diagnostic that cannot sign. A green `eth_chainId`, `finalized`, or Gateway receipt alone is insufficient.
5. Publish a **separate guarded light release** only after this PR's exact source and CI evidence are reviewed. Do not reuse the v3.5.1 F-20 scope-limited release workflow; its allowlist deliberately excludes this RPC policy change.
6. Roll out one validator at a time preserving validator data, attestations and BLS vote journals. Verify genesis/fork settings unchanged, sync, IBFT consensus, route worker and eventual destination delivery. Roll back the executable if validation regresses, without deleting chain state or rebroadcasting the original BSC source TX.

## Scope and economics

No paid RPCs or API keys. Public endpoints can rate-limit unpredictably. This mitigation improves redundancy but does not guarantee recovery or defeat a shared-upstream/malicious-provider failure.

Original 100 wXGR pending transfer must **not** be locked or burned a second time. A new node build does not itself unlock it; it merely permits validators to attempt the existing source evidence verification under the release-reviewed operator policy.
