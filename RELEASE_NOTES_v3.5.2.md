# XGRChain v3.5.2 — BSC Direct-BLS source RPC redundancy light release

**Scope:** Non-consensus Interchain source RPC availability patch on the existing v3.5.1 Mainnet baseline. No fork, genesis, validator-set rotation, staking changes, BLS wire-format change, on-chain contract change, or custody transfer.

**Exact source:** This release's GitHub tag, `commit.txt` and SHA-256 manifest must all identify the same reviewed `XGR3.0` commit. Do not deploy builds taken from unreviewed source or stale CI artifacts.

## Changes from v3.5.1

- Add the keyless public BSC RPC endpoint `https://public.1rpc.io/bnb` as a *fourth* compiled provider for source chain/domain 56.
- Preserve existing PublicNode, dRPC and OnFinality endpoints; prioritize PublicNode, then 1RPC, then dRPC and OnFinality.
- Keep the parallel operator IDs `publicnode`, `onerpc`, `drpc`, `onfinality`. Endpoints of the same operator cannot count as separate evidence witnesses.
- Preserve strict `finalized` source head, the **two independently operated source RPC witness requirement**, complete canonical Gateway/Dispatch/SourceRegistry/FeeVault checks, destination ISM and validator set, BLS signature rules, and all retry/fail-closed behavior.
- No change to the other four compiled chain policies or to the locally verified XGR IBFT chain. No outbound transfer, signing or validator runtime change is triggered by publishing release assets.

## Incident motivation and evidence

Original BSC transaction: `0x64b73c6ea882ec6f5e73f9f405845b572a7d58bce8aa826937d8fd0e3e535121`, block `133969943`, message `0xb9a701628bfc3176d70cfc1583c331937b67aff26694c5c707945073dcea62ef`.

A read-only real-source preflight observed matching finalized block hash `0xc7b66f38ef0627477558ed4610dbf180ab6c88516876cfd5fbb8531ee61f3d38`, transaction receipt, and Gateway logs from PublicNode and keyless 1RPC. Meanwhile dRPC and OnFinality returned HTTP 429 on the historical source checks. Seven additional keyless public endpoints were tested and all failed at least one required preliminary check.

**Important limits:** This preflight is **not** a completed validator Direct-BLS proof. In particular it does not establish complete FeeVault/Registry/Dispatch checks, a BLS quorum, a destination `Mailbox.process` receipt, an availability guarantee, or independence of 1RPC's *underlying upstream* infrastructure. 1RPC acts as a provider/aggregator and public sources can fail or rate-limit again. A successful GitHub binary release does not prove a real transfer has been delivered. No paid RPC plan or credential is introduced.

## CI validation and release guard

Use the dedicated manually dispatched `.github/workflows/release-light-v352.yml` on `XGR3.0`. The workflow requires:
- An exact 40-character reviewed `XGR3.0` commit and explicit `RELEASE-v3.5.2` confirmation.
- `v3.5.1` ancestry, no pre-existing `v3.5.2` tag and a strict path allowlist comparing source against `v3.5.1`.
- Exact four-entry BSC URL/operator ordering, unchanged contract registry bindings, original chain finality, no external RPC in native XGR mode and no changes under consensus, VM, PoS, chain database or transaction processing directories.
- Focused repeatable unit regressions for BSC policy and independent RPC witness/failover, plus existing Direct-BLS and destination-set compatibility tests.
- Build `xgrchain-v3.5.2-linux-amd64` with embedded version and exact commit and publish `version.txt`, `commit.txt`, `toolchain.txt`, `sha256sums.txt`, and immutable GitHub release assets.

## Deployment — operator action AFTER publishing

1. Verify release tag, version, commit SHA, downloaded binary SHA-256 and unchanged network/genesis parameters. Back up the current verified **v3.5.1 binary** while retaining on-disk chain data, interchain journals, votes and attestations.
2. Deploy **one validator at a time** according to the existing IBFT quorum/availability procedure. Do not change consensus, PoS, BLS keys, bootstrap, fee allocations, or deployed contracts.
3. Verify healthy IBFT sync, source-chain finality, the exact original BSC source evidence including Registry/FeeVault and destination validator set, Direct-BLS quorum and the destination delivery transaction. Never infer delivery from a green RPC or cursor.
4. If independent source verification still fails, collect *specific provider and stage errors*, retain the original pending transfer and restore the previously verified binary as needed. **Do not resend the original BSC transfer, delete local attestation journals or disable two-provider verification.**

**Open acceptance gap:** Full historical independent witness checks by the production XGR validator, especially SourceRegistry/FeeVault/Dispatch, have not yet been demonstrated for 1RPC in the incident. The release is a fail-closed availability mitigation, not a verified incident closure. Operators must treat the original 100 wXGR source transfer as pending until destination-chain delivery is actually observed.
