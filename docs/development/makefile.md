# Makefile Guide

This document explains the targets available in the `Makefile` for Panurus. The Makefile allows you to automate common tasks such as installing tools, running tests, and managing Docker images.

## Setup & Installation

These targets help you set up your development environment.

| Target | Description |
| :--- | :--- |
| `make install-tools` | Installs necessary Go tools (linters, generators, etc.) defined in `tools/tools.go`. It also installs `golangci-lint`. |
| `make download-fabric` | Downloads Hyperledger Fabric binaries. You can specify `FABRIC_VERSION` and `FABRIC_CA_VERSION` env vars to control the versions. |
| `make install-softhsm` | Installs SoftHSM for testing hardware security module integration. |

## Testing

These targets run various test suites.

### Unit Tests

| Target | Description |
| :--- | :--- |
| `make unit-tests` | Runs standard unit tests for Panurus, excluding integration and regression tests. |
| `make unit-tests-race` | Runs unit tests with the Go race detector enabled. |
| `make unit-tests-regression` | Runs regression tests. |

### Integration Tests

Panurus has several integration test targets. Some common ones include:

| Target | Description |
| :--- | :--- |
| `make integration-tests-nft-dlog` | Runs NFT integration tests with Idemix driver. |
| `make integration-tests-nft-fabtoken` | Runs NFT integration tests with FabToken driver. |
| `make integration-tests-dvp-fabtoken` | Runs Delivery vs Payment (DvP) integration tests with FabToken. |
| `make integration-tests-dvp-dlog` | Runs DvP integration tests with Idemix. |

(See the `Makefile` for the full list of integration test targets).

## Docker Images

These targets build or pull Docker images required for testing and development.

| Target | Description |
| :--- | :--- |
| `make docker-images` | Pulls all necessary images (Fabric, monitoring, testing). |
| `make fabric-docker-images` | Pulls Hyperledger Fabric images. |
| `make testing-docker-images` | Pulls images like Postgres and Vault for testing. |
| `make monitoring-docker-images` | Pulls monitoring tools like Prometheus, Grafana, Jaeger, and Explorer. |

## Maintenance

These targets help keep your project clean and tidy.

| Target | Description |
| :--- | :--- |
| `make tidy` | Runs `go mod tidy` in all modules to ensure dependencies are clean. |
| `make clean` | cleans up Docker artifacts (containers, volumes, networks) and removes generated test output directories. **Use with caution as it removes Docker volumes.** |
| `make clean-all-containers` | Removes all running and stopped Docker containers. |
| `make clean-fabric-peer-images` | Removes Docker images related to Fabric peers. |
| `make lint` | Runs `golangci-lint` to check code quality. |
| `make lint-auto-fix` | Runs `golangci-lint` and automatically fixes issues where possible. |

## Tools Generation

| Target | Description |
| :--- | :--- |
| `make tokengen` | Installs the `tokengen` tool. |
| `make traceinspector` | Installs the `traceinspector` tool. |
| `make memcheck` | Installs the `memcheck` tool. |
