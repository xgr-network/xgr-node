#!/usr/bin/env bash
set -euo pipefail

# -----------------------------------------------------------------------------
# Hardcoded configuration (edit these values directly)
# -----------------------------------------------------------------------------
RPC_URL="http://127.0.0.1:8545"
OUT_FILE="balance_report.txt"
POOL_ADDR="0x000000000000000000000000000000000000fEE2"

VALIDATOR_ADDRS=(
  "0x1111111111111111111111111111111111111111"
  "0x2222222222222222222222222222222222222222"
)
# -----------------------------------------------------------------------------

if [ "${#VALIDATOR_ADDRS[@]}" -lt 1 ]; then
  echo "Please set at least one address in VALIDATOR_ADDRS at the top of this script."
  exit 1
fi

rpc_call() {
  local method="$1"
  local params_json="$2"
  curl -sS -X POST "$RPC_URL" \
    -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"method\":\"${method}\",\"params\":${params_json},\"id\":1}"
}

extract_result() {
  local payload="$1"

  python3 - "$payload" <<'PY'
import json
import sys

payload = json.loads(sys.argv[1])
if payload.get("error") is not None:
    raise SystemExit(f"RPC error: {payload['error']}")
result = payload.get("result")
if result is None:
    raise SystemExit("RPC response missing result")
print(result)
PY
}

hex_to_dec() {
  local hex_value="$1"
  python3 - "$hex_value" <<'PY'
import sys
print(int(sys.argv[1], 16))
PY
}

block_hex="$(extract_result "$(rpc_call "eth_blockNumber" '[]')")"
block_dec="$(hex_to_dec "$block_hex")"

{
  printf "\n==== %s | block=%s ====" "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" "$block_dec"
  printf "\n%-12s | %-44s | %-20s | %-10s\n" "ENTITY" "ADDRESS" "BALANCE_WEI" "BALANCE_HEX"
  printf -- "%.12s-+-%.44s-+-%.20s-+-%.10s\n" "------------" "--------------------------------------------" "--------------------" "----------"

  pool_hex="$(extract_result "$(rpc_call "eth_getBalance" "[\"$POOL_ADDR\",\"latest\"]")")"
  pool_dec="$(hex_to_dec "$pool_hex")"
  printf "%-12s | %-44s | %-20s | %-10s\n" "POOL" "$POOL_ADDR" "$pool_dec" "$pool_hex"

  idx=1
  for validator in "${VALIDATOR_ADDRS[@]}"; do
    bal_hex="$(extract_result "$(rpc_call "eth_getBalance" "[\"$validator\",\"latest\"]")")"
    bal_dec="$(hex_to_dec "$bal_hex")"
    printf "%-12s | %-44s | %-20s | %-10s\n" "VALIDATOR_$idx" "$validator" "$bal_dec" "$bal_hex"
    idx=$((idx + 1))
  done
} >> "$OUT_FILE"

echo "Appended balances to: $OUT_FILE"
