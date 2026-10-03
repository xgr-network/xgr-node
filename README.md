# xgr-node

`xgr-node` is the public Go implementation of XGRChain, the EVM-compatible Layer-1 blockchain of the XGR Network.

The node provides:

- Ethereum-compatible EVM execution,
- IBFT deterministic finality,
- delegated Proof-of-Stake validator participation,
- stake- and uptime-weighted voting power,
- validator self-staking and delegation,
- epoch-based validator lifecycle and reward accounting,
- XGR-specific gas and fee behavior,
- Ethereum-compatible JSON-RPC,
- XGR-specific PoS and operator RPC methods,
- libp2p networking,
- native XGR protocol precompiles,
- Online State Trie Sweeper / State Growth Control,
- genesis and chain-configuration tooling.

Current public release:

    v3.1.1

Release commit:

    1a4844b311fb856cb8c2303a40fa8aa69b560544

---

## XGRChain mainnet

| Parameter | Value |
| --- | --- |
| Network | `xgrchain` |
| Chain ID | `1643` |
| Chain ID hex | `0x66b` |
| Native asset | XGR |
| Native decimals | `18` |
| Execution | EVM-compatible |
| Finality | IBFT |
| Current validator model | Delegated PoS |
| PoS activation | block `5,446,500` |
| PoS activation hex | `0x531b64` |
| Target block time | approximately 2 seconds |
| Minimum validators | `4` |
| Maximum validators | `25` |
| Micro epoch | `25` blocks |
| Macro epoch | `1000` blocks |
| Default P2P port | `1478` |
| Default JSON-RPC port | `8545` |
| Default gRPC port | `9632` |

Canonical mainnet genesis:

https://github.com/xgr-network/XGR/blob/main/genesis/mainnet/genesis.json

Public technical documentation:

https://github.com/xgr-network/XGR/tree/main/docs/chain

---

# Architecture

## EVM execution

XGRChain uses an Ethereum-compatible execution model.

Supported public transaction types include:

| Type | Code |
| --- | ---: |
| Legacy | `0x00` |
| Access List | `0x01` |
| Dynamic Fee | `0x02` |

The node also defines an internal protocol transaction type:

    StateTx = 0x7f

`StateTx` is used by internal deterministic protocol execution and is not a normal wallet transaction type.

Standard Ethereum tooling can interact with XGRChain through Ethereum-compatible JSON-RPC.

---

## IBFT finality

XGRChain uses IBFT for deterministic block finality.

The published mainnet consensus schedule is:

| Phase | Validator model | Blocks |
| --- | --- | --- |
| Initial | PoA / BLS | `0–5,446,499` |
| Current | Delegated PoS / BLS | `5,446,500+` |

IBFT remains the finality protocol after the transition to delegated PoS.

PoS determines:

- validator eligibility,
- validator-set evolution,
- staking and delegation,
- consensus voting power,
- epoch accounting,
- rewards and slashing.

---

# Delegated Proof of Stake

## Validator participation

Normal validator eligibility is governed by protocol-defined staking rules.

Current mainnet parameters include:

| Parameter | Value |
| --- | ---: |
| Minimum validator self stake | `200,000 XGR` |
| Normal total-support threshold | `2,000,000 XGR` |
| Default delegator minimum | `10,000 XGR` |
| Maximum delegators per validator | `200` |
| Maximum active validators | `25` |
| Minimum validator target | `4` |

The minimum self stake and total-support threshold are different concepts.

A validator can have a valid staking position without yet satisfying normal consensus eligibility.

---

## Stake-weighted voting power

After the PoS transition boundary has matured into a PoS-parent context, IBFT voting power is based on validator voting stake and uptime weighting.

Conceptually:

    effectiveVotingPower =
        votingStake
        × effectiveUptimeWeight
        ÷ nominalUptimeWeight

Consensus quorum is:

    ceil(2 × totalVotingPower / 3)

Validator count alone therefore does not determine PoS quorum.

The first PoS block at `5,446,500` uses the deterministic transition/unit-voting path because its parent is still PoA.

Stake-weighted voting begins from the subsequent PoS-parent context.

---

## Epochs

Current mainnet configuration:

    microEpochSize          = 25
    macroEpochMicroFactor   = 40

Therefore:

    macro epoch = 25 × 40 = 1000 blocks

Epoch processing governs deterministic behavior including:

- validator-set lifecycle,
- effective stake,
- proposer-duty accounting,
- reward distribution,
- slashing evaluation.

Detailed staking behavior:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Staking_PoS_Model.md

---

# Gas and fees

XGRChain uses Ethereum-compatible transaction fee fields with XGR-specific fee policy and accounting.

Current implementation parameters include:

