# XGR-Node v3.5.0 — private security release candidate

Status: private review candidate. No Mainnet activation height, public tag,
deployment, release publication or advisory publication is authorized here.

Baseline: v3.4.3, `0d9aca695c49784da64736d78620cc1bba4f7daa`.
Advisory: GHSA-rhv3-x676-72g9. Reported by **alfset**.

## Security fix

Staking BLS key registration and rotation require a possession signature bound
to the validator address, chain ID, staking contract, public-key hash and a
monotonically consumed nonce. The existing BLS12-381 precompile verifies the
signature. Registration through the old proofless selector reverts after the
runtime upgrade. Consensus signature encodings remain unchanged.

One explicit fork, `params.forks.stakingBlsPoP.block`, jointly activates the
staking runtime upgrade, independently verified manifest migration, PoP-only
validator selection and validation of cached consensus sets. There is no default
production height. Historical block and parent-seal validation use the rules of
the height being signed.

The staking address remains `0x0000000000000000000000000000000000001001`.
Migration preserves existing storage, balances, stakes, delegation records,
weights, activation data and history. Only verified-key history, nonces and the
migration digest are appended. Existing validators do not restake.

## Operator tools

`ibft bls-pop proof`, `assemble`, `verify` and `configure` collect public evidence
from existing local secrets, build a canonical manifest and prepare a separate
configuration file. Private keys are neither exported nor included in evidence.
`ibft join` automatically supplies a fresh PoP for the upgraded runtime and
refuses unknown runtime code or a proofless retry.

An optional local service setting, `interchainRuntimeDisabled: true` inside the
IBFT configuration, disables external-chain worker construction for isolated
consensus tests. It is not a consensus fork and is unset for normal operation.
The immutable XITA network policy and Direct-BLS wire protocol are unchanged.

## Validation and rollout

Run `bash scripts/validate-v3.5.0.sh`. It uses Go 1.23.11, locked solc 0.8.30,
compiler storage-layout comparison, security regressions and an isolated
four-validator fork/restart/fresh-sync devnet. See
`docs/security/v3.5.0.md` and `docs/security/v3.5.0-rollout.md` for the precise
security boundary, evidence requirements and recovery procedure.

Mainnet rollout still requires production proof collection, final private review,
an approved activation height and explicit operator authorization.
