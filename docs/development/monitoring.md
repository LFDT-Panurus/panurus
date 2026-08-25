# Monitoring

We adopt the monitoring infrastructure provided by the [`Fabric Smart Client`](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/services/monitoring.md).

We use the following two methods to monitor the performance of the application:
* **Metrics** provide an overview of the overall system performance using aggregated results, e.g. total requests, requests per second, current state of a variable, average duration, percentile of duration
* **Traces** help us analyze single requests by breaking down their lifecycles into smaller components

## Where to look next

* [Metrics Reference](./metrics.md) — every metric Panurus exports, under the exact name Prometheus
  serves it, plus how those names are derived, example queries, and the current coverage gaps.
* [Grafana dashboards](../monitoring/grafana/README.md) — an importable overview dashboard covering
  every exported metric, and what to run after editing a query.
* [Driver Metrics](../drivers/metrics.md) — how the driver service wrappers are built and which
  methods they instrument.
* [Fabric Smart Client monitoring](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/services/monitoring.md)
  — the platform metrics and traces Panurus inherits (views, sessions, gRPC, process), and how to
  enable the Prometheus endpoint and the tracing exporter.
