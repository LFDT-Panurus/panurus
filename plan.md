# Plan: switch legacy chaincode builds to a Go external builder

Fixes #2363

## Goal

Integration tests currently package/build Go chaincode via Fabric's legacy
path, which lets the peer build it inside a `hyperledger/fabric-ccenv:3.1`
container. That image bundles Go 1.26.0, which cannot compile code that
requires Go 1.27 language features (as introduced by the `fabric-smart-client`
v0.21.0 bump). Switch to Fabric's External Builder mechanism so the peer
compiles chaincode using the *host's* Go toolchain (already pinned to 1.27.1
via `go.mod` + `actions/setup-go`) instead of the ccenv container's fixed,
older one.

## Findings

- FSC's `integration` module (`nwo/fabric`) already supports registering
  external builders generically via `network.Network.ExternalBuilders
  []fabricconfig.ExternalBuilder` (rendered into `core.yaml`'s
  `chaincode.externalBuilders`), and already uses this mechanism for CCaaS
  chaincode (`ccaasBuilderPath()` in `nwo/fabric/platform.go`, keyed off
  `$FAB_BINS/../builders/ccaas`). There is no such wiring for legacy
  (non-CCaaS) Go chaincode — `fabric.NewPlatform` only appends the `ccaas`
  builder when found.
- `integration.Infrastructure.RegisterPlatformFactory` (in the top-level
  `integration` module) lets a caller override the platform factory
  registered under a given name — the default `"fabric"` factory is
  `fabric.NewPlatformFactory()`, calling `fabric.NewPlatform`. Panurus's own
  `integration/nwo/token/factory.go` already calls
  `network.RegisterPlatformFactory` for the `"token"` platform, so the same
  call is available to override `"fabric"` too.
- No FSC/upstream change is required: panurus can register its own `"fabric"`
  `api.PlatformFactory` that wraps `fabric.NewPlatform` and appends a
  panurus-owned external builder (name e.g. `golang`) to
  `platform.Network.ExternalBuilders`, pointing at build/detect/release
  scripts shipped in this repo that invoke the host's `go build` directly
  (no Docker).
- Fabric's builder protocol (`detect`/`build`/`release` executables under
  `<builder>/bin/`) is documented upstream; there's a reference sample at
  `hyperledger/fabric-samples/chaincode-external-builders/golang`. Our
  scripts only need to handle the `golang` chaincode type used by panurus's
  own chaincode, not the general case.
- `requiredImagesFor` in `nwo/fabric/platform.go` still requests the
  `ccenv`/`baseos` docker images whenever any non-CCaaS chaincode is present,
  regardless of external builders — pulling those images is harmless (they
  just go unused), so no change needed there; `make fabric-docker-images` can
  stay as-is (kept as a fallback / for anyone not using the external
  builder).

## Steps

1. [x] Write the external builder scripts (`detect`, `build`, `release`)
   under a new `ci/external-builders/golang/bin/` directory:
   - `detect`: exit 0 only when `metadata.json`'s `type` is `golang`.
   - `build`: unpack the chaincode source, run `go build` using the host
     toolchain (respecting `GOCACHE`/`GOPATH`/module proxy env so it works
     offline in CI), producing the `chaincode` binary and a
     `connection.json`-free release layout matching what the peer's built-in
     `chaincode-launcher` expects for non-CCaaS chaincode (a `bin/chaincode`
     executable at minimum).
   - `release`: copy the built binary into the release output directory.
   - Make all three scripts executable.
2. [x] Add a Go type implementing `api.PlatformFactory` (new package under
   `integration/nwo/token/fabric/` or similar) that wraps
   `fabric.NewPlatform`, then appends
   `fabricconfig.ExternalBuilder{Name: "golang", Path: <abs path to
   ci/external-builders/golang>}` to the returned `*fabric.Platform`'s
   `Network.ExternalBuilders` before returning it.
3. [x] Call `network.RegisterPlatformFactory` with the new factory in the
   same place panurus registers its `"token"` platform factory
   (`integration/token/test_utils.go`), so every integration-test suite picks
   it up.
4. [x] Confirm `topology.Chaincode.Lang`/`Path`/packaging metadata already
   produce a `metadata.json` with `type: golang` (native `peer lifecycle
   chaincode package` behavior) — no topology changes expected, since we're
   only changing which builder handles what's already packaged.
5. [x] Run one legacy (non-CCaaS) integration test locally end-to-end
   (`make integration-tests-dlog-fabric-t1 TEST_FILTER="T1"` or similar) to
   confirm the peer picks the `golang` external builder over ccenv Docker,
   and chaincode compiles/starts successfully.
6. [x] Update `docs/development/debug-integration-tests.md` (or a new
   `docs/development/` page) describing the external-builder chaincode build
   path, in case build failures need debugging differently than before
   (e.g. logs come from the peer's builder invocation, not `docker logs` on
   a ccenv container).
7. [x] Run `make checks`, `make lint-auto-fix`, `make unit-tests-race`.
8. [ ] Stop and get the user's explicit go-ahead before pushing/opening a PR.

## Implementation Progress

- [x] Steps 1-5 done: builder scripts written, `fabricbuilder.NewPlatformFactory`
  added, wired into `integration/token/test_utils.go`, packaging metadata
  confirmed to need no changes, and `make integration-tests-dlog-fabric-t2.1`
  passes end-to-end locally (3/3 specs, `TestEndToEnd` PASS) using the new
  `golang` external builder — no ccenv Docker build involved.
- [ ] Step 6 (docs update), Step 7 (`make checks`/`lint-auto-fix`/
  `unit-tests-race`), Step 8 (go-ahead before push/PR) remain.

## Notes & Decisions

- Chose to override the `"fabric"` platform factory from panurus's own code
  rather than patching FSC upstream, since the extension point
  (`RegisterPlatformFactory` + public `Network.ExternalBuilders` field)
  already exists and needs no upstream change.
- Keeping `fabric-docker-images` / `FABRIC_VERSION` pins untouched — they are
  no longer load-bearing for legacy chaincode once the external builder is in
  place, but removing them is out of scope for this fix.
- Step 5's first two local runs failed with `Header.DataHash is different
  from Hash(block.Data)` during channel join, before any chaincode building
  happened. Root cause: the local `$FAB_BINS` had a mismatched `configtxgen`
  (separately built, arm64/dev-build) alongside v3.1.4 darwin/amd64
  peer/orderer/cryptogen binaries. Re-ran `make download-fabric` to get a
  matched v3.1.4 set; unrelated to this fix, but blocked local validation
  until fixed.
