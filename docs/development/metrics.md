# Metrics Reference

This page lists every metric Panurus registers, with the **exact name Prometheus exports**, so a name
can be copied straight into a Prometheus query or a Grafana panel.

The list is not maintained by hand. `token/services/metricsdoc` instantiates every metrics constructor
in the SDK the way production wiring does, reads the resulting names back out of a Prometheus registry,
and compares them against `token/services/metricsdoc/testdata/metrics.golden` and against this page.
Adding, renaming or moving a metric fails that test until this page is updated. Because a name also
depends on *which* provider production hands the constructor, the same test pins that wiring: it
asserts that the token drivers are the only place a TMS-scoped provider is built, so dropping or
relocating the wrapper fails too rather than silently invalidating the names below.

See [Monitoring](./monitoring.md) for the wider monitoring setup and [Driver Metrics](../drivers/metrics.md)
for how the driver instrumentation is wired. Every metric below already has a panel in the importable
[Grafana overview dashboard](../monitoring/grafana/README.md), whose queries are checked against this
same list.

## How a metric name is built

Panurus code never writes the exported name. It supplies only the bare `Name` field:

```go
p.NewCounter(metrics.CounterOpts{
    Name: "endorsed_transactions",
    Help: "The number of endorsed transactions.",
    LabelNames: []string{"network", "channel", "namespace"},
})
```

FSC's Prometheus provider fills in the missing `Namespace` and `Subsystem` from **the Go package of the
caller** (`platform/view/services/metrics/prometheus/provider.go`), then joins the three parts:

1. The caller's import path is split on `/` — the last segment becomes the **subsystem**, everything
   before it, joined with `_`, becomes the **namespace**.
2. Registered replacers rewrite the namespace. Panurus registers
   `github.com_LFDT-Panurus_panurus_token` → `panurus` in `token/services/logging/init.go`, so every
   metric of this repository is prefixed `panurus_`.
3. The exported name is `<namespace>_<subsystem>_<name>`.

So `endorsed_transactions`, created from `token/services/ttx`, is exported as:

```
github.com/LFDT-Panurus/panurus/token/services/ttx  →  namespace panurus_services, subsystem ttx
                                                    →  panurus_services_ttx_endorsed_transactions
```

Two consequences are worth internalising before writing a query or a dashboard:

- **The bare name in the source is never queryable.** A dashboard built from `Name:` fields alone
  matches nothing — this is what made the first attempt at this documentation unusable.
