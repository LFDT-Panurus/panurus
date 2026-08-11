# Grafana dashboards

## `panurus.json` — Panurus Overview

A single overview dashboard covering every metric Panurus exports: 20 panels across 9 rows, one row per
subsystem (driver services, transaction lifecycle, finality listener, envelope sessions, auditor, token
selection, certification and identity caches, signer resolution and cache provisioning, Fabric-X finality
queue).

### Import

1. Grafana → **Dashboards** → **New** → **Import** → *Upload JSON file*.
2. Pick the Prometheus data source that scrapes the node's metrics endpoint when prompted for
   `DS_PROMETHEUS`.

The dashboard declares four template variables — `network`, `channel`, `namespace` and `method` — whose
values are discovered with `label_values` against
`panurus_core_common_metrics_transfer_service_operations_total`. A node that has never issued or
transferred a token exports no series for that metric, so the pickers stay empty until the first
transaction; the unfiltered panels still work.

Requires Grafana 9.0 or later (`schemaVersion` 37).

### Not covered

- **FSC platform metrics** (views, sessions, gRPC, process) — these come from
  [Fabric Smart Client](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/services/monitoring.md)
  and are exported under `fsc_*`, not `panurus_*`.
- **Traces.** The dashboard is metrics-only.
- Panels are built from metric *names*, so they show what a node reports, not whether the reported
  numbers are healthy: there are no thresholds or alert rules here.

### Changing it

Every query in this file is checked by `token/services/metricsdoc`, which asserts that

- each metric a query names is one the SDK registers, under the name Prometheus actually exports;
- each metric name carries its package prefix, so a bare `Name` from the Go source fails the build
  rather than rendering an empty panel;
- each label a query filters or groups on is declared by the metric it is applied to;
- each `$variable` a query interpolates is either a Grafana built-in or declared in this dashboard.

These are the failure modes a dashboard cannot report itself: Grafana does not error on an unknown
metric or an absent label, it renders **No data**, which is indistinguishable from an idle node. An
earlier version of this dashboard ([#1749](https://github.com/LFDT-Panurus/panurus/pull/1749)) had every
one of its 51 panel queries and 4 variable queries written against bare option names from the Go source,
so not one of them matched a series; it closed unmerged.

So: after editing a query, run

```bash
go test ./token/services/metricsdoc/...
```

If you add a panel for a metric that does not exist yet, add the metric first — see
[Metrics Reference](../../development/metrics.md) for the exported names and
[`testdata/metrics.golden`](../../../token/services/metricsdoc/testdata/metrics.golden) for the
machine-readable list.
