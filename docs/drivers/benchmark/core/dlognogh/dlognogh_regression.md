# ZK-ATDLOG Regression Tests

This document explains the regression testing for the Zero-Knowledge Anonymous Token Discrete Logarithm (ZK-ATDLOG) validator implementation.

> **Related Documentation:**  
> - [Testing Architecture](./dlognogh_architecture.md) - Understanding the test layers  
> - [Running Benchmarks](./dlognogh.md) - How to run performance benchmarks

## Overview

Regression tests ensure **backwards compatibility** of the ZK-ATDLOG validator by verifying that previously generated token requests remain valid across code changes. These tests use pre-recorded test vectors containing serialized token requests that must continue to validate correctly.

**Location:** `token/core/zkatdlog/nogh/v1/regression/`

## Purpose

- **Backwards Compatibility**: Ensure new code changes don't break validation of existing token requests
- **Protocol Stability**: Verify that the cryptographic protocol remains consistent across versions
- **Change Detection**: Identify when modifications require regenerating test data

## Test Structure

### Test Data Organization

```
testdata/
└── zero/
    ├── 32-BLS12_381_BBS_GURVY/     # 32-bit IPA/bulletproof range proofs, BLS12_381 curve
    │   ├── params.txt              # Base64-encoded public parameters
    │   └── testdata.json           # All test cases for this configuration
    ├── 32-BN254/                   # 32-bit IPA/bulletproof range proofs, BN254 curve
    │   ├── params.txt
    │   └── testdata.json
    ├── 64-BLS12_381_BBS_GURVY/     # 64-bit IPA/bulletproof range proofs, BLS12_381 curve
    │   ├── params.txt
    │   └── testdata.json
    ├── 64-BN254/                   # 64-bit IPA/bulletproof range proofs, BN254 curve
    │   ├── params.txt
    │   └── testdata.json
    ├── csp/                        # Same 4 configurations, CSP range-proof system
    │   ├── 32-BLS12_381_BBS_GURVY/
    │   ├── 32-BN254/
    │   ├── 64-BLS12_381_BBS_GURVY/
    │   └── 64-BN254/
    └── open-policy/                # Same 4 configurations, open issuer policy (empty
        │                           # PP.IssuerIDs); IPA proof type only, since issuer
        │                           # policy is orthogonal to the range-proof system
        ├── 32-BLS12_381_BBS_GURVY/
        ├── 32-BN254/
        ├── 64-BLS12_381_BBS_GURVY/
        └── 64-BN254/
```

### Test Vector Format

Each `testdata.json` file contains all test cases for a configuration with labeled keys:

```json
{
  "transfers_i1_o1_0": {
    "req_raw": "<base64-encoded token request>",
    "txid": "<transaction ID>",
    "metadata": "<base64-encoded metadata>",
    "inputs": [[<serialized-token-bytes>], [...]]
  },
  "transfers_i1_o1_1": { ... },
  ...
  "transfers_i1_o1_63": { ... },
  "transfers_i1_o2_0": { ... },
  ...
  "issues_i1_o1_0": { ... },
  ...
  "redeems_i2_o2_63": { ... },
  "swaps_i2_o2_63": { ... },
  "upgrade_witness_0": { ... },
  "pub_metadata_issue_0": { ... },
  "pub_metadata_transfer_0": { ... },
  "unclaimed_metadata_0": { ... },
  "multi_auditor_0": { ... },
  "extra_signature_0": { ... }
}
```

The `open-policy/` root's `testdata.json` files contain only two keys instead,
`open_policy_issue_0` and `open_policy_redeem_0`, since that PP has no other
scenarios generated against it.

**Key Format:**
- Swept action types: `<action>_i<inputs>_o<outputs>_<index>`
  - `action`: One of `transfers`, `issues`, `redeems`, `swaps`
  - `inputs`: Number of input tokens (1 or 2)
  - `outputs`: Number of output tokens (1 or 2)
  - `index`: Test case number (0-63)
- Fixed-shape scenario fixtures: `<scenario>_<index>` (always `_0`, one fixture per
  configuration) — `upgrade_witness`, `pub_metadata_issue`, `pub_metadata_transfer`,
  `unclaimed_metadata`, `multi_auditor`, `extra_signature`, `open_policy_issue`,
  `open_policy_redeem`

**Fields:**
- `req_raw`: Base64-encoded serialized token request
- `txid`: Transaction ID for the request
- `metadata`: Base64-encoded token request metadata (for auditor validation)
- `inputs`: Nested array of serialized input tokens (for auditor validation)

### Fixed-Shape Scenario Fixtures

Beyond the swept action-type sweep, each configuration also carries a set of
fixed-shape fixtures that exercise validator/auditor branches the sweep never
reaches (single-issuer/single-auditor setup, idemixnym owners only, no public
metadata):

