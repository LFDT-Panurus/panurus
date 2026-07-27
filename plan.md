# Fix #1991: enable the linters that are configured but never run

## Goal

`.golangci.yml` carries `linters.settings` blocks for nine linters that appear in neither
`linters.enable` nor golangci-lint v2's defaults, so the configuration is inert. Angelo asked
to enable them step by step to keep the number of files touched per change small.

## Measured fallout

Counted with `golangci-lint run --enable-only=... --max-issues-per-linter=0` over all nine
Go modules in `GO_MODULES`.

| linter | issues | files |
| --- | --- | --- |
| rowserrcheck | 9 | 3 |
| fatcontext | 2 | 2 |
| lll | 20 | 4 |
| maintidx | 8 | 8 |
| ireturn | 26 | 14 |
| iface | 52 | 37 |
| gocognit | 134 | 83 |
| wrapcheck | 2262 | 395 |
| revive | 3367 | 682 |

## This step

Enable `rowserrcheck` and `fatcontext` only. Five files, and rowserrcheck turns out to catch a
real bug rather than a style nit.

## Implementation Progress

- [x] Done: measured every linter's fallout across all nine modules before choosing.
- [x] Done: `rowserrcheck` found that `dedupedTokenRowsIterator.Next` never checked `rows.Err`,
  so a mid-iteration failure was reported as a clean end of results. Fixed, plus tests.
- [x] Done: 6 sites in `prepared_stmt_holder_test.go` now assert `rows.Err`. Three remaining
  hits are cases the linter cannot see through and carry a specific `//nolint` with a reason.
- [x] Done: `fatcontext`'s two hits are the cleanup and recovery managers holding a lifecycle
  context. Suppressed with a reason rather than restructuring, the pattern is deliberate.
- [x] Done: both linters added to `linters.enable`, confirmed live via `golangci-lint linters`.
- [x] Done: `golangci-lint run` clean on all nine modules, which is what `make lint` runs in CI.
  `go test -race` green on the touched packages, all six `make checks` targets exit 0.

## Notes & Decisions

- `lll` looked free because the root module has zero violations, but the other modules have 20
  across 4 files, 19 of them long function signatures in integration support files and one an
  unwrappable string literal. Pure style churn with no correctness value, so it is left to its
  own step rather than padding this diff.
- `maintidx` only flags large table-driven test functions. Satisfying it means splitting those
  tests, which is churn for its own sake. Worth deciding whether to exclude `_test.go` from it
  or drop the setting entirely.
- The six linters not enabled here keep their settings blocks for now, since the plan is to
  enable them in later steps rather than delete the configuration.
