# Interchain operator reference — node v3.4.0

This file specifies the Interchain CLI and JSON-RPC implemented by the public node. Physical network policy is in `interchain/config.go`; message authorization is Direct-BLS. Contract family suffixes do not change the node release number.

- [Architecture and documentation index](https://github.com/xgr-network/XGR/blob/main/docs/interchain/README.md)
- [Validator runtime, grouped observation, durable state and retention](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_Validator_Runtime.md)
- [Independent wallet transfer and unsigned recovery](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_v3.4.0_Operations.md)
- [Source bindings, payload and trust assumptions](https://github.com/xgr-network/XGR/blob/main/docs/interchain/XGR_INTERCHAIN_Security_Model.md)
- [Node release and consensus activation](https://github.com/xgr-network/XGR/blob/main/docs/chain/XGRCHAIN_v3.4.0_Release_and_Activation.md)

Public route registration needs no route-admission vote. Optional website, commercial gateway, sponsor and relayer are not message authorization authorities. Current validator quorum, correct source evidence, protocol fees, RPC access and gas remain necessary for delivery.

## Complete CLI command reference

All commands below use the `xgrchain ibft interchain` prefix. `--json` selects machine-readable command output; read the returned error as well as the process result. The local data-directory commands communicate with the running worker through its storage/queue model; supplying a different directory does not select another chain RPC. Public transfer users do not need a validator data directory or a validator BLS key.

### `bootstrap-proof`

Creates a possession/bootstrap proof from an existing validator ECDSA/BLS identity. It reads secrets and signs the bootstrap payload locally; it does not register a token route or broadcast a destination deployment.

| Flag | Type/default | Requirement and meaning |
| --- | --- | --- |
| `--data-dir` | string, empty | Local secrets-manager directory; mutually exclusive with `--config` |
| `--config` | string, empty | Cloud secrets-manager configuration; exactly one of these two is required |
| `--origin-chain-id` | uint64, 0 | Required nonzero XGR origin chain identity |
| `--destination-domain` | uint32, 0 | Required nonzero destination domain |
| `--json` | boolean, false | Structured output |

```sh
xgrchain ibft interchain bootstrap-proof \
  --data-dir /ABS/VALIDATOR_DATA \
  --origin-chain-id 1643 --destination-domain 8453 --json
```

Result fields: `validator` (ECDSA address), `originChainId`, `destinationDomain`, `blsPublicKey` (compressed public key), `blsPublicKeyEIP2537`, `payload` and `possessionProof` (EIP-2537 representation). The payload binds identity and target domain. Both secret keys must already exist; missing/unparseable secrets or failed key conversion produce an error. The output contains public proof material, not private keys. Verify it against the intended destination Registry bootstrap interface before use.

### `set-active`

Requests activation/deactivation of this validator on an already configured destination. This is validator membership management, not permission to create asset routes. The worker validates canonical XGR eligibility, collects membership quorum, sends the destination transaction where needed and checks final state.

| Flag | Type/default | Meaning |
| --- | --- | --- |
| `--data-dir` | string, empty | Required running validator directory |
| `--chain` | string, empty | Required configured destination name, e.g. `base` or `arbitrum` |
| `--active` | string, empty | Required explicit `true` or `false`; no implicit default |
| `--timeout` | duration, `2m` | Positive wait for quorum, receipt and final-state verification |
| `--json` | boolean, false | Structured output |

```sh
xgrchain ibft interchain set-active \
  --chain base --active true --data-dir /ABS/VALIDATOR_DATA --timeout 2m --json
xgrchain ibft interchain set-active \
  --chain base --active false --data-dir /ABS/VALIDATOR_DATA --timeout 2m --json
```

Result fields: `validator`, normalized `chain`, `requestedActive`, `finalActive`, `changed`, `setId`, optional `transactionHash`, `destinationCommitted`. A matching pre-existing state can return `changed:false` without a new destination transaction. A timeout is not proof that no transaction was sent: inspect destination state and worker outcome before retrying. Reject empty directory/chain, unspecified or invalid active string and nonpositive timeout.

### `fee create`

Builds a positive, source-native fee proposal for the entire configured source chain. Creation **does not sign approval**. The command reads current destination membership set and confirmed source fee nonce, constructs the proposal and queues it to the worker.

| Flag | Type/default | Meaning |
| --- | --- | --- |
| `--data-dir` | string, empty | Required running validator directory |
| `--source` | string, empty | Required configured source-chain name |
| `--fee-wei` | decimal string, empty | Required positive uint256 in the source native currency's smallest unit |
| `--nonce` | uint64, `0` | `0` auto-detects confirmed nonce+1; explicit value must equal the next nonce |
| `--ttl` | duration, `5m` | Positive proposal lifetime, maximum `10m` |
| `--timeout` | duration, `2m` | Worker response wait |
| `--json` | boolean, false | Structured output |

```sh
# Example amount only: 1,000,000,000 wei. Select the intended economic fee yourself.
xgrchain ibft interchain fee create \
  --source base --fee-wei 1000000000 --ttl 5m \
  --data-dir /ABS/VALIDATOR_DATA --json
```

The proposal binds `sourceChainId`, `sourceDomain`, source Registry, current `setId`, next `nonce`, Unix `validUntil` and `validatorFeeWei`. Result: `proposalId`, `approved` and `quorum`. `approved:false` is expected from unsigned creation. Errors include invalid/zero/out-of-range fee, invalid TTL, missing chain bindings, unreadable membership/nonce, explicit nonce mismatch and nonce exhaustion. Do not interpret the example amount as the deployed current fee.

### `fee approve`

Explicitly approves an existing source-fee proposal with this validator's BLS key. This is the signing step and must follow inspection of the actual proposal's source, Registry, set, nonce, expiry and amount.

| Flag | Type/default | Meaning |
| --- | --- | --- |
| `--data-dir` | string, empty | Required running validator directory |
| `--proposal-id` | 0x-prefixed bytes32, empty | Required nonzero 32-byte proposal hash |
| `--timeout` | duration, `2m` | Worker response wait |
| `--json` | boolean, false | Structured output |

```sh
xgrchain ibft interchain fee approve \
  --proposal-id "$PROPOSAL_ID" --data-dir /ABS/VALIDATOR_DATA --json
```

Result: `proposalId`, `approved`, `quorum`. One approval is not a completed 2/3 quorum. Invalid hash syntax, zero hash, missing local storage/proposal, ineligible identity or worker validation failure is an error. This command does not create or approve a route registration.

### `fee execute`

Submits an existing completed source-fee quorum on chain and confirms the resulting nonce. It does not sign another validator's vote and does not create routes.

| Flag | Type/default | Meaning |
| --- | --- | --- |
| `--data-dir` | string, empty | Required local running validator directory |
| `--proposal-id` | nonzero bytes32, empty | Existing completed source-fee proposal |
| `--timeout` | positive duration, `6m` | On-chain execution wait |
| `--json` | boolean, false | Structured output |

```sh
xgrchain ibft interchain fee execute \
  --proposal-id "$PROPOSAL_ID" --data-dir /ABS/VALIDATOR_DATA --timeout 6m --json
```

Result: `proposalId`, `transactionHash`, confirmed `nonce`. The source Registry rejects an invalid quorum, wrong current set, stale/nonconsecutive nonce, expired proposal, wrong binding or invalid fee. Execution requires source-chain availability and transaction gas. After a timeout inspect the canonical transaction/nonce before another submission.

### `storage-status`

Read-only offline measurement of persisted Interchain state. It does not enqueue work, sign, delete files, rebuild proofs or contact a destination.

| Flag | Type/default | Meaning |
| --- | --- | --- |
| `--data-dir` | string, empty | Required node directory to inspect |
| `--json` | boolean, false | Structured measurement |

```sh
xgrchain ibft interchain storage-status --data-dir /ABS/VALIDATOR_DATA --json
```

The result reports persisted file/byte totals and attestation, local-vote, quorum-request and governance categories. A missing directory/file permission failure is not an empty healthy database. Size measurements do not prove source finality, quorum completeness, delivery or a recoverable backup.

## Complete message and monitoring RPC reference

The examples below are request/response **shapes**, not observations of a live transfer. Replace `ROUTE`, `MESSAGE_ID`, `SOURCE_BLOCK`, `SET_ID` and `PROPOSAL_ID` with independently obtained values. Numeric `uint64` parameters use JSON numbers, not hex quantity strings. Very large numbers require an exact-integer client; do not round with JavaScript `Number`.

For route ID `0x` followed by 64 lowercase hex digits, the canonical node route name is:

```text
iln_<sourceDomain>_<destinationDomain>_<routeIdWithout0x>
```

The parser rejects zero domains, zero route ID, extra components, nonhex characters and noncanonical decimal representations. A route name is not just `base` or `arbitrum`; those names are used for physical-chain configuration/membership commands.

### `xgr_requestILNQuorum(route, messageId, sourceBlockNumber)`

Parameters, in order: canonical route name (string), nonzero 0x-prefixed 32-byte original message ID (string), nonzero original source RPC block number (uint64). This creates an idempotent discovery/processing **hint**, not an instruction to sign supplied bytes. The worker must reconstruct and validate actual source evidence.

```json
{"jsonrpc":"2.0","id":1,"method":"xgr_requestILNQuorum","params":["ROUTE","MESSAGE_ID",12345678]}
```

Successful result shape:

```json
{"route":"ROUTE","messageId":"MESSAGE_ID","sourceBlockNumber":12345678,"status":"PENDING"}
```

The node requires a fresh worker heartbeat (10-second admission check), local Interchain storage and either a discovered route or parseable deterministic discovery hint. Unknown route hints and per-route message request queues are bounded to 128 entries. A 15-minute request lease permits retry of stale hints; repeating the same identity/source block is idempotent. Reusing a live request with a different source block is rejected. Queue admission never replaces Registry/source validation.

Typical error messages include `ILN worker not available`, `route is not configured or discovered locally`, `ILN discovery hint queue full`, `ILN request queue full; retry later`, invalid message ID, missing source block and conflicting persisted request identity.

### `xgr_getILNQuorum(route, messageId)`

```json
{"jsonrpc":"2.0","id":2,"method":"xgr_getILNQuorum","params":["ROUTE","MESSAGE_ID"]}
```

Returns `route`, `messageId`, `sourceBlockNumber`, `status` and, when recorded, `votes`, `quorumThreshold`, `failureCategory`, `updatedUnix`.

| Status | Meaning and action |
| --- | --- |
| `NOT_FOUND` | No local archived quorum/progress/request; request the original message using its source block |
| `PENDING` | An admission hint exists; no conclusion about valid signatures yet |
| `EXPIRED_RETRY` | Admission hint exceeded its lease; repeat the same original identity |
| `PARTIAL_QUORUM` | Recorded valid progress below threshold; inspect votes/eligible validator availability |
| `RETRYING_VALIDATION` | Worker is retrying canonical evidence validation; inspect failure category/source RPC |
| `AVAILABLE_UNVERIFIED` | Local quorum data exists; current destination set and execution validity still require verification |

Progress records are diagnostics, not authorization. The implementation bounds accepted progress-record size and age, and matches recorded route/message identity. `AVAILABLE_UNVERIFIED` can refer to a stale set after rotation and is never equivalent to `Mailbox.delivered == true`.

### `xgr_getILNQuorumAttestation(route, messageId, setId)`

```json
{"jsonrpc":"2.0","id":3,"method":"xgr_getILNQuorumAttestation","params":["ROUTE","MESSAGE_ID",7]}
```

Returns the complete archived attestation for the explicitly requested nonzero destination set. It does not silently choose another set. The reader enforces Direct-BLS domain, required fields, canonical payload consistency, nonempty bitmap and aggregate lengths. It returns an error if no matching attestation exists, storage is invalid or archived identity/set does not match.

| Result field | Type and meaning |
| --- | --- |
| `version` | `XITA_DIRECT_MESSAGE_V1` |
| `chain`, optional `destination` | Node route label and destination diagnostic label |
| `originChainId`, `originDomain`, `destinationDomain` | Signed source/destination identities |
| `routeId`, `setId`, `sourceBlockNumber` | Signed directed route, destination epoch and original source block |
| `registry`, `gateway`, `sourceRouter`, `mailbox` | Signed source contract bindings |
| `destinationRouter`, `destinationIsm`, `destinationRegistry` | Signed recipient/authentication contract bindings |
| `validatorFeeWei` | Exact unsigned fee as a decimal string |
| `authorizedMessageId` | Original dispatched message hash |
| `payload` | Canonical fixed-width signing bytes, hex |
| `signerBitmap` | Bitmap in canonical destination Registry order, hex |
| `aggregateSignatureCompressed` | 96-byte compressed aggregate, hex |
| `aggregateSignature` | 256-byte EIP-2537 aggregate, hex |

Do not sort validators before interpreting the bitmap. Compare `setId` with the live destination registry and verify the quorum/ISM on that destination. The archival read does not prove that the set is current, that the recipient can execute or that the message has not already been delivered.

### `xgr_getInterchainAttestation(route)` and `xgr_getILNInterchainAttestation(route,messageId)`

These compatibility methods read the route's latest archive or its message archive respectively. They return the same validated Direct-BLS result structure. Old implementation comments or method labels containing “checkpoint” do not turn the current result into a Merkle proof. For delivery prefer the explicit-set method above, because an arbitrary latest entry need not be the desired message/current set. `xgr_getInterchainAttestationByCheckpoint` is not documented as a supported current method.

### `xgr_getInterchainValidators(destinationName)`

```json
{"jsonrpc":"2.0","id":4,"method":"xgr_getInterchainValidators","params":["base"]}
```

Result fields: `destination`, `chainId`, `domain`, configured `registry`, `setId`, `count`, `quorumThreshold` and ordered `validators` entries with `address` and 48-byte compressed `blsPublicKey`. This reads the configured destination's membership set; callers cannot supply their own RPC/Registry. It rejects unknown destinations and invalid, empty, duplicate or malformed set entries.

Quorum threshold is `ceil(2*n/3) = (2*n+2)/3` with integer division. This is not XGRChain stake-weighted voting. A one-member set is representable; the API does not prove sufficient operational decentralization or current node uptime.

### `xgr_getILNGovernanceQuorum(proposalId)`

```json
{"jsonrpc":"2.0","id":5,"method":"xgr_getILNGovernanceQuorum","params":["PROPOSAL_ID"]}
```

Read-only retrieval of a completed **source fee** quorum. Result fields: `version`, `proposalId`, `sourceChainId`, `sourceDomain`, `registry`, decimal-string `validatorFeeWei`, `setId`, `nonce`, Unix `validUntil`, canonical hex `payload`, `signerBitmap`, both aggregate signature encodings. Reading does not create, approve or execute a proposal. Validate source bindings, current set, expiry and next on-chain nonce before execution; the method does not expose a route-admission governance mechanism.

### `xgr_getILNIndexerStatus()`

```json
{"jsonrpc":"2.0","id":6,"method":"xgr_getILNIndexerStatus","params":[]}
```

Top-level fields: `version` (schema 2), `updatedUnix`, `workerAlive`, `state`, `routes`. Each route includes `route`, `source`, `destination`, `state`, `sourceHead`, `scanCursor`, `blockLag`, `sourceHeadKnown`, `scanCursorKnown`, optional `lastSuccessfulScanUnix`, `updatedUnix`, optional `error`.

The implementation marks a heartbeat snapshot older than two minutes, or more than 30 seconds in the future, as stalled. It derives aggregate state from worker and per-route states; therefore inspect per-route errors and timestamps rather than treating a single summary string as a cryptographic health certificate. Unknown head/cursor fields are not evidence of zero lag. This monitor is independent of the XITA transfer indexer's database.

### HTTP transport and error handling

```sh
curl -fsS http://127.0.0.1:8545 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"xgr_getILNIndexerStatus","params":[]}'
```

Check JSON-RPC `error`, not only HTTP status or curl exit code. Application errors use the node dispatcher's JSON-RPC envelope with a message; this reference does not assign invented stable error codes to implementation errors. Missing files, malformed storage, worker unavailability and destination RPC failures must remain visible and must not be transformed into successful proof readiness. Deploy authenticated/rate-limited RPC infrastructure according to operator policy; public quorum hints never provide arbitrary signing authority.

## Storage result fields

`baseDir`, `totalFiles`, `totalBytes`, `attestationFiles`, `localVoteFiles`, `requestFiles`, `governanceFiles`, `otherFiles`. Category values are counts; `totalBytes` is the measured aggregate bytes.

## Storage result fields

`baseDir`, `totalFiles`, `totalBytes`, `attestationFiles`, `localVoteFiles`, `requestFiles`, `governanceFiles`, `otherFiles`. Category values are counts; `totalBytes` is the measured aggregate bytes.

## Storage result fields

`baseDir`, `totalFiles`, `totalBytes`, `attestationFiles`, `localVoteFiles`, `requestFiles`, `governanceFiles`, `otherFiles`. Category values are counts; `totalBytes` is the measured aggregate bytes.
