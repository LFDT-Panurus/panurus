# Validator Resource Limits

This page describes the resource limits enforced on untrusted token requests and actions before
they reach cryptographic verification, the configuration mechanism that controls them, and the
consensus-safety contract that mechanism carries.

## Why limits exist

The token request validators (`token/core/common`, and the fabtoken/zkatdlog drivers built on top
of it) accept raw, attacker-controlled bytes over the network. Aside from the signing anchor
(`driver.MaxAnchorSize`), nothing else bounds the size of the raw request, the number of actions or
signatures, the size of an individual action or signature, the number of inputs/outputs/metadata
entries in an action, the length of a zero-knowledge proof, or how deeply a composite owner identity
nests — unless these limits are enforced. Without them, an attacker could force unbounded allocations
(`make([]..., len(attackerControlledCount))`), unbounded recursion (a composite identity nested
inside itself), and expensive cryptographic work (proof deserialization, ZK verification) purely by
shaping the wire bytes, without needing any valid signature.

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
        maxIdentityDepth: 5
        maxIdentityComponents: 16
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

The FSC/DI runtime (`token/sdk/dig/sdk.go`) registers exactly one `driver.ResourceLimitsProvider` in
the dig container — the config-backed implementation above — and injects it into **both** validator
construction paths, so they always enforce the same configured limits:

- The per-TMS token manager service: each driver's `NewTokenDriver` constructor
  (`token/core/fabtoken/v1/driver/driver.go`, `token/core/zkatdlog/nogh/v1/driver/driver.go`) takes a
  `driver.ResourceLimitsProvider` and resolves it inside `NewTokenService`, immediately before
  constructing that TMS's validator.
- The standalone validator-driver service: `newValidatorDriverService`
  (`token/sdk/dig/providers.go`) takes the same injected `driver.ResourceLimitsProvider` and resolves
  it once when building `core.ValidatorDriverService`.

The resolved `driver.ResourceLimits` flows: provider → (`Driver.NewTokenService` /
`core.NewValidatorDriverService(limits, ...)` → `driver.ValidatorDriver.NewValidator(pp, limits)`) →
the per-driver `common.NewValidator(..., limits, ...)` → `ActionDeserializer.DeserializeActions`,
which calls `action.SetLimits(limits)` on every deserialized action before `Deserialize` runs. Any
action constructed without `SetLimits` (e.g. in tests or other non-validator call sites) falls back
to `DefaultResourceLimits()` via an internal `effectiveLimits()` helper — never more permissive than
the historical behavior.

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

### 3. Composite identity nesting

A token's owner is an identity, and three identity types are *composite* — their components are
themselves identities:

| Type | Registered as | Components |
| --- | --- | --- |
| `multisig` (`token/services/identity/multisig`) | `driver.MultiSigIdentityType` | N component identities, all of which must sign |
| `boolpolicy` (`token/services/identity/boolpolicy`) | `driver.PolicyIdentityType` | N component identities, combined by a boolean expression |
| `htlc` (`token/services/identity/interop/htlc`) | `htlc.ScriptType` | the script's sender and recipient |

Each driver's `NewDeserializer` (`token/core/fabtoken/v1/driver/deserializer.go`,
`token/core/zkatdlog/nogh/v1/driver/deserializer.go`) registers the verifier multiplex as the
*component* deserializer for all three, including for itself:

```go
des := deserializer.NewTypedVerifierDeserializerMultiplex()
...
des.AddTypedVerifierDeserializer(htlc2.ScriptType, htlc.NewTypedIdentityDeserializer(des))
des.AddTypedVerifierDeserializer(multisig.Multisig, multisig.NewTypedIdentityDeserializer(des, des))
des.AddTypedVerifierDeserializer(boolpolicy.Policy, boolpolicy.NewTypedIdentityDeserializer(des, des))
```

That self-registration is what makes composite identities compose — a policy over a multisig over an
x509 identity resolves correctly — and it is also what makes the recursion unbounded without an
explicit budget. `GetOwnerVerifier` is called once per input token from the transfer validator, so an
attacker-shaped owner identity drives that recursion **before any signature is verified**.

Two bounds close it:

