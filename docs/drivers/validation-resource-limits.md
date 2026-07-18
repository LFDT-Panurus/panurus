# Validator Resource Limits

This page describes the resource limits enforced on untrusted token requests and actions before
they reach cryptographic verification, and the consensus-safety contract those limits carry.

## Why limits exist

The token request validators (`token/core/common`, and the fabtoken/zkatdlog drivers built on top
of it) accept raw, attacker-controlled bytes over the network. Before these limits were
introduced, only the signing anchor had an explicit size bound (`driver.MaxAnchorSize`). Nothing
else bounded the size of the raw request, the number of actions or signatures, the size of an
individual action or signature, the number of inputs/outputs/metadata entries in an action, or the
length of a zero-knowledge proof. An attacker could force unbounded allocations
(`make([]..., len(attackerControlledCount))`) and expensive cryptographic work (proof
deserialization, ZK verification) purely by shaping the wire bytes, without needing any valid
signature.

## Consensus-safety contract

Every validating peer must reject or accept the same request identically, or endorsement becomes
nondeterministic. To guarantee this, all limits are **baked-in Go package constants**, not fields
read from `PublicParameters` or any other network-supplied or runtime-configurable value. Two
peers running the same binary version always apply the same limits, independent of what a request
or its public parameters claim.

**Changing any constant listed below is a breaking protocol change.** It must be coordinated as a
version upgrade across every validating peer, the same way changing `driver.MaxAnchorSize` would
be. Do not make these values configurable per-deployment or derive them from `PublicParameters`.

## Enforcement points

Limits are enforced at two boundaries, both strictly before the request or action is used to
allocate proportional memory or is handed to a cryptographic verifier:

### 1. Common request envelope (`token/core/common/limits.go`)

Enforced in `VerifyTokenRequestFromRaw` (`token/core/common/validator.go`):

| Constant | Value | Checked | Enforced by |
| --- | --- | --- | --- |
| `MaxRequestBytes` | 256 KiB | Raw serialized request size | `CheckRawRequestSize`, before `TokenRequest.FromBytes` |
| `MaxActions` | 256 | Number of actions in the request | `CheckRequestLimits`, immediately after parsing |
| `MaxSignatures` | 4096 | Number of request signatures | `CheckRequestLimits`, immediately after parsing |
| `MaxActionBytes` | 256 KiB | Length of a single action's raw bytes | `CheckRequestLimits`, immediately after parsing |
| `MaxSignatureBytes` | 4 KiB | Length of a single auditor or action signature | `CheckRequestLimits`, immediately after parsing |

`CheckRawRequestSize` runs before the protobuf decode, so an oversized message never reaches an
allocation proportional to its own claimed size. `CheckRequestLimits` runs on the parsed request,
before `MarshalToMessageToSign` and before any signature verification, so oversized or
over-counted requests never reach cryptographic work. Violations return a typed error
(`ErrRequestTooLarge`, `ErrTooManyActions`, `ErrTooManySignatures`, `ErrActionTooLarge`,
`ErrSignatureTooLarge`).

### 2. Driver-specific action internals

Each driver bounds the shape of its own action payload, checked inside `Deserialize` (before the
proportional-size allocations for inputs/outputs) and `Validate` (before proof-specific
cryptographic work):

**ZKAT-DLOG NOGH v1** (`token/core/zkatdlog/nogh/v1/issue/limits.go`,
`.../transfer/limits.go` — identical constants for issue and transfer actions):

| Constant | Value |
| --- | --- |
| `MaxInputs` | 256 |
| `MaxOutputs` | 256 |
| `MaxMetadataEntries` | 64 |
| `MaxMetadataKeyBytes` | 256 |
| `MaxMetadataValueBytes` | 4 KiB |
| `MaxProofBytes` | 128 KiB |

`MaxProofBytes` is checked before the zero-knowledge proof body is handed to the bulletproof/CSP
verifier for deserialization, so an oversized proof is rejected without running any ZK-specific
cryptographic code.

**FabToken v1** (`token/core/fabtoken/v1/actions/limits.go` — fabtoken has no ZK proof, so there is
no `MaxProofBytes`):

| Constant | Value |
| --- | --- |
| `MaxInputs` | 256 |
| `MaxOutputs` | 256 |
| `MaxMetadataEntries` | 64 |
| `MaxMetadataKeyBytes` | 256 |
| `MaxMetadataValueBytes` | 4 KiB |

Each driver-level violation returns its own typed error (e.g. `ErrTooManyInputs`,
`ErrProofTooLarge`), defined alongside the constant it enforces.

## Choosing and changing these values

The current values are conservative but comfortably above real usage observed across the unit,
regression, and integration test suites — no currently-valid request or action is rejected by any
of these limits. If a legitimate use case needs a higher limit:

1. Confirm no currently-valid production traffic pattern needs a value close to the existing
   limit, to avoid an unnecessarily invasive change.
2. Treat the change as a coordinated protocol upgrade: every validating peer must adopt the new
   constant in the same release before any peer accepts a request that only the new limit permits.
3. Update the exact-boundary unit tests (`limit-1`/`limit`/`limit+1`) and the fuzz seed corpus
   (`testdata/fuzz/<TargetName>/`) alongside the constant.

## Testing

- **Exact-boundary unit tests**: every constant has a table-driven test asserting `limit-1` and
  `limit` succeed and `limit+1` fails with the specific typed error (`limits_test.go` next to each
  `limits.go`).
- **Reject-before-cryptographic-work tests**: `RejectsBeforeCryptographicWork` tests assert an
  oversized proof is rejected in well under 50ms — i.e. before any verifier is constructed. This is
  a timing property, verified as a plain (non-fuzzed) unit test so it isn't subject to fuzz-worker
  CPU contention.
- **Fuzzing**: `common.FuzzRequestResourceLimits`, `zkatdlog validator.FuzzActionResourceLimits`,
  and `fabtoken validator.FuzzActionResourceLimits` fuzz requests/actions shaped directly by their
  resource dimensions (counts and byte lengths), asserting no panic and the expected typed error
  at every boundary. Each target has a persisted seed corpus under its package's
  `testdata/fuzz/<TargetName>/` covering every constant's boundary, and runs nightly via
  [`.github/workflows/nightly-fuzz.yml`](../../.github/workflows/nightly-fuzz.yml).
