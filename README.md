# xgr-node

Go-based node software for running an Ethereum-compatible network.

This repository provides the **open-source baseline** node implementation:
- EVM execution
- consensus networking
- standard JSON-RPC interface
- configuration / genesis tooling

> Note: Certain advanced features and protocol extensions are **not** part of this public repository and are distributed separately.

---

## Scope

Included (open source):
- Ethereum-compatible execution layer (EVM)
- validator-based consensus & networking (as implemented in this repo)
- standard Ethereum JSON-RPC endpoints
- chain configuration, genesis & CLI utilities

Not included:
- proprietary rule/process engine modules
- any XGR-specific enterprise extensions
- restricted RPC namespaces or methods

If you are looking for the higher-level project documentation, see the main **XGR** repository.

---

## Chain Configuration

Chain configuration (including reference genesis files) is maintained in the **XGR** repository:

- https://github.com/xgr-network/XGR

---

## Quick Links

- Project Specs / Standards (XGR): https://github.com/xgr-network/XGR
- Website: https://xgr.network
- Testnet Faucet: https://faucet.xgr.network
- Explorer: https://explorer.xgr.network

---

## License / Upstream Notice

This project builds on prior open-source work.
Required copyright and license attributions (especially for code derived from **Polygon Edge**)
are intentionally preserved in `NOTICE` and must not be removed or altered during rebranding.

Please keep the following rules in mind for future changes:
- do not remove existing copyright/license notices
- do not rename third-party attributions in legal texts
- modify attribution/legal notices only with legal approval

---

## Build

Requirements:
- Go 1.23
- Linux (primary dev / supported environment)

macOS may work but is not currently guaranteed or officially supported.

```bash
git clone https://github.com/xgr-network/xgr-node.git
cd xgr-node
make build