| Parameter | Value |
| --- | ---: |
| Static fallback minimum base fee | `100 gwei` |
| Critical utilization threshold | `80%` |
| Emergency base-fee denominator | `4` |

At or below the critical utilization threshold, the calculated next base fee returns to the effective configured minimum.

Above the threshold, an emergency congestion ramp applies.

XGRChain also implements protocol-specific fee accounting including:

- fixed burn component,
- donation component,
- validator component,
- PoS immediate validator allocation,
- pooled validator rewards through the FeePool.

Detailed fee semantics:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XRC-GAS_Gas_Price_Behavior.md

---

# Native protocol primitives

## Interchain BLS verification

`v3.1.1` includes a native BLS12-381 Interchain verification precompile at:

    0x0000000000000000000000000000000000002040

The precompile verifies XGR Interchain quorum attestations at the execution layer.

It is a chain-level cryptographic primitive.

It does not itself define:

- a bridge route,
- router deployment,
- relayer availability,
- Interchain validator membership,
- destination-chain security policy.

Interchain infrastructure is maintained separately in:

https://github.com/xgr-network/xgr-hyperlane

---

# State Growth Control

## Online State Trie Sweeper

`xgr-node` includes an online state-trie garbage collector called the Online State Trie Sweeper.

The feature was introduced in `v2.1.0` and remains part of `v3.1.1`.

It can reclaim unreachable historical:

- EVM trie nodes,
- contract-code data,

while the node remains online.

The Trie Sweeper is a **node-local storage policy**.

It does not modify:

- canonical state roots,
- block validity,
- transaction execution,
- consensus,
- staking,
- validator selection,
- receipts,
- logs.

---

## Trie Sweeper configuration

Runtime flags:

    --trie-sweeper
    --trie-sweeper-retain-blocks
    --trie-sweeper-interval

Defaults:

| Setting | Default |
| --- | ---: |
| Sweeper enabled | `false` |
| Retention when enabled | `10,000` blocks |
| Sweep interval | `6h` |

The default `10,000`-block value is a retained canonical-root window, not a promise that every database object older than exactly 10,000 blocks is deleted.

Archive-style nodes requiring unrestricted historical state should leave the Trie Sweeper disabled.

Detailed storage behavior:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_State_Storage_and_Retention.md

---

# Public standalone build

`xgr-node` is designed to build and operate as a standalone public XGRChain node.

Normal chain operation does not require:

- a private XDaLa repository,
- a private XGR process engine,
- proprietary application services.

The Go module contains a repository-local `xgrEngine` stub for the public standalone build path.

Private embedded-engine builds can replace that stub in their own development environment, but the public build must remain functional without private repositories.

---

# Current release

## `v3.1.1`

Release:

https://github.com/xgr-network/xgr-node/releases/tag/v3.1.1

Release commit:

    1a4844b311fb856cb8c2303a40fa8aa69b560544

Published Linux AMD64 binary:

    xgrchain-v3.1.1-linux-amd64

SHA-256:

    429d18db37e9cdb6eb82b33583880c407701fcf070f1d8f6398ca8c4c7a0c88d

The release also publishes:

- `sha256sums.txt`
- `version.txt`

---

# Install release binary

Example for Linux AMD64:

    curl -fL \
      https://github.com/xgr-network/xgr-node/releases/download/v3.1.1/xgrchain-v3.1.1-linux-amd64 \
      -o xgrchain

Verify:

    echo \
    "429d18db37e9cdb6eb82b33583880c407701fcf070f1d8f6398ca8c4c7a0c88d  xgrchain" \
    | sha256sum -c -

Expected:

    xgrchain: OK

Make executable:

    chmod +x xgrchain

Check version:

    ./xgrchain version

---

# Build from source

## Requirements

The current release declares:

    go 1.23.4
    toolchain go1.23.11

A normal Linux build environment should include:

- Git,
- Go compatible with the declared toolchain,
- Make for repository build targets.

---

## Clone the release

    git clone https://github.com/xgr-network/xgr-node.git
    cd xgr-node

    git fetch --all --tags
    git checkout v3.1.1

Verify:

    git describe --tags --exact-match
    git rev-parse HEAD

Expected:

    v3.1.1

and:

    1a4844b311fb856cb8c2303a40fa8aa69b560544

---

## Versioned build

The repository build target is located in:

    scripts/Makefile

Build using:

    make -f scripts/Makefile build

This embeds:

- release version,
- commit,
- branch,
- build time.

Output:

    ./xgrchain

Check:

    ./xgrchain version

There is currently no root-level `Makefile` defining this target.

Do not document:

    make build

as the repository build command unless a root Makefile is added in a future release.

---

## Full Go compilation check

For development and review:

    go build ./...

Consensus-sensitive changes should additionally run the relevant tests.

The repository Makefile provides targets such as:

    make -f scripts/Makefile test
    make -f scripts/Makefile test-e2e
    make -f scripts/Makefile lint

