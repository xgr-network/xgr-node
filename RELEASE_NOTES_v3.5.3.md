# XGRChain v3.5.3 — Generic receipt-pinned Direct-BLS source verification (light release)

**Scope:** Interchain-only, non-consensus update of the reviewed v3.5.2 Mainnet binary. No hardfork, genesis, IBFT, PoS/staking, BLS key/PoP, validator-set, BLS signature wire format, ECDSA/XDC, on-chain contract or user asset operation changes.

## Changes relative to v3.5.2

- For **known external EVM source transactions**, the ChainObserver and durable retry journal forward the original source transaction hash as an **untrusted locator**, without fetching the historical Gateway event again through `eth_getLogs`.
- On **each independently operated source RPC**, the Direct-BLS source verifier fetches the exact `eth_getTransactionReceipt(txHash)` and independently verifies canonical block identity and the configured chain-specific finality: **BSC 56: finalized; Base 8453, Arbitrum 42161, Optimism 10: safe**.
- Every source witness still verifies the original same-transaction Gateway `ILNOperation`, canonical Mailbox `Dispatch`, registered immutable SourceRegistry/Gateway/Router/Mailbox bindings, FeeVault allocation and validator set epoch. The existing full-evidence, **two-independent-operator** comparison and fail-closed signing path remain mandatory.
- New public RPC **`xgr_requestILNQuorumWithSourceTx(route,messageId,sourceBlock,sourceTxHash)`** admits an untrusted locator without granting signing authority. This hash is carried to validator peer gossip, where each peer repeats the same checks. The existing `xgr_requestILNQuorum(route,messageId,sourceBlock)` and bounded `eth_getLogs` **discovery** remain available for transactions whose hash is not yet known.
- The **native XGRChain** source path still uses local committed IBFT chain state, not an external RPC/indexer.
- Keep exactly the v3.5.2 compiled source-chain RPC URLs, operator labels, addresses, chain/domain rules and signer/custody controls. No release automatically initiates a transfer or modifies the pending roundtrip.

## Release assurance and limitations

The reviewed PR is [xgr-node #97](https://github.com/xgr-network/xgr-node/pull/97). CI includes full Go unit packages, Direct-BLS-focused security regressions, node/IBFT compatibility, and the new **four-chain actual verifier-path** test fixture. The fixture operates two separate simulated RPC providers per EVM chain; it explicitly rejects source receipts requiring `eth_getLogs`, one-provider availability, conflicting canonical blocks, wrong transaction/block, mismatched FeeVault allocation, and missing chain-specific finality. It does **not** claim that public RPC services were live-tested.

**Open operational acceptance gap:** A successful **live historical source transaction replay with two independent complete RPC witnesses on each of Base, Arbitrum, Optimism and BSC has not been verified** from the current execution environment. In particular the pending BSC transfer may remain blocked when public providers reject `eth_getTransactionReceipt`, finalized heads, or FeeVault reads (HTTP 403/429). This release removes an avoidable historical `eth_getLogs` requirement, but never reduces the independent source RPC requirement. No successful delivery, available liquidity, or recovered transfer is implied.

## Immutable light-release gate

Manually run `.github/workflows/release-light-v353.yml` from **XGR3.0** only after PR #97 has been reviewed and merged. Supply the full reviewed `XGR3.0` head SHA as `expected_commit` and `confirmation=RELEASE-v3.5.3`. The workflow:
- Verifies the exact 40-digit immutable commit, existing v3.5.2 release ancestry, absence of an existing v3.5.3 tag, and an allowlist of only reviewed Interchain RPC/signing/journal/tests, docs and the new release workflow.
- Rejects all chain-policy, consensus, VM, state, genesis, staking, networking, cryptography and unrelated code changes.
- Re-runs full Interchain/Direct-BLS regressions, the complete independent RPC negative matrix and public-hint/cursor tests.
- Builds `xgrchain-v3.5.3-linux-amd64` with embedded version + source commit; publishes `version.txt`, `commit.txt`, `toolchain.txt`, `sha256sums.txt` and the immutable GitHub release assets. **No automatic deployment.**

## Controlled validator rollout — manual operator actions only

1. Compare the release's tag, `commit.txt`, embedded `version` output and downloaded `sha256sums.txt`. Retain the previously verified **v3.5.2** binary. Back up binaries only; never discard existing chain databases, ILN cursors, durable votes, retry queues, BLS keys or BSC source transaction records.
2. Upgrade **one validator at a time**, preserving IBFT and validator availability. No fork, state migration, new PoP, new set registration or smart-contract transaction is necessary.
3. Verify sync status, version, validator membership and Direct-BLS diagnostics. On each active external chain independently inspect a real canonical source receipt, safe/finalized head, Registry, Gateway/Dispatch and FeeVault from at least two distinct provider operators. **Do not authorize signing** when fewer than two complete matching sources are available.
4. Only after upgraded XGR RPC endpoints expose `xgr_requestILNQuorumWithSourceTx`, independently review and deploy the matching [xgr-interchain #440](https://github.com/xgr-network/xgr-interchain/pull/440) recovery update. Until then the old on-demand RPC method remains in the node; do not deploy the new recovery UI/API ahead of the new node.
5. The existing original BSC source transaction **0x6e6a7c3ff28cb9fce8db40230891f3e34dd756568167b0943f6e717c5c277dd7**, source block **134450134**, message **0x83ec6a35146a7dbab41f8472dc3262320a8d1003462e260e891ade4db69e534d** and its 53,703,582 XGR must **never** be resent, relocked or cancelled merely because a new node binary is published. Confirm destination delivery on-chain before considering it complete.

**Rollback:** If a provider, finality or validator regression is observed, stop the rolling upgrade and restore the previous verified binary without altering on-disk state or independent witness thresholds. Retain the source transfer and its original durable records.
