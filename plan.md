# Plan: Bump fabric-smart-client to v0.18.0

## Goal
Update Panurus's `github.com/hyperledger-labs/fabric-smart-client` dependency from
v0.17.0 to the tagged release **v0.18.0** across every Go module, absorb the two
breaking API changes in that release, align pinned infra versions, and get
`make checks` / `make unit-tests` green. Do not push or open a PR without explicit
user go-ahead (per AGENTS.md).

Already on branch `fsc-v0-18-0` (== `main`, no commits yet) — no new branch needed.

## Upstream changes (v0.17.0...v0.18.0)
Two PRs are marked breaking (`fix!:` / explicit "Breaking changes" section):

1. **PR #1649** — `view.Session.Send`/`SendError` become ctx-first; `SendWithContext`/
   `SendErrorWithContext` are removed (merged into `Send`/`SendError`).
2. **PR #1644** — leading `context.Context` added to `NewBatchExecutor`,
   `NewBatchRunner`, `NewTimeoutCache`, `NewTimeoutEviction`, `NewListenerManager`,
   `NewSequentialListenerManager`, `NewDeliveryFLM`, `chaincode.NewChaincode`,
   `chaincode.NewManager`, and the package-level `view.RunView`.

Grep-confirmed blast radius in Panurus:
- Panurus never calls FSC's package-level `view.RunView` (it has its own
  independent `token/services/utils/view.RunView` wrapper) — no changes needed there.
- Panurus doesn't call `NewBatchExecutor`/`NewBatchRunner`/`NewTimeoutCache`/
  `NewTimeoutEviction`/`NewDeliveryFLM`/`chaincode.NewChaincode`/`chaincode.NewManager`
  directly — no changes needed.
