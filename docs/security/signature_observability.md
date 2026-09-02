# Signature Observability and Throttling

## Overview

Every signature the node produces or checks goes through two services: the identity
provider (which resolves *signers*) and the deserializer (which resolves *verifiers*).
Before this feature they were silent — a caller hammering `GetSigner`, or feeding a
stream of forged signatures to `OwnerVerifier`, looked exactly like healthy traffic, and
the only evidence was CPU time.

Panurus now:

1. **Instruments** both services. Every operation produces one event — what was
   done, for which principal, with what outcome, and how long it took.
2. **Exports** those events as Prometheus metrics and as a privacy-safe audit log.
3. **Escalates** automatically: a principal whose request rate, error ratio, or
   invalid-signature ratio crosses a threshold is moved to a reduced quota and, if it
   keeps going, blocked for a while.

Instrumentation is always on and costs a no-op call when nothing is wired to it. The
throttle policy defaults to **monitor** — it evaluates and reports but never denies —
so enabling enforcement is a deliberate decision.

## Principals

Every event is attributed to a **principal**: the identity hash,
`driver.Identity.UniqueID()`, of the identity the operation was performed for.

Raw identity bytes are never used as a metric label, never logged, and never used as a
throttle key. The hash is stable, it is already the signer cache key, and it keeps
identity material (which for X.509 identities includes the certificate) out of logs and
out of a metrics database.

Operations that cannot be attributed to a single identity — `AreMe` over a batch, for
instance — are reported with an empty principal and are never throttled. Charging a
batch to one of its members would let unrelated callers throttle each other.

## Where the gate applies

**Instrumentation is installed everywhere. The gate is consulted at exactly one place:
`token.SignatureService`, the client-facing entry point.**

This is a deliberate asymmetry. `GetOwnerVerifier` and friends are also called by driver
*validators*, while validating a transaction. Denying a verifier resolution there would
make the validity of a transaction depend on the local call history of whichever node
happened to validate it — two nodes would disagree about the same transaction. So the
validators' deserializers are instrumented (their events feed the metrics and the
policy) but never gated.

`AreMe` and `IsMe` are not gated either, even at the client boundary: they answer a
question about local state and have no way to express "refused". Returning `false` for an
identity that is in fact ours would be a wrong answer, not a denial.

`AuditorVerifier` and `GetSigner` are also **not gated**, even though they are
client-facing, for a different reason: the identities they receive come from trusted
fixed sources that are not attacker-controlled.

- `AuditorVerifier` is called with auditor identities taken from the public parameters
  of the token system. That set is tiny and fixed per deployment. Charging every
  transaction to one of those buckets would make `DefaultRate` a hard ceiling on
  transaction throughput for the node.
- `GetSigner` is called on the hot endorsement path with the node's own long-term signing
  identity. All endorsements on that node would share one bucket, so the 200 ops/s
  default would be a global endorsement rate limit.

Both operations are still instrumented downstream (in the deserializer and the identity
provider respectively), so their events feed the metrics and the audit log. Only the
gate is bypassed.

Callers of `AuditorVerifier` must still be prepared for the error sentinel returned by a
gated downstream: if the signature service ever returns `token.SignatureThrottled` — for
example from a gated verifier inside the deserializer — callers should propagate it
distinctly rather than folding it into a generic "failed verifying signature" error.

## Metrics

All metrics are TMS-scoped: the `network`, `channel` and `namespace` labels are added by
the TMS metrics provider, ahead of the labels listed below.

