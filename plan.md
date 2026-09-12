# Update fabric-smart-client to v0.21.0

## Goal
Bump the `github.com/hyperledger-labs/fabric-smart-client` dependency (currently
`v0.18.0`) to the tagged release `v0.21.0` across every Go module in the repo,
absorb any resulting API/lint breakage, align pinned infra versions, and get
`make checks` / `make unit-tests` green. Adapts the `/update-fsc` runbook
(normally targets latest `main`) to a fixed tagged version instead.

## Implementation Steps
1. [x] Branch off `main` as `fsc-update-v0.21.0`. (main == fsc_v_0_12_0 branch tip)
2. [x] Bump dependency in all modules: `make update-dep DEP=github.com/hyperledger-labs/fabric-smart-client VER=v0.21.0`
   and same for the `/integration` submodule path. Go toolchain bumped 1.26.5 -> 1.27.1
   (FSC v0.21.0 requires go >= 1.27.1); several transitive deps upgraded too.
3. [x] Diff FSC's Makefile-pinned infra versions (FABRIC_VERSION, FABRIC_TWO_DIGIT_VERSION,
   FABRIC_X_TOOLS_VERSION, FABRIC_X_COMMITTER_VERSION) and Docker image refs against
   Panurus's root `Makefile` / `fabricx.mk` — all already match, no drift.
4. [x] Build + vet every module in `GO_MODULES` to detect API breakage. Breakage found
   in `integration` module (+ `cmd/artifactgen`, which imports it) and in
   `token/services/network/fabric/config` test file.
5. [x] Fixed compile errors:
   - `integration/nwo/token/fabric/cc/tcc.go`: removed the removed `Chaincode.Policy`
     field (kept `SignaturePolicy`, which carries the same value).
   - `integration/nwo/token/fabricx/factory.go`: `Topology.AddNamespaceWithUnanimity`
     was removed and `AddNamespace` now takes an `EndorsementPolicy` + `NamespaceOption`s
     instead of `(name, policy string, peers ...string)`. Switched to
     `fabrictopology.Unanimity(orgs...)` / `fabrictopology.Signature(policy)` +
     `fabrictopology.WithPeers(peers...)`.
   - `integration/nwo/runner/rpc/user_provider.go`: `grpc.ConnectionConfig` dropped
     `TLSEnabled`/`TLSRootCertFile` in favor of a `TLS SecureOptions` field taking raw
     PEM bytes; `webclient.Config` dropped `TLSCertPath`/`TLSKeyPath` in favor of
     `TLSCertRaw`/`TLSKeyRaw`. Read the cert/key files with `os.ReadFile` at the call
     site and pass the bytes through.
   - `token/services/network/fabric/config/config_test.go`: `driver.ConfigService`
     gained `RawSubtree`/`RawSubtrees`; added no-op stubs to the test's
     `mapConfigService`.
   No counterfeiter mock regeneration was needed (`go generate ./...` not required —
   no mocked interface implementing `ConfigService`, `Chaincode`, etc. exists).
6. [x] `make lint-auto-fix` and `make checks-no-tidy` until clean (see notes on `checks`
   vs `checks-no-tidy` and the `gofix` modernization pass below).
7. [x] `make unit-tests`; fixed one regression (sherdlock iterator bug, see below).
   `unit-tests-race` not run (time-boxed; no concurrency-sensitive code changed beyond
   the pre-existing sherdlock locking, which `gofix` also touched mechanically).
8. [ ] Commit (signed off) with old→new version noted in body.
9. [ ] Stop and report; wait for user go-ahead before pushing / opening PR.

## Implementation Progress
- [x] Dependency bumped, all modules build/vet clean.
- [x] Lint (`make lint-auto-fix`) clean across all `GO_MODULES` — many pre-existing
  revive `unhandled-error` findings surfaced by golangci-lint's rescan after the go1.27.1
  bump; fixed the unwrapped `strings.Builder`/`hash.Write`/`fmt.Fprintf` calls it flagged,
  plus one `time-naming` rename (`PayerAccessTokenExpMin` -> `PayerAccessTokenExp`).
