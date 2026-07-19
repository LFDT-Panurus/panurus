# Validator Resource Limits

This page describes the resource limits enforced on untrusted token requests and actions before
they reach cryptographic verification, the configuration mechanism that controls them, and the
consensus-safety contract that mechanism carries.

## Why limits exist

The token request validators (`token/core/common`, and the fabtoken/zkatdlog drivers built on top
of it) accept raw, attacker-controlled bytes over the network. Aside from the signing anchor
(`driver.MaxAnchorSize`), nothing else bounds the size of the raw request, the number of actions or
signatures, the size of an individual action or signature, the number of inputs/outputs/metadata
entries in an action, or the length of a zero-knowledge proof — unless these limits are enforced.
Without them, an attacker could force unbounded allocations
(`make([]..., len(attackerControlledCount))`) and expensive cryptographic work (proof
deserialization, ZK verification) purely by shaping the wire bytes, without needing any valid
signature.

## Configuration mechanism

Limits are held in a single struct, `driver.ResourceLimits` (`token/driver/limits.go`), injected
into every validator at construction time — the validator itself never reads a package constant.
`driver.DefaultResourceLimits()` returns the historical, always-safe values (see the tables below);
`driver.ResourceLimits.WithDefaults()` overlays those defaults onto any zero-valued field, so a
partially-specified override never silently disables a limit by leaving it at zero.

Two sources resolve a `driver.ResourceLimits` value at composition-root time, both implementing
`driver.ResourceLimitsProvider`:

- **Config-backed** (`token/services/config.ResourceLimitsProvider`) — used by the FSC/DI runtime
  (`token/sdk/dig/providers.go`). Reads the process-wide key `token.validation.limits` via the
  configuration service and overlays `DefaultResourceLimits()` onto any field left unset:

  ```yaml
  token:
    validation:
      limits:
        maxActions: 128
        maxProofBytes: 65536
  ```

  Every field is optional; an entirely absent `token.validation.limits` key resolves to
  `DefaultResourceLimits()` unchanged.

- **Env-backed** (`token/services/network/fabric/tcc.EnvResourceLimitsProvider`) — used by the
  standalone Fabric chaincode process (`token/services/network/fabric/tcc/main/main.go`), which has
  no configuration service wired. Reads `TOKEN_VALIDATION_MAX_*` environment variables (e.g.
  `TOKEN_VALIDATION_MAX_ACTIONS`), applying the same unset-field-defaults overlay.

A `driver.StaticResourceLimits` provider (a trivial wrapper returning a fixed value) is used by
tests, tools, and any caller that only needs the defaults (e.g.
`cmd/token_validation_service`, the zkatdlog regression suite).

The resolved `driver.ResourceLimits` flows: composition root → `core.NewValidatorDriverService(limits, ...)`
→ `driver.ValidatorDriver.NewValidator(pp, limits)` → the per-driver `common.NewValidator(..., limits, ...)`
→ `ActionDeserializer.DeserializeActions`, which calls `action.SetLimits(limits)` on every
deserialized action before `Deserialize` runs. Any action constructed without `SetLimits` (e.g. in
tests or other non-validator call sites) falls back to `DefaultResourceLimits()` via an internal
`effectiveLimits()` helper — never more permissive than the historical behavior.

## Consensus-safety contract

Every validating peer must reject or accept the same request identically, or endorsement becomes
nondeterministic. Limits are no longer baked-in constants — they are configurable — which shifts
the uniformity guarantee from "guaranteed by the binary" to **an explicit operator
responsibility**:

- The out-of-the-box defaults (`DefaultResourceLimits()`) are safe and identical across every peer
  that does not override them; deployments that never touch `token.validation.limits` or
  `TOKEN_VALIDATION_MAX_*` keep the historical, always-consistent behavior.
- **If you override any limit, every peer validating the same channel/namespace MUST be configured
  with the identical `ResourceLimits` value.** A peer with a looser `maxActions` will accept
  requests that a peer with the default (or a stricter) value rejects, silently breaking
  endorsement determinism — this will not surface as an error until peers disagree on a
  transaction's validity.
- Treat a limits change the same way you would treat a `driver.MaxAnchorSize` change: roll it out
  as a coordinated configuration change across every validating peer (and the chaincode process, if
  it enforces limits independently) before any peer relies on the new value.

