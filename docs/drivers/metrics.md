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

The full list of metrics emitted by the driver wrappers:

| Metric Name | Type | Description |
|-------------|------|-------------|
| `issue_service_operations_total` | Counter | Total `IssueService` method invocations |
| `issue_service_duration_seconds` | Histogram | Duration of `IssueService` method calls |
| `issue_service_errors_total` | Counter | Total `IssueService` method errors |
| `transfer_service_operations_total` | Counter | Total `TransferService` method invocations |
| `transfer_service_duration_seconds` | Histogram | Duration of `TransferService` method calls |
| `transfer_service_errors_total` | Counter | Total `TransferService` method errors |
| `auditor_service_operations_total` | Counter | Total `AuditorService` method invocations |
| `auditor_service_duration_seconds` | Histogram | Duration of `AuditorService` method calls |
| `auditor_service_errors_total` | Counter | Total `AuditorService` method errors |
| `tokens_service_operations_total` | Counter | Total `TokensService` method invocations |
| `tokens_service_duration_seconds` | Histogram | Duration of `TokensService` method calls |
| `tokens_service_errors_total` | Counter | Total `TokensService` method errors |
| `tokens_upgrade_service_operations_total` | Counter | Total `TokensUpgradeService` method invocations |
| `tokens_upgrade_service_duration_seconds` | Histogram | Duration of `TokensUpgradeService` method calls |
| `tokens_upgrade_service_errors_total` | Counter | Total `TokensUpgradeService` method errors |

All metrics use labels: `network`, `channel`, `namespace`, `method`.

## Source

The wrapper implementations and their tests are located in:
- `token/core/common/metrics/issue.go`
- `token/core/common/metrics/transfer.go`
- `token/core/common/metrics/auditor.go`
- `token/core/common/metrics/tokens.go`
- `token/core/common/metrics/upgrade.go`
- `token/core/common/metrics/wrappers_test.go`