Tool-specific prerequisites apply to the corresponding targets.

---

# Mainnet genesis

Canonical mainnet chain configuration:

https://github.com/xgr-network/XGR/blob/main/genesis/mainnet/genesis.json

Example installation:

    curl -fsSL \
      https://raw.githubusercontent.com/xgr-network/XGR/main/genesis/mainnet/genesis.json \
      -o genesis.json

Do not modify network-defining mainnet fields such as:

- chain ID,
- fork schedule,
- IBFT configuration,
- PoS activation,
- initial allocation,
- protocol addresses.

A node using incompatible network-defining configuration is not participating in the same XGRChain network.

---

# Run a full node

Example:

    ./xgrchain server \
      --chain ./genesis.json \
      --data-dir ./data \
      --libp2p 0.0.0.0:1478 \
      --nat <PUBLIC_IP> \
      --jsonrpc 127.0.0.1:8545 \
      --grpc-address 127.0.0.1:9632 \
      --seal=false

For non-validator infrastructure, make:

    --seal=false

explicit.

The default P2P bind is localhost, so a publicly reachable node should configure its P2P bind and advertised address deliberately.

---

# Run with bounded historical state

Example:

    ./xgrchain server \
      --chain ./genesis.json \
      --data-dir ./data \
      --libp2p 0.0.0.0:1478 \
      --nat <PUBLIC_IP> \
      --jsonrpc 127.0.0.1:8545 \
      --grpc-address 127.0.0.1:9632 \
      --seal=false \
      --trie-sweeper \
      --trie-sweeper-retain-blocks 10000 \
      --trie-sweeper-interval 6h

At the nominal two-second block target:

    10,000 blocks ≈ 5 hours 33 minutes

This wall-clock estimate varies with actual block production.

---

# JSON-RPC

XGRChain exposes Ethereum-compatible JSON-RPC together with XGR-specific extensions.

Registered namespaces include:

    eth_*
    net_*
    web3_*
    txpool_*
    bridge_*
    xgr_*
    debug_*

Not every namespace should be exposed unrestricted to public clients.

---

## PoS monitoring RPC

Current XGR-specific PoS monitoring methods include:

    eth_getPosValidatorsOverview
    eth_getPosValidatorDelegators

The deprecated legacy compatibility endpoint:

    eth_getBeaconTimeStatus

remains callable but reports itself as deprecated and should not be used for current PoS monitoring.

Reference:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Staking_PoS_Endpoint_Reference.md

---

## Interchain attestation RPC

`v3.1.1` also includes read-only XGR Interchain attestation methods including:

    xgr_getInterchainAttestation
    xgr_getInterchainAttestationByCheckpoint

These methods expose native attestation state.

They do not themselves trigger validator signing or Interchain message submission.

Operator RPC reference:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Node_Operator_RPC_Reference.md

---

# Networking

XGRChain uses libp2p networking with Noise transport security and Gossipsub.

Current defaults:

| Setting | Value |
| --- | ---: |
| P2P port | `1478` |
| Maximum peers | `40` |
| Maximum inbound peers | `32` |
| Maximum outbound peers | `8` |
| Discovery query maximum | `16` peers |
| Normal discovery interval | `5s` |
| Bootnode discovery interval | `60s` |
| Routing-table bucket size | `20` |
| Gossipsub outbound queue | `1024` |
| Gossipsub validation queue | `1024` |

Networking reference:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Networking_P2P.md

---

# Security boundaries

A production deployment should separate:

- validator signing keys,
- libp2p network keys,
- normal transaction keys,
- Interchain relayer keys,
- public RPC infrastructure.

A public RPC user is not a validator.

A connected P2P peer is not a validator.

An Interchain relayer is not an XGRChain consensus validator.

Recommended validator policy:

- restrict JSON-RPC,
- restrict gRPC,
- avoid unrestricted public debug RPC,
- isolate validator workloads from public RPC load,
- protect validator data directories and keys,
- expose only the required P2P interface.

Security and permission documentation:

https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Access_Control_and_Permission_Boundaries.md

---

# XGRChain and XDaLa

`xgr-node` implements the blockchain substrate.

XDaLa is a separate higher-level validation, orchestration and execution framework built on XGR infrastructure.

A standard public XGRChain node does not need to run the complete XDaLa service stack.

XDaLa and XRC specifications are maintained in:

https://github.com/xgr-network/XGR

---

# XGRChain and Interchain

Interchain infrastructure is a separate operational and security domain.

`xgr-node` provides chain primitives including:

- EVM execution,
- finality,
- logs and receipts,
- JSON-RPC,
- native Interchain BLS verification,
- native attestation RPC.

Router deployments, cross-chain validators, relayers and destination security modules are maintained separately.

