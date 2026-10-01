# Grafana dashboards

## `panurus.json` — Panurus Overview

A single overview dashboard: 20 panels across 9 rows, one row per subsystem (driver services,
transaction lifecycle, finality listener, envelope sessions, auditor, token selection, certification and
identity caches, signer resolution and cache provisioning, Fabric-X finality queue). It covers every
metric Panurus exports except the signature and throttle series described in
[Signature observability](../../security/signature_observability.md), which have no panel yet.

### Import

1. Grafana → **Dashboards** → **New** → **Import** → *Upload JSON file*.
2. Pick the Prometheus data source that scrapes the node's metrics endpoint when prompted for
   `DS_PROMETHEUS`.

The dashboard declares four template variables — `network`, `channel`, `namespace` and `method` — whose
values are discovered with `label_values`. `network`, `channel` and `namespace` come from
`panurus_core_common_metrics_transfer_service_operations_total`; `method` is unioned across all five
driver services with a `__name__` matcher, because each service reports a disjoint set of method names
and a `method` list taken from one of them would filter the other four down to nothing.

All four set `allValue: ".*"`, so the default “All” selection matches every series rather than
interpolating to an empty string. This matters on a node that has never transferred a token: it exports
no series for the source metric, the pickers stay empty, and without `allValue` a selector such as
`network=~""` would match nothing and blank every filtered panel.

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
