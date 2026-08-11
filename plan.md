# Plan: Remove `Context` from `ttx.Transaction`

## Goal

Remove the `Context context.Context` field stored on `token/services/ttx/transaction.go`'s
`Transaction` struct. Every method that needs a context receives it as an explicit `ctx
context.Context` parameter from its caller instead of reading it off the struct. This fixes
stale-context propagation across serialized/rebuilt transactions and a live bug in
`htlc.Transaction.Lock`, which already accepts `ctx` but ignores it in favor of `t.Context`.

Full rationale and file-by-file breakdown: see the approved plan at
`/Users/adc/.claude/plans/imperative-napping-shannon.md`.

## Implementation Progress

1. [x] `token/services/ttx/transaction.go`: remove `Context` field and its two initializers;
   add `ctx context.Context` param to `Bytes`, `Issue`, `Transfer`, `Redeem`, `Upgrade`,
   `Outputs`, `Inputs`; reword the `Release()` comment.
2. [x] `token/services/ttx/marshaller.go`: `marshal` takes `ctx` as first param.
3. [x] `token/services/ttx/auditor.go`: `Validate` takes `ctx`; fix `startRemote`/`startLocal`
   `.Bytes()` calls.
4. [x] `token/services/ttx/collectactions.go`: thread `ctx`/`context.Context()` through
   `Transfer`/`Bytes` calls.
5. [x] `token/services/ttx/collectendorsements.go`: thread `context.Context()` through
   `Bytes` calls.
6. [x] `token/services/interop/htlc/transaction.go`: `Outputs` gains `ctx`; `Lock` body uses
   `ctx` instead of `t.Context` (no signature change); `Reclaim`/`Claim` gain `ctx`.
7. [x] `token/services/nfttx/transaction.go`: `Issue`/`Transfer`/`Outputs` gain `ctx`, forwarded.
8. [x] `token/services/ttx/boolpolicy/tx.go` and `token/services/ttx/multisig/tx.go`: `Lock`/
   `Spend` gain `ctx`, forwarded to `Transfer`.
9. [x] Update all external call sites in `integration/token/{fungible,nft,interop,dvp}/**/views/*.go`
   to pass `context.Context()` (or the in-scope `ctx`) as the new first argument.
10. [x] Update unit tests: `token/services/ttx/{transaction_test.go, endorse_test.go,
    receivetx_test.go}`, `token/services/nfttx/transaction_test.go`.
11. [x] Update docs: `docs/token_sdk_usage.md`, `docs/upgradability.md`.
12. [x] Verify: `go build ./...`, `make unit-tests`, `make unit-tests-race`, `make checks`,
    `make lint-auto-fix`.

## Notes & Decisions

- No compatibility shims/deprecated wrappers — internal pre-1.0 API, compiler catches every
  missed call site.
- `ttx.Context` (alias of `view.Context` in `views.go`, used only for counterfeiter mocks) is
  unrelated and untouched.
- `ctx` is always the first parameter, matching the existing `InputsAndOutputs`/`IsValid` style.
- `.golangci.yml` on this branch separately (pre-existing, uncommitted) enables the
  `containedctx` linter. A full repo-wide scan (`golangci-lint run --max-same-issues=0
  --max-issues-per-linter=0 ./...` in both the root and `integration/` modules — the linter's
  default output truncates same-message findings, which hid this) surfaced 11 structs holding a
  `context.Context` field across both modules, not just the ones touched by this refactor. Per
  explicit user decision, all 11 were suppressed with `//nolint:containedctx` plus a
  justification comment rather than fixed, since fixing them was out of scope for this plan:
  - Long-lived background-worker/service lifecycle (`ctx`/`cancel` set once in `Start()`,
    cancelled in `Stop()`): `storage/services/cleanup/manager.go`, `storage/services/recovery/manager.go`,
    `network/fabricx/finality/queue/queue.go`, `certifier/interactive/client.go`,
    `storage/db/sql/postgres/notifier.go`, `integration/token/fungible/dlogstress/support/worker.go`.
  - Per-request context override wrapper: `utils/view/view.go`'s `contextWrapper`.
  - Per-event/message struct carrying context through a channel: `storage/db/common/status.go`'s
    `StatusEvent`.
  - Session-wrapper convenience default (ctx captured at construction, explicit
    `...WithContext` variants exist alongside): `utils/session/session.go`'s `S`,
    `token/services/ttx/session.go`'s `localSession`.
  - Test fake mirroring `view.Context`: `token/services/ttx/withdrawal_test.go`'s
    `minimalViewContext`.
  - `make checks` and `make lint-auto-fix` are clean across every module (root, `integration`,
    and all `cmd/*` modules) after these suppressions.