- `events.NewListenerManager[TxInfo]` (`token/services/network/fabric/finality/deliveryflm.go:165`)
  and `events.NewSequentialListenerManager[KeyInfo]` (`token/services/network/fabric/lookup/deliveryllm.go:146`)
  ARE called directly and need a leading ctx. No ctx flows through their callers
  (`ListenerManagerProvider.NewManager(network, channel string)`). FSC's own
  `channelprovider.go` hit the identical situation ("channel has no higher-level
  context to derive from today") and used `context.WithCancel(context.Background())`
  — mirror that: pass `context.Background()` at both call sites (no `Close()` hook
  exists on `deliveryBasedFLM`/`deliveryBasedLLM` to wire a cancel into, matching
  upstream's own constraint).
- `view.Session` implementers needing the Send/SendError merge:
  - `token/services/ttx/session.go` (`localSession`) — hand-written, real implementer.
  - `token/services/ttx/cleanupsessions_test.go` (`countingSession`) — test double.
  - `token/services/ttx/dep/mock/session.go`, `token/services/ttx/deps/mock/session.go`,
    `token/services/utils/session/mock/session.go` — counterfeiter mocks, regenerate.
  - `token/services/utils/session/session.go` (`S`, `type Session = view.Session`
    alias): internal calls `j.s.SendWithContext(...)` / `j.s.SendErrorWithContext(...)`
    must rename to `j.s.Send(...)` / `j.s.SendError(...)`. `S`'s own public API
    (`Send(state any)`, `SendWithContext`, `SendError(err string)`,
    `SendErrorWithContext`) is a distinct signature (not the FSC interface) and
    stays as-is — not in scope.
  - `token/services/utils/json/session/envelope.go` calls `s.SendWithContext(ctx, env)`
    where `s` is Panurus's own `*session.S` wrapper (not the FSC interface directly)
    — unaffected, no change.
  - `token/services/ttx/session_test.go` — update to the renamed methods it exercises.
- Sub-modules `platform/fabric/services/state/cc/query` and
  `platform/view/services/comm/host/libp2p` stay pinned at v0.16.0 (FSC's own
  root go.mod doesn't reference them — independently tagged; only bump if a build
  error demands it).

## Steps
- [x] 1. `make update-dep DEP=github.com/hyperledger-labs/fabric-smart-client VER=v0.18.0` — done.
- [x] 2. `make update-dep DEP=github.com/hyperledger-labs/fabric-smart-client/integration VER=v0.18.0` — done
      (first attempt hit a transient proxy hiccup on the hashicorp module; retried clean).
- [x] 3. Diff FSC's Makefile pinned infra versions against Panurus's `Makefile`/`fabricx.mk`.
      `FABRIC_VERSION`/`FABRIC_TWO_DIGIT_VERSION`/`FABRIC_X_TOOLS_VERSION`/`FABRIC_X_COMMITTER_VERSION`
      already matched (3.1.4 / v1.0.1 / 1.0.4). Docker image refs HAD drifted: FSC now
      pulls `fabric-baseos`/`fabric-ccenv`/`fabric-x-committer-test-node` from
      `ghcr.io/hyperledger/...` and tags to the plain `hyperledger/...` name; Panurus's
      `Makefile` (`fabric-docker-images`) and `fabricx.mk` (`fabricx-docker-images`) were
      still pulling straight from Docker Hub. Fixed both to match FSC's ghcr.io source.
- [x] 4. Fix `view.Session` Send/SendError merge (files listed above), including all
      `SendWithContext*`/`SendErrorWithContext*` counterfeiter-mock call sites
      (`*Stub`, `*Returns`, `*ReturnsOnCall`, `*CallCount`, `*ArgsForCall`) surfaced by
      `go vet -all ./...` across `token/services/ttx/{,boolpolicy,multisig}/*_test.go`,
      `token/services/utils/json/session/envelope_test.go`,
      `token/services/utils/session/session_test.go`.
- [x] 5. Add `context.Background()` to the two `events.New*ListenerManager` call sites.
- [x] 6. `go generate ./...` to regenerate counterfeiter mocks — see Notes & Decisions
      for the scope-narrowing this required.
- [x] 7. `go build ./... && go vet -all ./...` in every `GO_MODULES` dir — all clean.
- [x] 8. `make lint-auto-fix && make checks-no-tidy` — clean (see Notes & Decisions on
      why `checks-no-tidy` instead of `checks`). `make tidy` run once to refresh
      go.mod/go.sum for the new FSC version.
- [x] 9. `make unit-tests` — all green, zero failures, across every package in the
      root module and the `hashicorp` kvs module. `unit-tests-race` not run (not
      requested; standard suite already gives full confidence in the Send/SendError
      merge and ctx-propagation changes).
- [x] 10. One signed-off commit (`b228a15b1`, `git commit -s`). Waiting for user
      go-ahead before push/PR.

✅ COMPLETE (pending user go-ahead for push/PR)

## Notes & Decisions
- Target is the tagged release `v0.18.0`, not latest `main` HEAD — runbook adapted
  accordingly (VER=v0.18.0 instead of a commit SHA; no new branch).
- `token/services/ttx/dep/mock/session.go` has no owning `//go:generate` directive
  anywhere in the current codebase (orphaned — likely left behind by a past
  refactor), so `go generate ./...` silently skips it. Hand-fixed by copying the
  freshly-regenerated, structurally-identical sibling `token/services/ttx/deps/mock/session.go`
  over it (both mock the same `ttx.Session` = `view.Session` alias). Not fixing the
  missing directive itself — out of scope for this bump.
- `token/services/utils/json/session/json.go:8` has a pre-existing broken
  `//go:generate counterfeiter ... . JsonSession` directive — no `JsonSession`
  interface exists anywhere in that package, so `go generate ./...` errors on it
  (`cannot find package with target: JsonSession`). Confirmed via git diff/ls that
  this predates this session and is unrelated to the FSC bump. Left unfixed —
  out of scope.
- Running `go generate ./...` unscoped regenerated ~15 mock files that have nothing
  to do with the FSC Session API change (a newer locally-installed `counterfeiter`
  version adds `var _ <pkg>.<Interface> = new(<Mock>)` assertion lines and other
  formatting churn project-wide) and created 3 new untracked mock artifacts that
  never existed in git. This violates the "minimal, surgical changes" rule and, as
  a side effect, introduced a genuine new import cycle in
  `token/services/identity/multisig` (a `package multisig` test file importing
  `multisig/mock`, which the new assertion line made import `multisig` right back).
  Fixed by reverting all 15 unrelated files via `git checkout --` and removing the
  3 new untracked artifacts, leaving only the 3 intentional Session mock
  regenerations (`ttx/dep/mock`, `ttx/deps/mock`, `utils/session/mock`) modified.
- `make checks`'s `tidy-check` sub-target diffs `go.mod`/`go.sum` against `HEAD` and
  will always flag them after a legitimate dependency-version bump + `make tidy`.
  The Makefile already anticipates this with a `checks-no-tidy` target
  ("for use after a workflow step that already rewrote go.mod/go.sum to a new
  dependency version and ran 'make tidy' itself") — used that instead of `checks`.
  `make tidy` itself was still run once, and its output (go.mod/go.sum updates
  across all 9 Go modules) is expected/desired.
- `make lint-auto-fix` (and standalone `golangci-lint`) reports 17 pre-existing
  `revive: unhandled-error` findings (unchecked `strings.Builder.Write*`,
  `fmt.Fprintf`/`fmt.Print` return values) in `cmd/profiler/*`,
  `token/core/zkatdlog/nogh/v1/crypto/common/array*.go`, `token/driver/wallet*.go`,
  `token/services/benchmark/runner.go`, `token/services/storage/db/sql/**`. Confirmed
  via `git status`/`git diff` that none of these files were touched by this session —
  pre-existing lint debt, unrelated to the FSC bump, left as-is.
- `token/services/ttx/cleanupsessions_test.go`'s `countingSession` fake dropped from
  7 methods to 5 (merging `Send`/`SendWithContext` and `SendError`/`SendErrorWithContext`
  into ctx-first `Send`/`SendError`) — `gofmt -s` then re-aligned the trailing-comment
  columns on the remaining method one-liners; ran `gofmt -l -s -w` on just that file
  to pick this up (it's what `make checks`'s `gofmt` sub-target itself was flagging).
