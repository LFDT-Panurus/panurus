# Driver Metrics

## Overview

Panurus provides a shared metrics layer for all driver service implementations.
Rather than each driver embedding its own instrumentation, a set of **decorator wrappers** in
`token/core/common/metrics/` transparently records Prometheus-style metrics around every
driver service call. Both the FabToken and ZKAT-DLog drivers are wrapped identically,
ensuring consistent observability regardless of the underlying token technology.

## Approach

The metrics layer follows the **Decorator Pattern**: each wrapper implements the same
`driver.*Service` interface as the real service, delegates every call to the inner
implementation, and records three metrics per method invocation:

| Metric type | What it captures |
|-------------|-----------------|
| **Counter** (`*_operations_total`) | Total number of method invocations |
| **Histogram** (`*_duration_seconds`) | Execution duration of each call |
| **Counter** (`*_errors_total`) | Total number of calls that returned an error |

The metric names in this page are the **declared** names, as written in the wrapper sources.
Prometheus exports them under a prefix derived from the package that creates them; because the driver
wrappers receive a TMS-scoped provider, every metric below is exported as
`panurus_core_common_metrics_<declared name>` — for instance `issue_service_operations_total` is
queried as `panurus_core_common_metrics_issue_service_operations_total`. See
[Metrics Reference](../development/metrics.md) for the derivation rules and the exported names of
every metric in the SDK.

All metrics carry four labels for multi-TMS filtering:

| Label | Description |
|-------|-------------|
| `network` | The Fabric network name |
| `channel` | The channel on which the TMS operates |
| `namespace` | The token namespace |
| `method` | The service method that was invoked (e.g., `Issue`, `Transfer`) |

### Wiring

During driver initialization, each driver factory wraps its concrete services before
returning the `TokenManagerService`:

```
Caller  →  Metrics Wrapper  →  Concrete Driver Service
           (records metrics)    (business logic only)
```

This keeps business logic free of monitoring concerns and guarantees that any new driver
automatically gets the same metrics by wrapping its services at construction time.

### Pitfall: `LabelNames` must include `network`, `channel`, `namespace`

`NewTMSProvider` (`token/core/common/metrics/provider.go`) wraps the underlying `Provider` so that
*every* metric it creates is bound to fixed `network`/`channel`/`namespace` label values via
`.With(...)` before the metric is returned — the caller never supplies these three values itself.

Because of this, any `CounterOpts`/`GaugeOpts`/`HistogramOpts` passed to a TMS-scoped provider's
`NewCounter`/`NewGauge`/`NewHistogram` **must declare `"network", "channel", "namespace"` as
`LabelNames`**, even though nothing in the wrapper code ever passes values for them explicitly.
Forgetting them creates a Prometheus vector with 0 label names while `NewTMSProvider` immediately
calls `.With(...)` with 3 values, which panics at runtime ("inconsistent label cardinality") the
first time the metric is used — not at registration time, so it can slip past a quick smoke test.
This exact mistake shipped in `token/services/identity/metrics.go` and crashed the DVP/DLog
integration suite inside `SignerRouter.Register`; see that file for the corrected `LabelNames`.

## Wrapped Services

Five driver services are wrapped:

### IssueService

Wraps `driver.IssueService`. Methods instrumented:

| Method | Description |
|--------|-------------|
| `Issue` | Create new tokens for one or more recipients |
| `VerifyIssue` | Validate an issue action against its output metadata |
| `DeserializeIssueAction` | Deserialize raw bytes into an `IssueAction` |

Metrics emitted:
- `issue_service_operations_total`
- `issue_service_duration_seconds`
- `issue_service_errors_total`

### TransferService

Wraps `driver.TransferService`. Methods instrumented:

| Method | Description |
|--------|-------------|
| `Transfer` | Move token ownership from sender to receiver(s) |
| `VerifyTransfer` | Validate a transfer action against its output metadata |
| `DeserializeTransferAction` | Deserialize raw bytes into a `TransferAction` |

Metrics emitted:
- `transfer_service_operations_total`
- `transfer_service_duration_seconds`
- `transfer_service_errors_total`

### AuditorService

Wraps `driver.AuditorService`. Methods instrumented:

| Method | Description |
|--------|-------------|
| `AuditorCheck` | Verify a token request and its metadata for regulatory compliance |

Metrics emitted:
- `auditor_service_operations_total`
- `auditor_service_duration_seconds`
- `auditor_service_errors_total`

### TokensService

Wraps `driver.TokensService`. Methods instrumented:

| Method | Description |
|--------|-------------|
| `SupportedTokenFormats` | Return the token formats the driver supports |
| `Deobfuscate` | Reveal the cleartext token from an opaque output and its metadata |
| `Recipients` | Extract the recipient identities from a token output |

Metrics emitted:
- `tokens_service_operations_total`
- `tokens_service_duration_seconds`
- `tokens_service_errors_total`

### TokensUpgradeService

Wraps `driver.TokensUpgradeService`. Methods instrumented:

| Method | Description |
|--------|-------------|
| `NewUpgradeChallenge` | Generate a random challenge for the upgrade protocol |
| `GenUpgradeProof` | Produce a zero-knowledge proof for a token upgrade |
| `CheckUpgradeProof` | Verify an upgrade proof against a challenge and tokens |

Metrics emitted:
- `tokens_upgrade_service_operations_total`
- `tokens_upgrade_service_duration_seconds`
- `tokens_upgrade_service_errors_total`

## Metric Reference

The exported names, types and labels of the fifteen driver metrics are listed in
[Metrics Reference — Driver services](../development/metrics.md#driver-services). That page is kept in
step with the code by `token/services/metricsdoc`, so it is the authoritative list; this page describes
only how the wrappers work and which methods they instrument.

All driver metrics use labels: `network`, `channel`, `namespace`, `method`.

## Source

The wrapper implementations and their tests are located in:
- `token/core/common/metrics/issue.go`
- `token/core/common/metrics/transfer.go`
- `token/core/common/metrics/auditor.go`
- `token/core/common/metrics/tokens.go`
- `token/core/common/metrics/upgrade.go`
- `token/core/common/metrics/wrappers_test.go`