- **Some names stutter**, because the metric name repeats its package: hence
  `panurus_services_auditor_auditor_audit_duration_seconds` and
  `panurus_services_network_fabricx_finality_queue_finality_queue_pending_events`. They are correct as
  listed; see [Coverage gaps](#metric-hygiene) for why they are not renamed here.

### The TMS-scoped provider changes the prefix

`metrics.NewTMSProvider` (`token/core/common/metrics/provider.go`) wraps a provider so that every metric
it creates is bound to fixed `network`/`channel`/`namespace` label values. Because the wrapper calls the
underlying provider on the caller's behalf, **Prometheus attributes the metric to the wrapper's package**,
`token/core/common/metrics`, and not to the package that declared it.

Every metric created through a TMS-scoped provider is therefore exported under
`panurus_core_common_metrics_`, whatever its source file. The clearest example is `cache_level`: it is
declared in `token/services/identity/idemix/cache/metrics.go`, yet exported as
`panurus_core_common_metrics_cache_level`.

The wiring that decides this is the driver setup in
`token/core/{fabtoken/v1,zkatdlog/nogh/v1}/driver/driver.go`, which builds a TMS-scoped provider and
passes it to the driver services and the wallet service. Everything reached from the
dependency-injection container instead keeps its own package prefix.

Two rules follow for anyone adding a metric:

- Any `CounterOpts`/`GaugeOpts`/`HistogramOpts` passed to a TMS-scoped provider **must declare
  `network`, `channel`, `namespace` in `LabelNames`**, even though the caller never supplies values for
  them. Omitting them panics at runtime with "inconsistent label cardinality" the first time the metric
  is used — not at registration, so it survives a smoke test.
- Check which provider your constructor receives before predicting the name. If in doubt, add the
  constructor to `token/services/metricsdoc` and read the name out of the golden file.

## Driver services

Recorded by the decorator wrappers in `token/core/common/metrics/`, which time every driver service
call. All are TMS-scoped, so `network`, `channel` and `namespace` are always present, and `method`
carries the interface method that was invoked. See [Driver Metrics](../drivers/metrics.md) for the
method lists.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_core_common_metrics_issue_service_operations_total` | counter | `network,channel,namespace,method` | Total number of IssueService method invocations |
| `panurus_core_common_metrics_issue_service_duration_seconds` | histogram | `network,channel,namespace,method` | Duration of IssueService method calls in seconds |
| `panurus_core_common_metrics_issue_service_errors_total` | counter | `network,channel,namespace,method` | Total number of IssueService method errors |
| `panurus_core_common_metrics_transfer_service_operations_total` | counter | `network,channel,namespace,method` | Total number of TransferService method invocations |
| `panurus_core_common_metrics_transfer_service_duration_seconds` | histogram | `network,channel,namespace,method` | Duration of TransferService method calls in seconds |
| `panurus_core_common_metrics_transfer_service_errors_total` | counter | `network,channel,namespace,method` | Total number of TransferService method errors |
| `panurus_core_common_metrics_auditor_service_operations_total` | counter | `network,channel,namespace,method` | Total number of AuditorService method invocations |
| `panurus_core_common_metrics_auditor_service_duration_seconds` | histogram | `network,channel,namespace,method` | Duration of AuditorService method calls in seconds |
| `panurus_core_common_metrics_auditor_service_errors_total` | counter | `network,channel,namespace,method` | Total number of AuditorService method errors |
| `panurus_core_common_metrics_tokens_service_operations_total` | counter | `network,channel,namespace,method` | Total number of TokensService method invocations |
| `panurus_core_common_metrics_tokens_service_duration_seconds` | histogram | `network,channel,namespace,method` | Duration of TokensService method calls in seconds |
| `panurus_core_common_metrics_tokens_service_errors_total` | counter | `network,channel,namespace,method` | Total number of TokensService method errors |
| `panurus_core_common_metrics_tokens_upgrade_service_operations_total` | counter | `network,channel,namespace,method` | Total number of TokensUpgradeService method invocations |
| `panurus_core_common_metrics_tokens_upgrade_service_duration_seconds` | histogram | `network,channel,namespace,method` | Duration of TokensUpgradeService method calls in seconds |
| `panurus_core_common_metrics_tokens_upgrade_service_errors_total` | counter | `network,channel,namespace,method` | Total number of TokensUpgradeService method errors |

Source: `token/core/common/metrics/{issue,transfer,auditor,tokens,upgrade}.go`.

## Transaction lifecycle (ttx)

The `ttx` service orchestrates a token transaction from assembly to commit. These counters and
histograms mark the phases an operator cares about; they are the closest thing the SDK has to
throughput and latency of the business flow.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_ttx_endorsed_transactions` | counter | `network,channel,namespace` | The number of endorsed transactions. |
| `panurus_services_ttx_audit_approved_transactions` | counter | `network,channel,namespace` | The number of approved transactions by the auditor. |
| `panurus_services_ttx_accepted_transactions` | counter | `network,channel,namespace` | The number of accepted transactions. |
| `panurus_services_ttx_endorsement_duration_seconds` | histogram | `network,channel,namespace` | Duration of the full endorsement collection phase including signatures, audit, and chaincode approval. |
| `panurus_services_ttx_audit_approval_duration_seconds` | histogram | `network,channel,namespace` | Duration of the auditor approval phase including validation, append, and signing. |
| `panurus_services_ttx_ordering_duration_seconds` | histogram | `network,channel,namespace` | Duration of the transaction broadcast to the ordering service. |

Source: `token/services/ttx/metrics.go`. Wired in `token/sdk/dig/sdk.go`.

## Finality listener

Recorded when a transaction's ledger status resolves. `hash_mismatch_total` is the one to alert on: a
non-zero value means a committed token-request hash did not match the locally stored one, which is a
data-integrity violation rather than an ordinary failure.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_ttx_finality_finality_listener_confirmed_total` | counter | — | Total number of transactions confirmed on the ledger and successfully committed to local storage |
| `panurus_services_ttx_finality_finality_listener_deleted_total` | counter | — | Total number of transactions marked as deleted due to an invalid ledger status or token-request hash mismatch |
| `panurus_services_ttx_finality_finality_listener_hash_mismatch_total` | counter | — | Total number of transactions rejected because the committed token-request hash did not match the locally stored one |
| `panurus_services_ttx_finality_finality_listener_retry_exhausted_total` | counter | — | Total number of transactions whose finality processing was abandoned after all retries were exhausted |
| `panurus_services_ttx_finality_finality_listener_on_status_duration_seconds` | histogram | — | Histogram of total OnStatus processing time per transaction (including retries), in seconds |

Source: `token/services/ttx/finality/metrics.go`.

## Envelope sessions

Recorded by the versioned-envelope wrapper used for view-to-view messaging. `version` is the envelope
version, `type` the message type, and `error` one of `missing_version`, `version_mismatch`,
`type_mismatch`, `invalid_envelope` or `unknown` (see `classifyError` in
`token/services/utils/json/session/envelope.go`).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_utils_json_session_ttx_envelope_sent_total` | counter | `version,type` | Total number of versioned envelopes sent |
| `panurus_services_utils_json_session_ttx_envelope_received_total` | counter | `version,type` | Total number of versioned envelopes received |
| `panurus_services_utils_json_session_ttx_envelope_errors_total` | counter | `error` | Total number of envelope validation errors |
| `panurus_services_utils_json_session_ttx_envelope_body_bytes` | histogram | `type` | Size of envelope body in bytes |

Source: `token/services/utils/json/session/metrics.go`. Wired in `token/sdk/dig/sdk.go`.

## Auditor service

Recorded by the auditor around `Audit()`, `Append()` and `Release()`. These metrics carry **no TMS
labels**, so a node auditing several TMSs reports them aggregated; see
[Coverage gaps](#metric-hygiene).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_auditor_auditor_audit_duration_seconds` | histogram | — | Histogram of Audit() processing time per transaction (including lock acquisition), in seconds |
| `panurus_services_auditor_auditor_audit_lock_conflicts_total` | counter | — | Total number of Audit() calls that failed to acquire enrollment-ID locks |
| `panurus_services_auditor_auditor_append_duration_seconds` | histogram | — | Histogram of Append() processing time per transaction, in seconds |
| `panurus_services_auditor_auditor_append_errors_total` | counter | — | Total number of Append() calls that failed to write to the audit database |
| `panurus_services_auditor_auditor_releases_total` | counter | — | Total number of Release() calls (explicit and deferred) |

Source: `token/services/auditor/metrics.go`.

## Token selection (sherdlock)

Recorded by the default token selector. `outcome` is one of `success`, `insufficient_funds`,
`locked_funds` or `error`; `fetcher_type` is `eager` or `lazy`. `selection_immediate_retries` shows how
often a selection had to retry because of concurrent lock contention.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_selector_sherdlock_unspent_tokens_invocations` | counter | `fetcher_type` | The number of invocations |
| `panurus_services_selector_sherdlock_selection_duration_seconds` | histogram | — | Duration of a token selection call in seconds |
| `panurus_services_selector_sherdlock_selection_outcome_total` | counter | `outcome` | Total number of token selection outcomes by result type |
| `panurus_services_selector_sherdlock_selection_immediate_retries` | histogram | — | Distribution of immediate retry counts per token selection call |

Source: `token/services/selector/sherdlock/metrics.go`.

## Certification (interactive)

`certified_tokens` is recorded by the certification service (server side); the remaining four are
recorded by the certification client. Note the asymmetry in labels — the client-side metrics carry
`channel` and `namespace` but not `network`.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_certifier_interactive_certified_tokens` | counter | `network,channel,namespace` | The number of tokens certified. |
| `panurus_services_certifier_interactive_certification_request_duration_seconds` | histogram | `channel,namespace` | Histogram of certification batch request durations in seconds. |
| `panurus_services_certifier_interactive_certification_errors_total` | counter | `channel,namespace` | Total number of failed certification request attempts. |
| `panurus_services_certifier_interactive_certification_pending_tokens` | gauge | `channel,namespace` | Current number of tokens waiting in the certification input buffer. |
| `panurus_services_certifier_interactive_certification_dropped_tokens_total` | counter | `channel,namespace` | Total number of tokens dropped because the certification buffer was full. |

Source: `token/services/certifier/interactive/metrics.go`.

## Identity and caches

All identity instrumentation is created through the TMS-scoped provider from the wallet service
(`token/core/{fabtoken/v1,zkatdlog/nogh/v1}/driver/ws.go`), hence the
`panurus_core_common_metrics_` prefix despite living under `token/services/identity`.

`outcome` and `path` are `cache`, `routed` or `fallback`. A near-zero
`identity_signer_router_registrations_total` in a busy deployment means routing is not being populated
and every signer lookup falls back to the probing deserializer. The two `*_provision_failures_total`
counters rise when a cache cannot pre-provision in the background, so requests are served on the slow
path while the identity backend keeps failing; the corresponding log line alone is easy to miss.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_core_common_metrics_identity_signer_resolutions_total` | counter | `network,channel,namespace,outcome` | Total number of GetSigner calls by outcome (cache, routed, fallback) |
| `panurus_core_common_metrics_identity_get_signer_duration_seconds` | histogram | `network,channel,namespace,path` | Histogram of GetSigner wall-clock time in seconds, labeled by resolution path |
| `panurus_core_common_metrics_identity_signer_router_registrations_total` | counter | `network,channel,namespace` | Total number of conf_id-to-KeyManager bindings registered with the SignerRouter |
| `panurus_core_common_metrics_identity_signer_router_no_probe_errors_total` | counter | `network,channel,namespace` | Total number of errors from the SignerRouter's probe-free signer deserialization path |
| `panurus_core_common_metrics_cache_level` | gauge | `network,channel,namespace` | Level of the idemix cache |
| `panurus_core_common_metrics_cache_provision_failures_total` | counter | `network,channel,namespace` | Failed attempts to pre-provision idemix identities |
| `panurus_core_common_metrics_recipient_data_cache_level` | gauge | `network,channel,namespace` | Level of the wallet recipient data cache |
| `panurus_core_common_metrics_recipient_data_provision_failures_total` | counter | `network,channel,namespace` | Failed attempts to pre-provision wallet recipient data |

Source: `token/services/identity/metrics.go`, `token/services/identity/idemix/cache/metrics.go`,
`token/services/identity/role/metrics.go`.

## Fabric-X finality queue

Recorded by the Fabric-X event queue that dispatches finality notifications to workers.
`pending_events` is the backlog gauge; `enqueue_drops_total` increments when the buffer was full and an
event was discarded, which means a lost finality notification.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `panurus_services_network_fabricx_finality_queue_finality_queue_pending_events` | gauge | — | Current number of finality events waiting in the queue buffer |
| `panurus_services_network_fabricx_finality_queue_finality_queue_enqueue_drops_total` | counter | — | Total number of finality events dropped because the queue was full |
| `panurus_services_network_fabricx_finality_queue_finality_queue_processing_errors_total` | counter | — | Total number of errors returned by event.Process in worker goroutines |
| `panurus_services_network_fabricx_finality_queue_finality_queue_processing_duration_seconds` | histogram | — | Histogram of successful event processing time in worker goroutines (seconds) |

Source: `token/services/network/fabricx/finality/queue/metrics.go`.

## Example queries

Transaction throughput per TMS:

```promql
sum by (network, channel, namespace) (rate(panurus_services_ttx_accepted_transactions[5m]))
```

95th percentile of the endorsement phase:

```promql
histogram_quantile(0.95, sum by (le, network, channel, namespace) (
  rate(panurus_services_ttx_endorsement_duration_seconds_bucket[5m])
))
```

Share of token selections that failed for lack of spendable funds:

```promql
sum(rate(panurus_services_selector_sherdlock_selection_outcome_total{outcome!="success"}[5m]))
  / sum(rate(panurus_services_selector_sherdlock_selection_outcome_total[5m]))
```

Slowest driver methods, to find the expensive cryptography:

```promql
topk(5, histogram_quantile(0.99, sum by (le, method) (
  rate(panurus_core_common_metrics_transfer_service_duration_seconds_bucket[5m])
)))
```

Fraction of signer lookups that fall back to the probing deserializer:

```promql
sum(rate(panurus_core_common_metrics_identity_signer_resolutions_total{outcome="fallback"}[5m]))
  / sum(rate(panurus_core_common_metrics_identity_signer_resolutions_total[5m]))
```

Integrity and loss alerts — any increase is worth paging on:

```promql
increase(panurus_services_ttx_finality_finality_listener_hash_mismatch_total[10m]) > 0
increase(panurus_services_network_fabricx_finality_queue_finality_queue_enqueue_drops_total[10m]) > 0
```

Finality backlog:

```promql
panurus_services_network_fabricx_finality_queue_finality_queue_pending_events
```

## Not covered here

- **FSC platform metrics** — view executions, sessions, gRPC, database and process metrics come from
  Fabric Smart Client and are documented in its [Monitoring](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/services/monitoring.md)
  page and [Metrics Catalog](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/services/monitoring_metrics.md).
  View-level timing in particular is already instrumented by FSC, so the SDK does not duplicate it.
- **Load-generator metrics** — `integration/nwo/txgen/service/metrics/` and `integration/nwo/runner/`
  instrument the test harness, not the SDK, and are not exported by a Panurus node.
- **Traces** — spans are a separate mechanism; see [Monitoring](./monitoring.md).

## Coverage gaps

The second half of this reference: what a Panurus node currently cannot tell an operator. Ordered by
operator value. Each item states what is blind today, a concrete proposal, and why existing signals do
not already cover it.

### 1. Storage layer

`tokendb`, `ttxdb`, `auditdb`, `identitydb` and the keystore have **no instrumentation at all**. A slow
node cannot be attributed to storage from SDK metrics alone; the driver and ttx histograms include
storage time without isolating it.

Proposal: `store_operations_total{store,operation,outcome}` (counter) and
`store_operation_duration_seconds{store,operation}` (histogram), recorded in the store service layer.

Why not just a database exporter: an exporter reports IO, contention and query statistics, but cannot
attribute them to `store="tokendb"`/`operation="write"` without per-query parsing rules that have to be
maintained against every schema change. Embedded backends such as SQLite have no exporter at all, so
SDK-level counters are the only available signal there. The two are complementary — the exporter for IO
and contention, these counters for application semantics.

### 2. Transaction failure counter

`panurus_services_ttx_accepted_transactions` has no failure counterpart, so a **success rate cannot be
computed from SDK metrics**. A deployment where every transaction fails looks, in these metrics, like a
deployment with no traffic.

Proposal: `panurus_services_ttx_transactions_total{network,channel,namespace,phase,outcome}` (counter)
with `phase` in `assemble|endorse|audit|order|commit` and `outcome` in `success|failure`, from which
throughput and failure rate both follow.

### 3. Commit-time rejections

Nothing counts transactions the ledger rejects, broken down by reason. Note that **double spending can
only be enforced at commit time**, so this is where a double-spend attempt becomes visible — it cannot
be detected earlier during validation.

Proposal: `panurus_services_network_commit_rejections_total{network,channel,namespace,reason}` (counter),
recorded where the commit result is processed, with `reason` distinguishing double-spend from signature,
policy and format failures.

### 4. Standard Fabric network path

The Fabric-X finality queue is instrumented; the standard Fabric network driver
(`token/services/network/fabric/`) has no equivalent, and the approval path — `EndorserService` in
`token/services/network/fabric/endorsement/provider.go` — is not timed. On Fabric deployments the
approval round trip is therefore invisible.

Proposal: `panurus_services_network_fabric_approval_duration_seconds{network,channel,namespace}`
(histogram) and `..._approval_errors_total{...,reason}` (counter) around the endorser call.

Note that the *views* that drive this path are already instrumented by FSC; the gap is the endorsement
round trip itself, not the view execution.

### 5. Auditor lock contention

`panurus_services_auditor_auditor_audit_lock_conflicts_total` counts failed lock acquisitions but says
nothing about contention short of failure: how long locks are held, how long acquisition waits, how many
holders are active.

Proposal: `panurus_services_auditor_lock_wait_duration_seconds` and `..._lock_hold_duration_seconds`
(histograms), plus `..._locks_held` (gauge).

### 6. Identity cache effectiveness

`cache_level` and `recipient_data_cache_level` report occupancy, not effectiveness. A cache that is
always full but never hit looks identical to one that serves every request.

Proposal: `identity_cache_requests_total{cache,outcome}` (counter) with `outcome` in `hit|miss`,
alongside the existing gauges. `identity_signer_resolutions_total` already does this for signers; the
identity and recipient-data caches deserve the same treatment.

### 7. Wallet resolution

Wallet lookups by identity, enrollment ID or wallet ID are not timed or counted, so a deployment with
pathological wallet resolution has no signal until it shows up as end-to-end latency.

Proposal: `panurus_services_identity_wallet_lookups_total{network,channel,namespace,role,outcome}`
(counter) and a matching duration histogram.

### Metric hygiene

Smaller consistency issues, worth fixing together since each one changes exported names:

- **Package attribution.** Metrics created through the TMS-scoped provider are exported under
  `panurus_core_common_metrics_` instead of their own package, which puts identity and cache metrics in
  a namespace that has nothing to do with them. Setting `Namespace`/`Subsystem` explicitly in the opts,
  or making the wrapper preserve the caller's package, would fix it.
- **Stuttering names.** `panurus_services_auditor_auditor_*`,
  `panurus_services_ttx_finality_finality_listener_*` and
  `panurus_services_network_fabricx_finality_queue_finality_queue_*` repeat their package in the metric
  name.
- **Missing TMS labels.** The auditor, finality-listener, sherdlock and Fabric-X queue families carry no
  `network`/`channel`/`namespace` labels, so on a node hosting several TMSs their values are aggregated
  and cannot be attributed. The certification client metrics carry `channel` and `namespace` but not
  `network`.

All three are breaking changes for existing dashboards and alerts, so they belong in a single deliberate
change with a note in the release notes — not folded into unrelated work.

### Not proposed

- **Re-instrumenting views.** FSC already records view executions; adding SDK-level equivalents would
  duplicate existing series.
- **Validation-time double-spend detection.** Not a metric gap but a protocol property: the check
  happens at commit, so item 3 is where it belongs.