Repository:

https://github.com/xgr-network/xgr-hyperlane

The first implemented XGR asset route between XGRChain and Base has been validated end-to-end in both directions on mainnet.

That validation does not imply that every relayer direction is continuously enabled or that a public bridge UI is currently open.

---

# Documentation

The main public protocol documentation is maintained in:

https://github.com/xgr-network/XGR

Useful references:

- [XGRChain Introduction](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Introduction.md)
- [Chain Specification](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Chain_Spec.md)
- [Consensus / IBFT](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Consensus_IBFT.md)
- [Node Operation](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Node_Operation.md)
- [Networking / P2P](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Networking_P2P.md)
- [Staking / PoS Model](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Staking_PoS_Model.md)
- [PoS RPC Reference](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Staking_PoS_Endpoint_Reference.md)
- [State Storage and Retention](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_State_Storage_and_Retention.md)
- [Gas Price and Fee Behavior](https://github.com/xgr-network/XGR/blob/main/docs/chain/XRC-GAS_Gas_Price_Behavior.md)
- [Ethereum JSON-RPC Reference](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Ethereum_JSON_RPC_Reference.md)
- [Operator RPC Reference](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Node_Operator_RPC_Reference.md)
- [Access Control](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Access_Control_and_Permission_Boundaries.md)
- [Upgrade and Hardfork Process](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_Network_Upgrade_and_Hardfork_Process.md)

Documentation index:

https://github.com/xgr-network/XGR/blob/main/docs/INDEX.md

---

# Development guidance

Changes affecting any of the following should be treated as high risk:

- EVM execution,
- transaction validity,
- state transition,
- IBFT consensus,
- validator-set calculation,
- stake calculation,
- voting power,
- epoch handling,
- rewards,
- slashing,
- fee calculation,
- native precompiles,
- genesis/fork handling.

For consensus-sensitive code:

- prefer small, reviewable changes,
- add deterministic tests,
- compare behavior across independent nodes,
- verify state roots and receipts,
- test activation boundaries,
- avoid non-deterministic dependencies.

Changes to local-only functionality such as logging or storage retention should still be tested, but they do not automatically require a network hardfork.

---

# Relationship to Polygon Edge

`xgr-node` builds on prior open-source work from Polygon Edge.

Polygon Edge provided the original modular Ethereum-compatible blockchain framework and substantial inherited node/client infrastructure.

XGR Network has since extended and adapted that codebase with XGR-specific:

- delegated PoS behavior,
- staking and delegation,
- stake-weighted consensus,
- uptime weighting,
- reward and slashing logic,
- gas and fee behavior,
- EVM updates,
- native protocol primitives,
- storage management,
- RPC extensions.

This repository is therefore an attributed XGR-maintained protocol adaptation of Polygon Edge-derived components.

It is **not** presented as a clean-room rewrite.

Required upstream copyright and license attribution is preserved in:

[NOTICE](./NOTICE)

Do not remove or weaken upstream attribution when modifying this repository.

---

# Public repository boundary

This repository is intended to remain publicly buildable and inspectable.

It does not necessarily contain every XGR product or service.

Components maintained separately can include:

- XDaLa application services,
- proprietary process-engine functionality,
- user interfaces,
- deployment infrastructure,
- Interchain services,
- enterprise integrations.

The absence of those components does not prevent normal standalone XGRChain node operation.

---

# Security

Please review:

[SECURITY.md](./SECURITY.md)

Do not commit or publish:

- validator private keys,
- wallet private keys,
- seed phrases,
- keystore passwords,
- relayer private keys,
- production credentials,
- private RPC credentials,
- SSH keys,
- internal infrastructure secrets.

---

# Official links

- Website: https://xgr.network
- XGR organization: https://github.com/xgr-network
- Specifications and documentation: https://github.com/xgr-network/XGR
- XGRChain node releases: https://github.com/xgr-network/xgr-node/releases
- Explorer: https://explorer.xgr.network
- Testnet faucet: https://faucet.xgr.network
- Interchain implementation: https://github.com/xgr-network/xgr-hyperlane
- MCP implementation: https://github.com/xgr-network/xgr-mcp

---

# License and attribution

This project contains and builds on open-source software, including code derived from Polygon Edge.

All applicable copyright, license and attribution requirements remain in force.

See:

- [LICENSE](./LICENSE)
- [NOTICE](./NOTICE)

Do not remove or alter third-party legal attribution without appropriate review.

---

# Current status

Current public XGRChain node baseline:

    xgr-node v3.1.1

Commit:

    1a4844b311fb856cb8c2303a40fa8aa69b560544

The public release provides the standalone open-source foundation for XGRChain mainnet node operation, delegated PoS consensus participation, EVM execution, RPC access and XGR-specific protocol functionality.