| Metric | Type | Labels | What it tells you |
|--------|------|--------|-------------------|
| `identity_signature_operations_total` | counter | `op`, `role`, `outcome` | The main series. A rising `outcome="invalid"` on `op="verify"`, or a rising `outcome="throttled"`, is a misbehaving caller. |
| `identity_signature_operation_duration_seconds` | histogram | `op` | Latency per operation. Signature work is CPU-bound, so this is where a resource-exhaustion attempt shows up. |
| `identity_signer_cache_lookups_total` | counter | `result` (`hit`/`miss`) | A collapsing hit ratio means signer material is re-derived on every call. |
| `identity_throttle_escalations_total` | counter | `level`, `reason` | Every level change, including de-escalations back to `normal`. |
| `identity_throttled_principals` | gauge | `level` | How many principals are currently at `soft` / `blocked`. |
| `identity_signer_resolutions_total` | counter | `outcome` (`cache`/`routed`/`fallback`) | How signers are being obtained. |
| `identity_get_signer_duration_seconds` | histogram | `path` | Latency of signer resolution per path. |

`op` is one of `get_signer`, `register_signer`, `register_identity_descriptor`, `is_me`,
`get_audit_info`, `bind`, `owner_verifier`, `issuer_verifier`, `auditor_verifier`,
`sign`, `verify`, `escalation`. `role` is `owner`, `issuer`, `auditor` or `unknown`.
`outcome` is `ok`, `error`, `invalid` or `throttled`.

`invalid` and `error` are deliberately separate. A `Verifier` that returns an error has
*rejected a signature*; a `GetSigner` that returns an error hit a missing signer or a
storage failure. The first is a security signal, the second is an operational one.

Cardinality is bounded by those closed sets. No metric carries a per-identity label,
since the number of identities a deployment sees is unbounded.

### Suggested alerts

```promql
# A principal is presenting rejected signatures.
rate(identity_signature_operations_total{op="verify",outcome="invalid"}[5m]) > 1

# The policy is actively refusing work.
identity_throttled_principals{level="blocked"} > 0

# Enforcement is on and someone is hitting it.
rate(identity_signature_operations_total{outcome="throttled"}[5m]) > 0
```

## The audit log

Alongside the metrics, each event is written as a single structured line to the
`panurus.driver.<driver>.signature` logger:

```
sig-audit op=get_signer principal=abcd1234 role=owner outcome=ok path=cache cache=hit duration_ms=1.500
sig-audit op=verify principal=abcd1234 role=owner outcome=invalid duration_ms=0.412 err=[signature mismatch]
sig-audit op=escalation principal=abcd1234 role=unknown outcome=ok level=blocked reason=invalid_signature_rate
```

Log level follows the outcome, so a deployment can keep the interesting lines without
the volume of the routine ones:

| Outcome | Level |
|---------|-------|
| `ok` | debug |
| `error`, `invalid`, `throttled` | warn |
| `escalation` (policy state) | info |

Fields that do not apply are omitted; an unattributed operation is written as
`principal=none`. As with the metrics, the `principal` field is an identity hash — the
audit log never contains identity bytes.

## The escalation policy

The policy keeps, per principal, a token bucket and a sliding window (one minute by
default, in ten-second steps) of operation counts. A principal moves up a level when:

- it exhausts its token bucket (`reason=rate`), or
- the fraction of its operations that failed crosses `errorRateThreshold`
  (`reason=error_rate`), or
- the fraction of its verifications that were rejected crosses
  `invalidSignatureRateThreshold` (`reason=invalid_signature_rate`).

Ratios are only evaluated once the window holds at least `minSamples` observations: one
failure out of three calls is not an attack.

The levels are:

| Level | Effect |
|-------|--------|
| `normal` | Full quota. |
| `soft` | Rate and burst multiplied by `quotaReductionFactor`, for at least `softDuration`. A soft-limited principal is slowed, never stopped: the reduced bucket always keeps room for one token. |
| `blocked` | Metered operations refused for `blockDuration`, then released back to `soft` (`reason=block_expired`). |

A principal that goes `deescalateAfter` without a violation is restored one level at a
time (`reason=quiet_period`). Each transition resets the window, so the counters that
caused an escalation cannot immediately trigger the next one. Per-principal state is
dropped after `idleTTL` of inactivity — except for principals above `normal`, whose
state *is* the record that they are throttled.

