# ibft join

One-command validator onboarding for PoS networks using the predeployed staking contract at `0x1001`.

What it does:
- loads (or generates) ECDSA/BLS keys from the secrets manager (DataDir or cloud config)
- registers the BLS public key onchain if not already registered
- stakes the required amount (by sending `stake()` with native value)

Example (local dev):

```
xgrchain ibft join \
  --jsonrpc http://127.0.0.1:8545 \
  --data-dir ./validator1 \
  --stake 200000
```

Example (remote chain node):

```
xgrchain ibft join \
  --jsonrpc http://<rpc-host>:8545 \
  --data-dir ./data \
  --stake 1000000
```