| Field | Default | Bounds |
| --- | --- | --- |
| `MaxIdentityDepth` | 5 | How deeply composite identities may nest inside one another |
| `MaxIdentityComponents` | 16 | How many components a single composite identity may carry |

Both are needed. Depth alone does not bound fan-out (one level with thousands of components is a
single recursive step doing unbounded work), and fan-out alone does not bound depth. Real deployments
nest 2–3 levels — a policy over a multisig over x509 — comfortably inside the default.

The depth budget is carried in `context.Context` (`token/driver/identity_nesting.go`), which is
already the first parameter of every method in the recursion. `driver.EnterCompositeIdentity(ctx)`
accounts for one level and returns the context to pass to the components; it returns an error
wrapping `driver.ErrIdentityNestingTooDeep` past the limit. Exceeding `MaxIdentityComponents` returns
`driver.ErrTooManyIdentityComponents`. Because the count rides in the context, it is **per-path**:
sibling components each descend from their parent's depth rather than sharing a running total, which
is the correct semantics for a depth bound and the reason the fan-out bound is required alongside it.

There are four independent recursion chains, each of which accounts for its own depth — the matcher
paths recurse separately from the deserialization that produced them, so a budget spent during
construction must not be inherited at match time:

| Chain | Entry point |
| --- | --- |
| Verifier deserialization | `TypedIdentityDeserializer.DeserializeVerifier` in all three packages |
| Matcher construction | `TypedIdentityDeserializer.GetAuditInfoMatcher` (`multisig`, `boolpolicy`) |
| Matcher evaluation | `InfoMatcher.Match` (`multisig`, `boolpolicy`), `AuditInfoMatcher.Match` (`htlc`) |
| Audit-info collection | `TypedIdentityDeserializer.GetAuditInfo` in all three packages |

Validators seed the configured limits into the context at each public entry point
(`(*Validator).withIdentityNestingLimits`, `token/core/common/limits.go`). Composite identity
deserialization is also reachable from paths that carry no `ResourceLimits` — a wallet resolving a
recipient, an auditor inspecting a request, tests — and those **still get the defaults** rather than
running unbounded, so a seeding site added later and forgotten weakens the bound to the default
instead of disabling it.

The fan-out bound is applied on every chain above that walks a component list — verifier
deserialization, matcher construction and audit-info collection — through the same
`validateComponentIdentities` choke point, which also rejects an empty or duplicated component. The
matcher-evaluation chain needs no separate check: its component count is fixed by the matcher tree
built during construction, which was already bounded.

The fan-out bound is also applied on the honest-caller path, in `multisig.WrapIdentities` and
`boolpolicy.WrapPolicyIdentity`, so an identity constructed in-process cannot exceed what a validator
will later accept.

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
- **Identity-nesting tests**: `nesting_test.go` in `multisig` and `boolpolicy` covers each of the
  four recursion chains at `limit` and `limit+1`, that the depth counter is per-path rather than
  global, and that an unseeded context is still bounded by the defaults.
  `deserializer_nesting_test.go` (`token/core/fabtoken/v1/driver`) proves the same against the real
  assembled multiplex, including composite types alternating with each other so that no type gets a
  fresh budget, and asserts a realistic three-level identity is *not* rejected as over-nested.
- **Fuzzing**: `common.FuzzRequestResourceLimits`, `zkatdlog validator.FuzzActionResourceLimits`,
  and `fabtoken validator.FuzzActionResourceLimits` fuzz requests/actions shaped directly by their
  resource dimensions (counts and byte lengths) against `DefaultResourceLimits()`, asserting no
  panic and the expected typed error at every boundary. `fabtoken driver.FuzzOwnerVerifierNoPanic`
  additionally fuzzes the owner-identity deserialization path itself — the one reached from the
  transfer validator once per input token — seeded with identities nested from 1 to 600 levels deep.
  The three resource-dimension targets each have a persisted seed corpus under their package's
  `testdata/fuzz/<TargetName>/` covering every default's boundary; `FuzzOwnerVerifierNoPanic` seeds
  from code only, since its interesting shapes are generated (nesting depth) rather than enumerated.
  All of them run nightly via
  [`.github/workflows/nightly-fuzz.yml`](../../.github/workflows/nightly-fuzz.yml).
