# XGR-Node v3.2.0: restore the Hyperlane checkpoint architecture

Status: **implementation in progress; NOT a release or deployment authorization**.

## Baseline
- xgr-node v3.1.1 read confirmed Hyperlane MerkleTreeHook mailbox(), count() and root() on the source chain.
- v3.1.8 replays InsertedIntoTree logs up to the operation block. Public Base RPCs return HTTP 429 for these calls.
- New `GetConfirmedILNHookCheckpoint` is an isolated read-only first step. It is **not** wired to validator signing yet.

## Safety-critical protocol work still required
1. Bind a fee-qualified exact Gateway operation and Dispatch to an immutable route and original source transaction.
2. Separate `operationBlock` from `checkpointBlock` in checkpoint payload semantics. A newer Merkle root must NEVER be falsely represented as the root at the earlier operation block.
3. Require an independently verified inclusion proof for the authorized message before BLS signing. Untrusted relayers must not choose checkpoint, message, route, or fee.
4. Ensure validators agree on one canonical confirmed checkpoint and current destination validator set.
5. Remove mandatory historical eth_getLogs and historical state calls from the signing / replay / recovery paths, including on cold start.
6. Preserve Hyperlane Mailbox.process and permissionless delivery; do not alter XGRChain IBFT consensus or native BLS primitives.
7. Rate-limit, batch, and backoff RPC calls; test concurrent requests and signer rotation.
8. Run existing v3.1.8 tests and full 3-validator end-to-end fixtures before release.

## Unavoidable data-availability boundary
A standard MerkleTreeHook root is a commitment, not a message archive. After TOTAL
loss of all off-chain message/proof indexes, reconstructing arbitrary older
messages without historical receipts/logs or archive state is impossible from
the root alone. This must be resolved as an explicit, measured protocol/storage
decision BEFORE declaring full disaster recovery. Never silently substitute
a trusted relayer or falsely claim complete recovery.

## Mainnet gate
No restart, no new TX, no deployment wipe, no tag v3.2.0 before tests and
the coordinated XITA deployment graph are ready.
