# JSON-RPC XGR API

## xgr_getNextProcessId

Returns the next sequential `processId` for the provided address. The result is a decimal string.

Example:

```json
{"jsonrpc":"2.0","id":1,"method":"xgr_getNextProcessId","params":[{"from":"0x<addr>"}]}
```

## xgr_validateDataTransfer

The `processId` field in permits is a `uint256` and is serialized as a decimal string in JSON-RPC. This method returns a quick acknowledgement without executing the session. The response contains a `status` field with one of the following values:

| status          | meaning                                                   |
|-----------------|-----------------------------------------------------------|
| `queued`        | new session or due step queued for immediate execution    |
| `already-queued`| session was already active; no additional action taken    |
| `scheduled`     | session is waiting for its scheduled wake-up time         |
| `paused`        | session is paused and was not re-queued                   |
