# v3.5.0 private security candidate

Advisory: GHSA-rhv3-x676-72g9. Baseline: `0d9aca695c49784da64736d78620cc1bba4f7daa`.

Implementation and isolated validation are complete for review. This is a private
release candidate, not authorization to deploy, publish or activate Mainnet.

## Implemented

- Chain/address/contract/key/nonce-bound BLS possession verification using the
  existing signature primitive and 0x2040 singleton verifier.
- Storage-compatible staking runtime, ABI, pinned reproducible compiler build
  and frozen historical runtime.
- Canonical independently verified migration manifest, exact registered-key and
  signing-snapshot coverage, consumed migration nonces and persistent digest.
- Exactly one optional gate, `stakingBlsPoP`, with no production height selected.
  Shared pre-transaction execution covers proposal and finalized-block import.
- Cached snapshot, normal and emergency admission guards, using the signed
  block's height for historical and ParentCommittedSeals verification.
- Local-secret proof collection, assembly, verification, explicit chain-file
  preparation and automatic proof generation in validator join.
- English release notes, security analysis and rollout/recovery instructions.

## Validation

`scripts/validate-v3.5.0.sh` completed successfully with Go 1.23.11 and solc
0.8.30. This includes the relevant Go regression suites, full Go compilation,
byte-for-byte artifact reproduction and an isolated four-validator fork test.
The test includes an observer, validator restart and fresh-node sync from
Genesis. See `docs/security/v3.5.0-validation.md` for exact evidence.

The advisory body was read in the authenticated browser. Original PoC attachment
access returned `ERR_BLOCKED_BY_CLIENT`; neither original attachment was read or
executed. An independent rogue-key cancellation regression uses the real legacy
committed-seal verifier, includes an ordinary-key negative control and proves
that the rogue key cannot pass the new possession admission.

An initial devnet attempt exposed an existing Interchain worker's public-RPC
startup. Automatic approval review rejected that run. The harness now explicitly
sets `interchainRuntimeDisabled`, checked before worker construction. The safe
isolated replacement passed twice. Production Interchain defaults are unchanged.
No attack test against a public RPC or Mainnet was performed.

## Remaining production prerequisites

Collect genuine proofs on each existing validator with its own local BLS key;
freeze the exact parent-state and snapshot inventory; independently review the
cryptography, upgrade and coverage logic; approve a concrete activation height
and canonical production manifest; rehearse that exact configuration and follow
the private rollout runbook. Existing keys are never grandfathered. No private
key belongs in a manifest, log or repository.

No Mainnet activation, deployment, public PR, public merge, release publication
or advisory publication has been performed.

## Final internal review (2026-11-03)

The final internal review is complete; see `docs/security/v3.5.0-final-review.md`
for reviewed invariants, explicit operational risks, reproducible test commands
and the reporter handoff. Additional EVM rotation/fund-exit and real-verifier
infinity regression tests pass. The full isolated validation passed again.
Production source and bytecode are unchanged from private commit
`4397c5e2c07ecbee9c3185678878bd03bc256bb5`; follow-up commits add tests/docs.
The candidate is ready for private reporter review, not Mainnet activation.
