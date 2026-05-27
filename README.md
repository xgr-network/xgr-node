# xgr-node XGR2.0

`xgr-node` is the public Go-based node baseline for the XGR Network.

It provides an Ethereum-compatible execution and validator node foundation for running XGRChain-compatible networks, including EVM execution, validator-based consensus/networking, standard JSON-RPC access, chain configuration and genesis tooling.

XGR2.0 is derived from Polygon Edge components and preserves all required upstream attribution. However, it materially diverges from the inherited baseline in its permissionless staking-based PoS model, stake-weighted quorum behavior, delegation and slashing mechanics, XGR-specific gas and fee logic, Shanghai-era EVM updates and precompile-level engine integration.

This repository is not presented as a clean-room rewrite. It is an attributed, XGR-maintained protocol adaptation of Polygon Edge-derived components.

---

## What this repository is

This repository contains the public baseline node implementation for XGR Network.

Included in this public baseline:

- Ethereum-compatible execution layer based on the EVM
- Validator-based consensus and networking as implemented in this repository
- Permissionless staking-based validator participation
- Stake-weighted quorum / voting-power logic
- Staking, delegation and slashing-related protocol logic
- XGR-specific gas accounting and fee-distribution behavior
- Shanghai-era EVM / protocol updates
- Standard Ethereum JSON-RPC interface
- Chain configuration, genesis and CLI tooling
- Precompile-level integration points for the XGR engine
- Public build baseline for XGR-compatible node software
- Stubbed integration points for private XGR engine modules where required

The goal of this repository is to provide a buildable, inspectable, open-source node baseline for the XGR ecosystem.

---

## Relationship to Polygon Edge

`xgr-node` XGR2.0 builds on prior open-source work from Polygon Edge.

Polygon Edge provided a modular Ethereum-compatible blockchain framework and validator/client baseline. XGR2.0 adapts and extends relevant components under the `xgr-network/xgr-node` module for the XGR Network.

All required upstream copyright and license attributions are preserved in `NOTICE`.

This repository should therefore be understood as:

> An XGR-specific, attributed continuation and protocol adaptation of Polygon Edge-derived code, maintained as the public baseline node for XGR Network.

It is not intended to obscure, remove or weaken the Polygon Edge origin of inherited components.

---

## What makes XGR2.0 different from Polygon Edge

XGR2.0 is not a cosmetic rebrand of Polygon Edge.

While it preserves Polygon Edge attribution for inherited components, the `XGR2.0` branch introduces substantial protocol-level changes that give the node its own XGR-specific chain identity.

The most important differences are:

### Permissionless staking-based PoS

XGR2.0 extends the inherited validator-set architecture into a permissionless, staking-based Proof-of-Stake model.

Validator participation is tied to protocol-level staking rules rather than only to a manually configured static validator set.

### Stake-weighted quorum / voting power

XGR2.0 adapts quorum behavior around validator weight and voting power.

This is one of the strongest consensus-level differences from the Polygon Edge baseline. Consensus participation is no longer only an equal-weight validator-set assumption, but is connected to the staking and voting-power model used by XGRChain.

### Staking, delegation and slashing

XGR2.0 includes staking-aware validator state, delegation mechanics and slashing-related protocol logic.

These features define the validator economics and security model of XGRChain.

### Permissionless validator participation

The validator model is designed for open participation under protocol-defined staking and validator rules.

This differs from a purely permissioned or manually curated validator-set assumption.

### XGR-specific gas logic

XGR2.0 contains XGR-specific gas accounting and gas-handling behavior.

This affects how execution costs are evaluated, charged and represented inside the node.

### XGR-specific fee distribution

Fee handling is adapted to XGRChain economics, including XGR-specific distribution logic for validator and network incentives.

### Shanghai-era EVM / EIP updates

XGR2.0 implements important Ethereum Shanghai-era EVM and protocol updates, modernizing the execution layer beyond the inherited baseline.

### Engine precompile extension

XGR2.0 adds precompile-level integration points for the XGR engine.

This allows selected engine functionality to be exposed through native execution-layer hooks instead of being implemented only as external application logic or ordinary smart contracts.

These changes make XGR2.0 an XGR-specific protocol adaptation of Polygon Edge-derived components, not merely a renamed fork.

---

## Comparison with Polygon Edge