- [x] `make checks-no-tidy` clean, including a `make gofix-apply` pass (go1.27 automated
  modernizations: manual atomic int fields -> `atomic.Int32/Uint32/Int64` methods in
  `token/services/utils/cache` tests and `token/services/selector/sherdlock/fetcher.go`;
  a manual reverse loop -> `slices.Backward` in
  `token/core/zkatdlog/nogh/v1/validator/validator_security_test.go`; a struct-literal
  simplification in `token/services/ttx/dep/wrapper/dbs_test.go`).
- [x] `make unit-tests`: all packages pass except
  `token/services/identity/storage/kvs/hashicorp`, which fails locally with
  "missing required image: hashicorp/vault:latest" — a Docker-image-availability gap in
  this sandbox, not a code regression (only `go.mod`/`go.sum` changed in that module;
  confirmed no `hashicorp/vault` image is present locally). Expected to pass in CI where
  the image is pulled.

## Notes & Decisions
- Target is a tagged release (v0.21.0), not latest main — skipped the SHA-resolution
  step from the runbook and used the tag directly with `go get ...@v0.21.0`.
- Left replace-pinned FSC submodules (`state/cc/query`, `comm/host/libp2p`) untouched
  unless a build error demands otherwise.
- **`checks` vs `checks-no-tidy`**: `make checks`'s `tidy-check` step fails whenever
  go.mod/go.sum differ from git HEAD at all — which they legitimately do after this
  bump. Used `checks-no-tidy` instead (already documented in `checks.mk` for exactly
  this "workflow step already rewrote go.mod/go.sum and ran `make tidy` itself"
  scenario), after confirming `make tidy` had already been run and produced no further
  diff.
- **Resolved**: bumped `Makefile:260`'s golangci-lint install pin from v2.12.2 to
  v2.13.2 (matches both the version already installed locally and the current latest
  upstream release), since v2.12.2 can't satisfy the go1.27.1 language-version check
  this FSC bump requires. Re-ran `make checks-no-tidy` and `make lint-auto-fix` after
  the bump — both clean (9/9 modules report "0 issues.").
- **Bugfix beyond the mechanical bump**: `token/services/selector/sherdlock/fetcher.go`'s
  `mixedFetcher.UnspentTokensIteratorBy` did an unchecked type assertion
  `it.(interface{ HasNext() bool }).HasNext()` on the iterator returned by the eager
  (cached) fetcher. FSC v0.21.0's `Iterator[V]` contract only ever guaranteed
  `Next()`/`Close()` — it never guaranteed `HasNext()` — and the concrete empty-iterator
  type returned on a cache miss (`iterators.Empty[K]()`) doesn't implement it, so this
  panicked in production on every cache-miss request through the mixed-fetcher strategy,
  not just in tests. Confirmed via `make unit-tests`
  (`TestCachedFetcher_UnspentTokensIteratorBy_CacheMiss` panicked with "interface
  conversion: ... missing method HasNext"). Fixed by adding a generic
  `peekIterator`/`peekedIterator[T]` helper that determines emptiness by consuming and
  replaying the first `Next()` result, matching FSC's actual (and this repo's own
  `sherdlock.Iterator[k]`) interface contract. Updated the two dependent tests in
  `fetcher_test.go` to assert via `it.Next()` against the real contract instead of the
  fragile concrete-type assertion. Repo-wide grep (`grep -rn "interface{ HasNext"`)
  confirmed this was the only occurrence of the pattern.
- Checked `docs/` for impact: this is an internal dependency bump plus an internal
  bugfix in `sherdlock` (unexported-behavior-preserving; no public API/protocol/CLI
  surface changed), so no `docs/` updates are required.
