# Makefile Guide

This document explains the targets available in the `Makefile` for Panurus. The Makefile allows you to automate common tasks such as installing tools, running tests, and managing Docker images.

## Setup & Installation

These targets help you set up your development environment.

| Target | Description |
| :--- | :--- |
| `make install-tools` | Installs necessary Go tools (linters, generators, etc.) defined in `tools/tools.go`. It also installs `golangci-lint`. |
| `make download-fabric` | Downloads Hyperledger Fabric binaries. You can specify `FABRIC_VERSION` and `FABRIC_CA_VERSION` env vars to control the versions. |
| `make install-softhsm` | Installs SoftHSM for testing hardware security module integration. Skips the install if SoftHSM is present. Retries transient network failures up to 3 times. |

## Testing

These targets run various test suites.

### Unit Tests

The unit-test targets live in [unit-tests.mk](../../unit-tests.mk). There is one target per Go module; `unit-tests-cmd` covers every `cmd/*` module.

| Target | Description |
| :--- | :--- |
| `make unit-tests` | Runs every target below, one after the other. |
| `make unit-tests-race` | Same as `make unit-tests RACE=1`. |
| `make unit-tests-main` | The main module. The regression packages run in a second `go test` call without the test timeout. Most selector and storage tests start Postgres or Vault containers; needs `make testing-docker-images`. |
| `make unit-tests-hashicorp` | The `token/services/storage/db/kvs/hashicorp` module. Starts a Vault container. |
| `make unit-tests-evm` | The `x/token/services/network/evm` module. The anvil e2e suite needs Foundry. |
| `make unit-tests-integration` | The `integration` module packages whose tests do not import Ginkgo. The Ginkgo suites are integration tests. |
| `make unit-tests-cmd` | Every `cmd/*` module. |
| `make unit-tests-check` | Fails if a tracked Go module has no target, if an exempt module (`tools`) has tests, or if the two CI workflows do not run exactly these targets with the same `docker` and `foundry` flags. Part of `make checks-fast`. |
| `make unit-tests-mk-test` | Tests the targets above (`ci/scripts/unit-tests-mk-test.sh`). Part of `make checks-fast`. |
| `make pr-issue-check-test` | Offline test of the PR metadata checks (`ci/scripts/pr-issue-check-test.sh`, see [Automation](general.md#automation)). Part of `make checks-fast`. |
| `make test-evm-contracts` | Runs the EVM driver's Solidity tests (`forge test`) and `forge fmt --check`. Skips without `forge` or the `forge-std` submodule; set `EVM_CONTRACTS_REQUIRED=1` to fail instead. |

Every unit-test target accepts `RACE=1`, which adds `-race` and sets `GORACE=history_size=7`. `unit-tests-main` and `unit-tests-evm` accept `COVERAGE=1`, which instruments every package of the module (`-coverpkg=./...`) and writes the profile to `COVERAGE_PROFILE` (default `profile.cov`). `unit-tests-main` fails if it finds no regression packages, instead of silently testing the repository root.

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
| `make govulncheck` | Scans dependencies for known, reachable vulnerabilities with `govulncheck`. Findings listed in `ci/govulncheck-allowlist.txt` don't fail the build. |

## Tools Generation

| Target | Description |
| :--- | :--- |
| `make tokengen` | Installs the `tokengen` tool. |
| `make traceinspector` | Installs the `traceinspector` tool. |
| `make memcheck` | Installs the `memcheck` tool. |
