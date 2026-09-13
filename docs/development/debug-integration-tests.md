---
name: debug-integration-tests
description: "Techniques for debugging Panurus integration tests — log locations, Docker/network inspection, and Ginkgo focus/skip. Trigger: /debug-integration-tests"
trigger: /debug-integration-tests
---

# Debugging Integration Tests

This doc is the single source of truth. In Claude Code it is also exposed as the
`/debug-integration-tests` skill via a symlink at
`.claude/skills/debug-integration-tests/SKILL.md`.

## Running Integration Tests: `TEST_FILTER` Labels

`TEST_FILTER` is a Ginkgo `--label-filter` expression, and it can combine **two
independent kinds of label** with `&&`:

1. **Test-identifying labels** (`T1`, `T2`, `T2.1`, `T3`, ...) — identify the
   scenario itself, set via `Label("T1")` on the individual `It(...)` block
   (e.g. `integration/token/fungible/dlog/dlog_test.go`).
2. **Infrastructure-type labels** (`websocket`, `libp2p`, `replicas`) — identify
   the transport/replication configuration the scenario runs under, defined in
   `integration/ports.go` (`WebSocketNoReplication`, `LibP2PNoReplication`,
   `WebSocketWithReplication`). Suites that call `fungible.TestAll` loop over
   `integration.AllTestTypes` and wrap every `Describe` in the matching infra
   label, so a filter of just `T1` runs T1 once per infra type sequentially.

Combine them to pin a scenario to one infrastructure type:

```bash
make integration-tests-dlog-fabric TEST_FILTER="T1 && websocket"
make integration-tests-fabricx-dlog TEST_FILTER="T6 && libp2p"
```

`fungible.mk` and `fabricx.mk` already expose make targets for the common
combos, e.g. `integration-tests-dlog-fabric-t1-websocket`, `-t1-libp2p`,
`-t1-replicas` (CI uses these to run the three infra configurations as
parallel jobs). The plain `-tN` targets (no infra suffix, e.g.
`integration-tests-dlog-fabric-t1`) leave the infra label unset and run all
three types sequentially.

**Local default: websocket only.** Unless the user asks for `libp2p`,
`replicas`, or "all infra types", always add `&& websocket` (or use an
existing `-websocket` make target) when running integration tests locally.
The other two configurations are far more expensive to set up locally and
are already covered by CI's parallel per-infra jobs.

## Log Locations
- **Integration Tests**: System temp directory (`/tmp/fsc-integration-<random>/...`)
- **Containers**: `docker logs <container_name>`
- **Persisted Logs**: Temporarily modify test to use `NewLocalTestSuite` (outputs to `./testdata`)
- **CI**: For a failing PR, fetch the failed jobs' logs from the most recent failed CI run
  with `ci/scripts/get-pr-failed-logs.sh <PR_NUMBER> [REPO]` (requires `gh` authenticated).
  It saves one cleaned, timestamp-stripped log file per failed job under
  `pr_<PR_NUMBER>_failed_logs/`.

## Public Parameters Setup Failures (fabricx)

On a fabricx network the token public parameters are installed by invoking the
`SetupPublicParams` view on the `issuer` FSC node
(`integration/nwo/token/fabricx/factory.go`). Three things about how that failure is
reported are worth knowing when a suite goes red:

- **Installation is deferred, and `PostRun` must not wait for it.**
  `Backend.InstallPublicParams` is called from `PostRun`, so it only records the public
  parameters — it cannot wait for an issuer that does not exist yet. The token platform is
  not an FSC platform, so NWO calls its `PostRun` before it starts the FSC nodes; waiting
  there would deadlock the bring-up, the installation would burn its retry budget and every
  spec would fail in `BeforeEach` with `client [issuer] not ready after 60 attempts`.
  The recorded parameters are installed by `InstallPendingPublicParams`, which the test
  suites call right after the network is up and before any spec, and whose error fails the
  suite. Specs therefore never start with the public parameters absent. The outcome of each
  installation is also recorded on the backend and surfaced in two later paths:
  - `NetworkHandler.UpdatePublicParams` checks it first, so a spec that updates the
    public parameters fails with the original installation error rather than with a
    confusing follow-up failure;
  - `NetworkHandler.Cleanup` logs it at teardown (`public params installation for [...]
    failed: ...`), so grep the suite log for that line when a network never became usable
    but no spec pointed at the public parameters.

  A test can also block on a recorded installation explicitly with
  `Backend.WaitForPublicParams(tms, timeout)`.
