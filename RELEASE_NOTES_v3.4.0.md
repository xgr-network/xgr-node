# XGR Node v3.4.0 — gated native accounting corrections

This release introduces a coordinated consensus upgrade for native system-transaction accounting and transaction-local SSTORE refund accounting. It also enforces deterministic parent-derived BaseFee validation under the same fork. It does not change the Interchain protocol or require new bridge contracts.

## Credits

**Thank you to [ngjupeng](https://github.com/ngjupeng)** for reporting both vulnerabilities, supplying reproducible tests and contributing the initial fixes. The final integration builds on that work and adds common activation, consensus-path enforcement, explicit accounting invariants and expanded validation. Original contributions remain preserved in the coordinated security-repair history.

## Explicit activation

The fork name is **`securityAccounting`**, configured under **`params.forks`** with a numeric `block` value. All participating validators must use the same schedule.

- Without this entry, the new rules remain inactive at every height.
- Before the configured height, historical execution rules remain in effect.
- At the configured height, including that block, both accounting corrections and the corresponding header/body checks activate together.
- No Mainnet or Testnet activation height is selected or embedded by this release.
- Do not set an already-passed height on an existing network. Preserve existing chain ID, genesis allocation and other network-defining fields.

For the operator's planned Testnet rollout: select a future height, install the verified binary and identical schedule on the validators, then perform a fresh non-validating sync from genesis through and beyond that height. Compare block hashes, state roots, receipt roots and gas usage at H−1/H/H+1 and surrounding epoch boundaries; include transactions that exercise the corrected accounting. Publication of this binary is not a claim that this rollout, a Mainnet activation, or live mixed-version testing has already completed.

## Changes

- Canonical authorization of native system-transaction markers during proposal and finalized-block validation.
- No gas credits for system transactions that have not paid an upfront gas debit.
- Checked BaseFee calculation from the committed parent state; missing or corrupt state inputs fail closed.
- Immutable transaction-start storage baseline for SSTORE originals, including same-block transactions and nested reverts, without an additional full-account scan per transaction.
- Explicit refund counter underflow/overflow invariants, with historical behavior retained before activation.

## Full release validation

The `Release (Integration + EEST + ForkGate + Publish)` workflow runs every non-E2E Go package without `-short` and reports passed, failed and skipped tests explicitly. This includes EVM, tracker, deployment and fixture-runner packages previously omitted by a package denylist. Fixture-dependent or platform-specific skips declared by test code remain visible; discovery is not evidence that such a test ran.

E2E suites run separately, including the four-validator PoS fork-boundary regression. EEST runs against both legacy and activated security rules using a pinned fixture revision; its selected execution-spec scope/filter is recorded by the workflow inputs. The historical ForkGate replays the reference network's existing schedule with a non-sealing node. It does not activate this fork on Mainnet.

Only successful integration, both EEST matrix runs and historical ForkGate permit publication. The release binary carries version/commit metadata, and its checksums and commit manifest are verified before publication. Full release checks execute in GitHub Actions when the operator starts the workflow; local tests alone do not establish those gates passed.

## Operator precautions

This is a consensus change. Roll out and verify binaries before choosing activation. Check effective validator voting power, not merely node count. An old binary may reject a configuration containing an unknown fork, or disagree on affected execution when using its legacy schedule. A post-activation consensus upgrade cannot be rolled back independently like a web service.
