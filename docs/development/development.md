# Development Guidelines

This page contains link to the development guidelines and more.

- [General Guidelines](./general.md)
- [Writing idiomatic, effective, and clean Go code](./idiomatic.md)
- [Testing Guide](./testing.md): How to run the unit, fuzz, integration and Fabric-X tests.
- [Debugging Integration Tests](./debug-integration-tests.md): Runbook for diagnosing failing integration tests (log locations, Docker and network inspection, Ginkgo focus and skip).
- [Best Practices for Versioning Go Projects](./versioning.md)
- [Development Tools](./tools.md)
- [Linting](./linting.md)
- [Monitoring](./monitoring.md)
- [Metrics Reference](./metrics.md)
- [Grafana dashboards](../monitoring/grafana/README.md)
- [Storage](./storage.md): Store and StoreService layers, the SQL schema, and how the SQLite and Postgres backends differ.
- [SQL Query DSL](./sql-query-dsl.md): The query builder used by the SQL stores, including bound parameters and pagination.
- [Mock Files Generation](./mock.md)
- [AI Agents Best Practices](./ai_agents.md)
- [Update fabric-smart-client](./update-fsc.md): Runbook for bumping the `fabric-smart-client` dependency to the latest `main`.
- [Tools: tokengen](./tokengen.md)
- [Tools: tokendiag](./tokendiag.md)
- [Tools: txgen (load generator)](./txgen.md)
- [Makefile Guide](./makefile.md)
- [Documentation Guide](./documentation.md): How to preview and build this documentation site with MkDocs (`make docs-serve`, `make docs-build`).

## Useful resources

- [How to use SDK's](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/sdk.md): Current structure and guidelines for development of Panurus's SDKs.
- [Common problems](https://github.com/LFDT-Panurus/panurus/wiki/Common-problems): Common problems you may encounter. You can document more issues to help onboarding of future developers.