- **A not-yet-started issuer is a wait, not a failure.** Both
  `InstallPendingPublicParams` and `UpdatePublicParams` retry the issuer client lookup
  (60 attempts, 1s apart by default).
  `client [issuer] not ready after 60 attempts` therefore means the issuer FSC node never
  came up — look at its own logs, not at the token platform.
- **Neither path panics.** A `SetupPublicParams` failure surfaces as a test failure
  wrapping the view error (`failed setting up the public params on
  [network:channel:namespace:driver]`). A process that dies with
  `panic: failed updating pps` is running an old build.

## Chaincode Build Path: Go External Builder

Legacy (non-CCaaS) Go chaincode is compiled by a Fabric **external builder**
(`ci/external-builders/golang`), not by the peer's built-in ccenv Docker
build. `integration/nwo/fabricbuilder.NewPlatformFactory` wraps NWO's default
`"fabric"` platform factory and appends this builder to every generated
network's `core.yaml` (`chaincode.externalBuilders`); it is registered in
`integration/token/test_utils.go`'s `TestSuite.Setup`. This exists because
`hyperledger/fabric-ccenv:3.1` bundles an older Go toolchain than
`fabric-smart-client` requires — building with the external builder uses the
*host's* `go` instead, avoiding the version mismatch.

Practical implications for debugging:

- **No `fabric-ccenv`/chaincode container is created** for this chaincode.
  `docker ps`/`docker logs` on a ccenv-style container will show nothing —
  the compiled chaincode runs as a plain host process launched by
  `ci/external-builders/golang/bin/run`.
- **Build/run output goes to the peer's own log**, inline with the rest of
  that peer's log lines (same log file/location as in "Log Locations"
  above), not to a separate container log. A chaincode compile failure shows
  up as a `go build` error captured by the peer's builder invocation.
- **To debug the builder scripts themselves**, run them by hand: `detect`
  takes `<chaincode-source-dir> <chaincode-metadata-dir>`, `build` takes
  `<chaincode-source-dir> <chaincode-metadata-dir> <build-output-dir>`,
  `release` takes `<build-output-dir> <release-output-dir>`, and `run` takes
  `<build-output-dir> <run-metadata-dir>` — the peer always calls them with
  these positional arguments, so pointing them at a real packaged chaincode
  directory (extracted `code.tar.gz` + `metadata.json`) reproduces the exact
  failure outside the test run.
- **`GOCACHE`/`GOPATH`/`GOMODCACHE`/`PATH`/etc. are explicitly propagated**
  from the host environment into the builder via `PropagateEnvironment` in
  `fabricbuilder.propagatedEnv` — if the build fails only in CI, check
  whether a needed Go env var is missing from that list rather than assuming
  a code problem.
- A `Header.DataHash is different from Hash(block.Data)` failure during
  channel join is unrelated to this builder — it means the local `$FAB_BINS`
  binaries are a mismatched set (e.g. `configtxgen` built separately from
  `peer`/`orderer`). Re-run `make download-fabric` to get a consistent set.

## Debugging Techniques
- **Manual Inspection**: Use `time.Sleep()` or pause loops in tests to inspect Docker state
- **Network Preservation**: Check for `no-cleanup` option or manually comment test suite cleanup
- **Focused Tests**: Modify `It(...)` to `FIt(...)` to focus, or `XIt(...)` to skip (never commit these changes)