| Key | Exercises | Outcome |
|---|---|---|
| `upgrade_witness_0` | `TransferUpgradeWitnessValidate` (input loaded from a fabtoken-precision `TokenFormat`, auto-upgraded via `ActionInput.UpgradeWitness`) | positive |
| `pub_metadata_issue_0` | `IssueApplicationDataValidate` (`"pub."`-prefixed issue attribute) | positive |
| `pub_metadata_transfer_0` | `TransferApplicationDataValidate` (`"pub."`-prefixed transfer attribute) | positive |
| `multi_auditor_0` | `AuditingSignaturesValidate`'s 1-of-N policy, signed by a second registered auditor key | positive |
| `open_policy_issue_0` / `open_policy_redeem_0` | `IssueValidate` / `TransferSignatureValidate` issuer-membership checks when `PP.Issuers()` is empty (open issuer policy) | positive |
| `unclaimed_metadata_0` | The metadata-counter invariant ("more metadata than those validated") — a transfer attribute key that no validator branch claims | **negative** |
| `extra_signature_0` | `Backend.EnsureExhausted` ("unconsumed signatures") — a token request with one spurious extra signature | **negative** |

Negative fixtures are expected to **fail** `UnmarshallAndVerifyWithMetadata`
with the documented error substring, rather than validate successfully; see
`regression_test.go`'s `negativeFixtureErrorSubstr`.

## Running Regression Tests

### Run All Tests

```bash
cd token/core/zkatdlog/nogh/v1/regression
go test -v
```

### Run Specific Configuration

```bash
# Run only 32-bit BLS12_381 tests
go test -v -run "TestRegression/testdata/32-BLS12_381_BBS_GURVY"

# Run only transfer tests
go test -v -run "TestRegression.*transfers"

# Run specific input/output combination
go test -v -run "TestRegression.*transfers_i2_o2"
```

### Parallel Execution

The tests run in parallel by default. To control parallelism:

```bash
# Run with specific number of parallel tests
go test -v -parallel 4
```

## Test Coverage

The regression suite tests, per range-proof system (IPA/bulletproof and CSP):

- **4 Action Types**: transfers, issues, redeems, swaps
- **4 Input/Output Combinations**: i1_o1, i1_o2, i2_o1, i2_o2
- **4 Configurations**: 2 bit sizes (32, 64) × 2 curves (BLS12_381, BN254)
- **64 Test Cases per Combination**: 64 vectors for each action/input/output combination
- **1,024 Swept Test Cases per Configuration**: 4 actions × 4 combinations × 64 vectors
- **6 Fixed-Shape Scenario Fixtures per Configuration**: `upgrade_witness`,
  `pub_metadata_issue`, `pub_metadata_transfer`, `unclaimed_metadata`, `multi_auditor`,
  `extra_signature` (see [Fixed-Shape Scenario Fixtures](#fixed-shape-scenario-fixtures))
- **Total Test Vectors**: (1,024 + 6) test cases × 4 configs × 2 proof systems, plus
  2 `open_policy_*` fixtures × 4 configs in the separate `open-policy/` root (IPA only)

## Generating New Test Data

When code changes require regenerating test vectors:

### 1. Use the Generator

```bash
cd token/core/zkatdlog/nogh/v1/regression/testdata/zero/generator

# IPA/bulletproof vectors (testdata/zero/<config> and testdata/zero/open-policy/<config>)
go run . -bits=32,64 -curves=BN254,BLS12_381_BBS_GURVY -num_inputs=1,2 -num_outputs=1,2

# CSP vectors (testdata/zero/csp/<config>)
go run . -proof_type=csp -bits=32,64 -curves=BN254,BLS12_381_BBS_GURVY -num_inputs=1,2 -num_outputs=1,2

# Or, equivalently, run both go:generate directives in this package:
go generate .
```

This regenerates every `testdata.json` in each affected configuration directory
(the swept action/input/output cases plus the fixed-shape scenario fixtures, in one
aggregated file per configuration), and — for the IPA run only — the separate
`testdata/zero/open-policy/<config>/testdata.json` files.

### 2. Document the Change

Update `changes.md` with:
- Commit hash where change occurred
- Description of what changed
- Reason for regeneration

Example:
```markdown
## With respect to commit `<commit-hash>`

Description of the change that required test data regeneration.
```

### 3. Commit New Test Data

```bash
git add token/core/zkatdlog/nogh/v1/regression/testdata/
git commit -m "Regenerate regression test data: <reason>"
```

## When to Regenerate Test Data

Regenerate test vectors when:

- **Serialization format changes**: Any modification to how token requests are serialized
- **Cryptographic changes**: Updates to proof generation or verification algorithms
- **Protocol updates**: Changes to the token protocol itself
- **Bug fixes**: Corrections that affect the output format

## Related Tests

- **Layer 3 Service Layer - Transfer Validator Service Benchmarks**: Performance testing of the same validation logic
  - `BenchmarkValidatorTransfer`
  - `TestParallelBenchmarkValidatorTransfer`
- See [dlognogh_architecture.md](./dlognogh_architecture.md) for the complete testing architecture