#!/usr/bin/env bash
# One-time PREDEPLOY development reset: XITA Interchain worker state ONLY.
# Never touches the EVM database, validator keys, wallet, or chain consensus.
set -euo pipefail
if [[ $# -ne 2 || "$2" != "--apply-hard-reset" ]]; then
  echo "Usage: XGR_INTERCHAIN_WORKER_STOPPED=yes $0 /absolute/xgrchain/data --apply-hard-reset" >&2
  exit 2
fi
[[ "${XGR_INTERCHAIN_WORKER_STOPPED:-}" == "yes" ]] || {
  echo "Stop the XGR Interchain workers on this validator before wiping their state." >&2
  exit 2
}
base="$(realpath -e -- "$1")"
[[ "$base" = /* && "$base" != "/" && "$base" != "/home" && "$base" != "/root" ]] || exit 2
target="$base/interchain"
[[ ! -L "$target" ]] || { echo "Refusing symlinked interchain state" >&2; exit 2; }
if [[ -d "$target" ]]; then
  echo "Removing development Interchain worker state: $target"
  rm -rf -- "$target"
else
  echo "Interchain state already empty: $target"
fi
echo "Interchain hard reset complete; chain database and validator keys untouched."