| Area | Polygon Edge baseline | xgr-node XGR2.0 |
| --- | --- | --- |
| Repository ownership | `0xPolygon/polygon-edge` | `xgr-network/xgr-node` |
| Purpose | Generic Ethereum-compatible blockchain framework | Public baseline node for XGRChain |
| Module path | `github.com/0xPolygon/polygon-edge` | `github.com/xgr-network/xgr-node` |
| Consensus model | Validator-set based IBFT baseline | Permissionless staking-based PoS with weighted quorum behavior |
| Validator admission | Static / configured validator-set assumptions | Staking-based, permissionless validator participation |
| Voting power | Baseline validator quorum behavior | Stake-/weight-aware quorum and voting-power logic |
| Staking | Not the core protocol identity | Native staking-aware validator state |
| Delegation | Not a primary baseline feature | Delegation-related validator economics |
| Slashing | Not a primary baseline feature | Slashing-related protocol logic |
| Execution model | Ethereum-compatible EVM execution | Ethereum-compatible EVM execution with XGR-specific runtime assumptions |
| Shanghai-era EIPs | Older inherited execution baseline | Important Shanghai-era EVM / protocol updates implemented |
| Gas logic | Baseline gas accounting | XGR-specific gas accounting and fee handling |
| Fee distribution | Baseline fee behavior | XGR-specific fee distribution model |
| Engine integration | Not applicable | XGR engine integration via precompile-level extension points |
| Attribution | Polygon Technology | XGR Network GmbH + preserved Polygon Edge attribution |
| Public scope | Polygon Edge client/framework | XGR public node baseline |

---

## Public baseline scope

This repository is intended to remain buildable and useful without access to private XGR repositories.

The public baseline includes:

- EVM execution
- Validator-based consensus / networking components available in this repo
- Permissionless staking-based validator logic
- Weighted quorum / voting-power behavior
- Staking, delegation and slashing-related protocol code
- XGR-specific gas and fee handling
- JSON-RPC server functionality
- CLI and genesis tooling
- Configuration support
- Precompile-level engine integration points
- Public release baseline

The public baseline does **not** include every XGR product, enterprise module or private deployment component.

---

## Not included / maintained separately

The following components are not part of this public baseline repository:

- Proprietary XGR rule/process engine modules
- Enterprise-specific extensions
- Restricted RPC namespaces or methods
- Private deployment infrastructure
- Internal production operations tooling
- Private XGR engine implementation repositories

These components may be maintained separately where required for security, product, operational or enterprise reasons.

The public repository remains designed to build without access to private XGR repositories.

---

## xgrEngine stub and private engine integration

The public repository is designed to build standalone.

Where XGR engine integration is referenced, the repository uses a local stub module by default so that public builds do not require access to the private `xgrEngine` implementation.

For private or embedded engine builds, the stub can be overridden through the existing workspace / build-tag setup used by the XGR development environment.

Do not remove or break the local stub behavior unless the public build path is replaced by an equivalent open build path.

---

## Quick links

- Website: https://xgr.network
- GitHub organization: https://github.com/xgr-network
- XGR project specs / standards: https://github.com/xgr-network/XGR
- Explorer: https://explorer.xgr.network
- Testnet faucet: https://faucet.xgr.network
- Security policy: [SECURITY.md](./SECURITY.md)
- Upstream / attribution notice: [NOTICE](./NOTICE)

---

## Build requirements

Recommended environment:

- Go 1.23+
- Linux as primary supported development/build environment
- `make`

macOS may work for some workflows, but Linux is the primary supported environment.

---

## Build

Clone the repository:

```bash
git clone https://github.com/xgr-network/xgr-node.git
cd xgr-node
git checkout XGR2.0
```

Build using the repository Makefile:

```bash
make build
```

For a full Go build check:

```bash
go build ./...
```

Before handing over, reviewing or committing chain-code changes, the full code tree should build successfully with:

```bash
go build ./...
```

This is especially important for changes in:

- `jsonrpc/`
- `core/`
- `contracts/`
- `state/`
- `types/`
- consensus / validator logic
- staking, delegation or slashing logic
- weighted quorum / voting-power logic
- gas accounting
- fee distribution
- EVM / precompile logic
- genesis or chain configuration tooling

---

## Development notes

When contributing or reviewing changes:

- Preserve all upstream copyright and license notices.
- Do not remove Polygon Edge attribution.
- Do not remove or weaken `NOTICE`.
- Keep the public build path working without private repositories.
- Do not break the local `xgrEngine` stub setup.
- Treat consensus, validator, staking, slashing, quorum, EVM, state and RPC changes as high-risk changes requiring careful review.
- Prefer small, auditable patches over broad rewrites.
- Do not publish secrets, private validator keys, production keys, private RPC credentials or internal deployment credentials in this repository.

---

## Security

Please review [SECURITY.md](./SECURITY.md) for security-related reporting and handling.

Do not publish secrets, production keys, validator keys, private RPC credentials or internal deployment credentials in this repository.

---

## License / Upstream Notice

This project builds on prior open-source work.

Required copyright and license attributions, especially for code derived from Polygon Edge, are intentionally preserved in `NOTICE` and must not be removed or altered during rebranding, refactoring or documentation updates.

Please keep the following rules in mind for future changes:

- Do not remove existing copyright or license notices.
- Do not rename third-party attributions in legal texts.
- Do not remove or weaken Polygon Edge attribution.
- Modify attribution or legal notices only with legal approval.


## Status

`xgr-node` XGR2.0 is the public XGR node baseline.

It provides the open technical foundation for XGR-compatible node operation while keeping selected advanced XGR modules, enterprise integrations and restricted components in separate repositories.

XGR2.0 should be understood as an XGR-specific protocol adaptation of Polygon Edge-derived components with its own validator-economic, consensus and execution-layer profile.