## Enforcement points

Limits are enforced at two boundaries, both strictly before the request or action is used to
allocate proportional memory or is handed to a cryptographic verifier:

### 1. Common request envelope (`token/core/common/limits.go`)

Enforced by `(*Validator).CheckRawRequestSize` / `CheckRequestLimits`
(`token/core/common/validator.go`), reading the validator's injected `Limits` field:

| Field | Default | Checked | Enforced by |
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
`ErrSignatureTooLarge`), wrapping the effective (possibly configured) limit value.

### 2. Driver-specific action internals

Each driver bounds the shape of its own action payload, checked inside `Deserialize` (before the
proportional-size allocations for inputs/outputs) and `Validate` (before proof-specific
cryptographic work), using the action's `effectiveLimits()` (the limits injected via `SetLimits`,
or `DefaultResourceLimits()` if none were set):

**ZKAT-DLOG NOGH v1** (`token/core/zkatdlog/nogh/v1/issue/limits.go`,
`.../transfer/limits.go` — identical field defaults for issue and transfer actions):

| Field | Default |
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

| Field | Default |
| --- | --- |
| `MaxInputs` | 256 |
| `MaxOutputs` | 256 |
| `MaxMetadataEntries` | 64 |
| `MaxMetadataKeyBytes` | 256 |
| `MaxMetadataValueBytes` | 4 KiB |

Each driver-level violation returns its own typed error (e.g. `ErrTooManyInputs`,
`ErrProofTooLarge`), wrapping the effective limit at check time.

Auditor-side deserializers (`.../audit/auditor.go` in both drivers) are not the
consensus-endorsement boundary and are unaffected by this configuration mechanism — they always
run with `DefaultResourceLimits()`.

## Choosing and changing these values

The default values are conservative but comfortably above real usage observed across the unit,
regression, and integration test suites — no currently-valid request or action is rejected by any
of the defaults. If a deployment needs a different limit:

1. Confirm no currently-valid production traffic pattern needs a value close to the existing
   limit, to avoid an unnecessarily invasive change.
2. Roll the configuration change out to every validating peer (and the chaincode process) before
   relying on it — see [Consensus-safety contract](#consensus-safety-contract) above.
3. If you are changing a *default* (not just deploying an override), update the exact-boundary unit
   tests (`limit-1`/`limit`/`limit+1`) and the fuzz seed corpus (`testdata/fuzz/<TargetName>/`)
   alongside `DefaultResourceLimits()`.

## Testing

- **Exact-boundary unit tests**: every field has a table-driven test asserting `limit-1` and
  `limit` succeed and `limit+1` fails with the specific typed error, both against
  `DefaultResourceLimits()` and against an injected custom override (`limits_test.go` next to each
  `limits.go`), proving overrides actually take effect and are not just read-only documentation.
- **Provider tests**: the config-backed and env-backed providers each have tests covering an unset
  source (resolves to defaults), a partial override (unset fields still default), and an
  invalid/unparseable value (returns an error).
- **Wiring test**: `TestValidatorDriverService_ForwardsConfiguredLimits`
  (`token/core/service_test.go`) asserts the exact `ResourceLimits` value passed into
  `NewValidatorDriverService` is the one forwarded to the driver's `NewValidator`, end to end.
- **Reject-before-cryptographic-work tests**: `RejectsBeforeCryptographicWork` tests assert an
  oversized proof is rejected in well under 50ms — i.e. before any verifier is constructed. This is
  a timing property, verified as a plain (non-fuzzed) unit test so it isn't subject to fuzz-worker
  CPU contention.
- **Fuzzing**: `common.FuzzRequestResourceLimits`, `zkatdlog validator.FuzzActionResourceLimits`,
  and `fabtoken validator.FuzzActionResourceLimits` fuzz requests/actions shaped directly by their
  resource dimensions (counts and byte lengths) against `DefaultResourceLimits()`, asserting no
  panic and the expected typed error at every boundary. Each target has a persisted seed corpus
  under its package's `testdata/fuzz/<TargetName>/` covering every default's boundary, and runs
  nightly via [`.github/workflows/nightly-fuzz.yml`](../../.github/workflows/nightly-fuzz.yml).