## Handling a denial

A denied operation returns an error wrapping the `token.SignatureThrottled` sentinel.
Callers should treat it as "back off", distinct from "unknown identity" or "invalid
signature":

```go
signer, err := signatureService.GetSigner(ctx, id)
if errors.Is(err, token.SignatureThrottled) {
    // back off and retry later, shed the request, or surface a 429-style response
}
```

## Configuration

The policy is read per TMS from `token.tms.<name>.identity.throttle`. The whole section
is optional; a missing section means the defaults below.

```yaml
token:
  tms:
    <name>:
      identity:
        throttle:
          mode: monitor
          rate: 200
          burst: 400
          window: 1m
          minSamples: 50
          errorRateThreshold: 0.5
          invalidSignatureRateThreshold: 0.2
          quotaReductionFactor: 0.25
          softDuration: 5m
          blockDuration: 1m
          deescalateAfter: 5m
          idleTTL: 10m
```

| Field | Default | Description |
|-------|---------|-------------|
| `mode` | `monitor` | `off` — nothing metered, nothing denied. `monitor` — evaluate and report, never deny. `enforce` — deny throttled principals. |
| `rate` | `200` | Metered signature operations per second per principal. A negative value disables the policy, like `off`. |
| `burst` | `400` | Bucket capacity, absorbing short spikes without raising the sustained rate. Values below `rate` are raised to `rate`. |
| `window` | `1m` | Evaluation period for the ratio thresholds. |
| `minSamples` | `50` | Minimum observations in a window before a ratio can escalate a principal. |
| `errorRateThreshold` | `0.5` | Failing-operation fraction that escalates. A value greater than `1` disables this trigger. |
| `invalidSignatureRateThreshold` | `0.2` | Rejected-verification fraction that escalates. Stricter than the error threshold: a healthy caller does not present bad signatures. |
| `quotaReductionFactor` | `0.25` | Multiplier applied to `rate` and `burst` at level `soft`. Must be in `(0,1]`. |
| `softDuration` | `5m` | Minimum time on a reduced quota. |
| `blockDuration` | `1m` | How long a blocked principal is refused before release back to `soft`. |
| `deescalateAfter` | `5m` | Violation-free period required to restore a level. |
| `idleTTL` | `10m` | How long per-principal state is kept after its last operation. |

Out-of-range values are rejected at startup rather than clamped: a configuration asking
for a `quotaReductionFactor` of `3` has a mistake in it, and silently treating it as `1`
would leave the operator believing a policy is in force that is not.

### Rolling out enforcement

1. Deploy with the default `monitor` mode.
2. Watch `identity_throttle_escalations_total` and the audit log's `op=escalation`
   lines. In monitor mode these are exactly the denials that `enforce` would have made.
3. Adjust `rate`, `burst` and the two thresholds until only traffic you would want to
   refuse escalates.
4. Switch `mode` to `enforce`.

## Implementation notes

For contributors:

- `token/services/identity/sigobserve` — the event vocabulary (`Event`, `Op`, `Outcome`,
  `Observer`, `Gate`), the `Timer` that times an operation without allocating, the
  `InstrumentSigner`/`InstrumentVerifier` decorators, and the audit logger. It depends on
  nothing but `token/driver`, which is what lets `token` import it without a cycle.
- `token/services/identity/throttle` — the escalation policy. It is both an `Observer`
  (it watches outcomes) and a `Gate` (it decides).
- `token/services/ratelimit` — the per-key token buckets the policy meters with.
- `token/services/identity/sigpolicy` — assembles metrics + audit log + policy into one
  `Stack`, so the wiring lives in one place instead of in every driver.
- `token/services/identity/metrics.go` — the Prometheus sink. Every metric must declare
  `network`, `channel`, `namespace` as its leading labels; the TMS provider prepends
  those values, and a metric that omits them makes Prometheus reject the series.
