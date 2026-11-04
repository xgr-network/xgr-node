#!/usr/bin/env bash
set -euo pipefail

taskRepoRoot="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$taskRepoRoot"
export GOTOOLCHAIN=go1.23.11
taskGoBinary="${GO_BINARY:-go}"
case "$("$taskGoBinary" version)" in
  *"go1.23.11 "*) ;;
  *) printf '%s\n' 'Go 1.23.11 is required.' >&2; exit 1 ;;
esac

npm ci --prefix scripts/staking-pop-build --ignore-scripts --no-audit --no-fund
node scripts/build-staking-pop.mjs --check
"$taskGoBinary" test -p 2 \
  ./chain ./crypto ./contracts/staking ./helper/staking \
  ./state ./state/runtime/precompiled ./validators/store/contract \
  ./consensus/ibft ./consensus/ibft/fork ./consensus/ibft/signer \
  ./consensus/ibft/blspop ./command/ibft/blspop ./command/ibft/join \
  ./interchain/... ./consensus/ibft/interchain ./command/ibft/interchain
"$taskGoBinary" build -p 2 ./...

taskBuildDir="${XGR_V350_BUILD_DIR:-$(mktemp -d)}"
mkdir -p "$taskBuildDir"
"$taskGoBinary" build -p 2 -o "$taskBuildDir/xgrchain" .
# This test explicitly sets interchainRuntimeDisabled before starting any node.
# All node endpoints and bootnodes are loopback addresses. No production-chain
# worker is constructed and no public chain RPC is part of this devnet test.
EDGE_BINARY="$taskBuildDir/xgrchain" "$taskGoBinary" test -p 2 ./e2e \
  -run '^TestStakingPoP_FourValidatorSingleForkAndFreshSync$' -count=1 -timeout=5m -v
