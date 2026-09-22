# Issue #2395 — sherdlock lock-contention fix: PR-by-PR analysis

Source data: CERT stress-test log `Liste_60_tokens_collisions_CERT_20260903.csv`
(60/1276 tokens with a `zkat_token_locks_pkey` collision; 5 tokens ≈ 0.39% of
the wallet's 1276 tokens absorbed 85.0% of all collisions; 8 tokens
(0.63%) absorbed 95.2%; token `f8a27fc4...` alone accounted for 1296
collisions (23.0%) spread from 14:06:18.515 to 14:13:00.453 — the "over six
minutes" in the report). Issue: [#2395](https://github.com/LFDT-Panurus/panurus/issues/2395).
PR stack (base → tip):

| PR | Phase | Title | Base |
|----|-------|-------|------|
| [#2397](https://github.com/LFDT-Panurus/panurus/pull/2397) | 1–2 | diagnostics + reproducible contention baseline | `main` |
| [#2398](https://github.com/LFDT-Panurus/panurus/pull/2398) | 3 | per-`Select`-call lock blacklist | #2397 |
| [#2399](https://github.com/LFDT-Panurus/panurus/pull/2399) | 4 | anti-join locked tokens + size-ordered candidates | #2398 |
| [#2400](https://github.com/LFDT-Panurus/panurus/pull/2400) | 5 | release locks on transaction settlement | #2399 |
| [#2402](https://github.com/LFDT-Panurus/panurus/pull/2402) | 6 | configurable Postgres lock-acquisition strategies | #2400 |
| (branch `fix/2395-phase7-testable-gaps`) | 7 | close testable gaps 1–4, 6, 7, 9 below | #2402 |

All five mechanisms named in the issue map 1:1 onto Phases 3–6:

1. Repeated re-offering of an already-lost-race token within one `Select` call → **Phase 3**
2. No size-based candidate ordering (small payment could draw the largest token) → **Phase 4b**
3. No lock-aware candidate query (a selector could still start a race it was bound to lose) → **Phase 4a**
4. No lock release on successful settlement (locks lived until the lease sweep) → **Phase 5**
5. Lost races surfacing as server-side unique-constraint errors (the log storm itself) → **Phase 6**

---

## PR #2397 (Phase 1–2) — diagnostics, no behavioral change

**Goal:** make the next four PRs falsifiable instead of guesswork. Landed with *zero* change
to selection or locking logic.

- `driver.TokenLockStore.ListLocks` — read API over held locks, joined with the consumer
  transaction's status, so "is this lock leaked" becomes a query instead of a manual
  Postgres/log correlation exercise. This directly answers the issue's ask #2 (a
  diagnostic tool) and its sub-question "has its availability status been correctly
  updated" for `f8a27fc4...`.
- `cmd/tokendiag locks` CLI — surfaces `ListLocks`, flags a lock whose consumer is already
  `Confirmed`/`Deleted`/`Orphan` as **leaked** — i.e. exactly the state the CERT report's
  6-minute token was suspected to be in.
- `sherdlock/metrics.go`: `LockConflicts` counter + `DistinctTokensAttempted` histogram,
  distinguishing "one hot token retried many times" from "many tokens each contended once" —
  the CSV's own Pareto shape (5 tokens = 85% of collisions) is exactly the first pattern.
- `selector.go` conflict log line promoted `Debug` → `Info`, tagged with the token ID, and
  both backends (Postgres store, in-memory locker) now map a lost race to the same
  `driver.ErrTokenAlreadyLocked` sentinel.
- **Baseline test**: `testutils.TestHotTokenContention` — few small tokens + one rotating
  large "hot" token, far more concurrent requests than tokens, against real Postgres.
  Baseline: **300 distinct tokens attempted, ~7500 lock attempts, ~96% conflict rate**, no
  single token ID dominating — because `deleteTokensAndStoreChange` mints a fresh output ID
  every time the hot token is spent, so the *lineage* is hot, not a static ID. Hard assertion:
  total demand == total wallet balance, so any observed selection error is provably spurious.

**Before/after:** none — this PR is instrumentation and a yardstick, not a fix.

## PR #2398 (Phase 3) — per-call lock blacklist

**Root cause fixed:** issue mechanism 1 — `selectInternal` had no memory of a lock it had
already lost within the same `Select` call, so a refetch could re-propose and re-lose the
same hot token repeatedly. This is the direct mechanism behind the CSV's `f8a27fc4...` row:
one token, 1296 collisions, a single first/last-occurrence window of ~6m42s.

**Change:** a per-call (not process-global) blacklist of tokens this `Select` invocation has
already lost a race on; skipped on later scans of the same call. Cleared if an entire scan
yields no non-blacklisted candidate, so a genuinely tight wallet doesn't get turned into a
permanent false `insufficient funds`. A token freed by another process is still reconsidered
on the *next* `Select` call.

**Measured (`TestHotTokenContention`, before → after):**

| metric | Phase 2 baseline | Phase 3 |
|---|---|---|
| total lock attempts | 7468 | 5096 (−32%) |
| total conflicts | 7168 | 4796 |
| conflict rate | 0.96 | 0.94 |
| distinct tokens attempted | 300 | 300 |
| max single-token conflict share | 0.04 | 0.06 |

Fewer wasted attempts within a call; aggregate conflict rate barely moves because this only
stops re-attempts *within* one call — the cross-call race for the same rotating hot token is
untouched. That's explicitly deferred to Phase 4.

## PR #2399 (Phase 4) — anti-join + size-ordered candidates

**Root causes fixed:** issue mechanisms 2 and 3.

- **4a anti-join:** the spendable-tokens query now excludes tokens already held by a lock,
  via a dialect-independent `NOT EXISTS` against `TokenLocks` (`cond.NotExists`, built on
  Phase 1's DSL primitive). Stops a selector from *starting* a race it's bound to lose,
  rather than losing it and recovering (Phase 3's job). Because the anti-join can hide every
  remaining candidate from a wallet that still has funds (they're just all locked elsewhere),
  a new lock-ignoring `TokenFetcher.HasAnySpendableTokens` disambiguates a genuinely-empty
  scan from a fully-locked-but-solvent one before reporting `SelectorInsufficientFunds` —
  this is precisely the issue's reported symptom ("insufficient funds" on a 1 EUR payment
  despite multiple tokens in the wallet).
- **4b size ordering:** candidates ordered ascending by amount, shuffled only within
  same-amount runs (`bucketedIterator.NewPermutation`). Directly answers the issue's Xavier
  test: 1 EUR request, 1 EUR + 200 EUR available → previously could draw the 200 EUR token
  (unordered); now the 1 EUR token sorts first. Deliberately *not* strict smallest-fit — that
  would relocate all contention onto the single smallest token; shuffling within a bucket
  keeps contention spread across equally-good candidates.
- Only `sherdlock` is touched; `simple` (unordered, no anti-join) is explicitly unchanged.

**Measured (`TestHotTokenContention`, Phase 3 → Phase 4):**

| metric | Phase 3 (blacklist only) | Phase 4 |
|---|---|---|
| total lock attempts | 4646 | 3414 (−26%) |
| total conflicts | 4346 | 3114 (−28%) |
| conflict rate | 0.94 | 0.91 |
| distinct tokens conflicted | 296 | 212 |
| max single-token conflict share | 0.07 | 0.10 |

First measurable drop in *distinct tokens contended at all* (296→212) — the anti-join is
keeping already-locked tokens out of candidate sets across calls, not just within one.

## PR #2400 (Phase 5) — release locks on settlement

**Root cause fixed:** issue mechanism 4 — the exact question the CERT report raised about
`f8a27fc4...`: "why does this token stay locked well after its transaction settled?" Answer,
confirmed by code inspection: **no production path unlocked on success.**
`Transaction.Release` (the only production `Unlock` caller) was wired solely to
`context.OnError`; `view.Context` has no success hook. A settled transaction's locks
therefore sat until the `leaseExpiry` sweep (minutes, by config) — during that entire window
the tokens were both spent-for *and* invisible to the Phase 4 anti-join, so they kept
colliding.

**Change:** both places that already compute terminal transaction status now call
`SelectorManager.Unlock` — `finality.Listener.runOnStatus` (live subscription) and
`TTXRecoveryHandler.applyFinalityLogic` (crash recovery) — for **both** `Confirmed` and
`Deleted` (a failed tx never spends its selected tokens either, so no reason to hold the
lock). Best-effort: a failed `Unlock` is logged, never fails settlement/recovery; the lease
sweep remains the backstop for `Orphan` (which the finality path never observes) and for any
failed release call. Wired at all three production sites (`ttx.Service`, `auditor.Service`,
Fabric recovery-manager).

**Measured:** *unchanged* — `TestHotTokenContention` calls `Select` and deletes/spends tokens
synchronously against the store, never routing through TTX finality, so it is structurally
blind to this mechanism. Verification is at the unit level instead: `Unlock` fires exactly
once per terminal transaction (`TestOnStatus_ReleasesLocksAfterTerminalStatus`,
`TestTTXRecoveryHandler_Recover_ReleasesLocksOnConfirmed/OnDeleted`), and a failing `Unlock`
doesn't fail the settlement/recovery path
(`TestTTXRecoveryHandler_Recover_LockReleaseErrorDoesNotFailRecovery`). This is a real,
important limitation of the benchmark harness — see gaps below.

## PR #2402 (Phase 6) — configurable Postgres lock-acquisition strategies

**Root cause fixed:** issue mechanism 5 — the collision *symptom itself*: every lost race
under the prior plain-`INSERT` acquisition surfaced as a server-side
`zkat_token_locks_pkey` unique-constraint violation, which is what actually filled the
Postgres logs the CERT report was built from.

**Change:** `token.storage.db.lockStrategy` (`insert` default / `onConflict` / `skipLocked`)
on the Postgres `TokenLockStore`; SQLite reads and ignores the key, `simple` never reaches
this config path — both untouched by construction.
- `onConflict`: `INSERT ... ON CONFLICT DO NOTHING RETURNING` — a lost race becomes a clean
  zero-row result, not a server error.
- `skipLocked`: same, plus `BatchLocker.LockBatch` — claims a whole covering window of
  candidates in one statement under `FOR UPDATE OF <tokens> SKIP LOCKED`, joined with the
  same `ON CONFLICT DO NOTHING` backstop.
- `HasEnoughSpendableTokens` lets the Phase 4 empty-anti-join fallback fail fast on a
  genuinely unpayable wallet instead of burning the full backoff budget first.

**Measured, precisely scoped (not inferred):**
- Aggregate conflict *rate* does **not** move across strategies — confirmed, not assumed:
  `SKIP LOCKED` only helps against a rival mid-claim at the exact same instant, not against
  an already-committed lock, which is the dominant conflict mode under sustained load.
- Server-side unique-constraint errors go to **zero**, but only on the single-token `Lock`
  path — `sherdlock` always uses `LockBatch` once a store implements `BatchLocker` (true for
  Postgres under every strategy), so under normal operation `insert`'s error-surfacing path
  is already dead code from the selector's own call site. `insert` only matters for a
  non-`BatchLocker` `Locker` (custom backend, or a rolling deploy with some replicas not yet
  upgraded). Isolated directly by forcing the single-token path
  (`TestHotTokenContention_SingleTokenLockPath`): `insert` → thousands of real
  unique-violations per run; `onConflict`/`skipLocked` → zero.
- Round-trip reduction comes from batching the claim into one statement per covering window —
  a property of `LockBatch` itself, uniform across all three strategies — not from strategy
  choice.
- `SKIP LOCKED` mechanism isolated deterministically against real Postgres
  (`TestTokenLockStore_LockBatch_SkipLocked_SkipsRowLockedByConcurrentTx`): a row a rival
  transaction is mid-claim on is skipped, not blocked; a plain `FOR UPDATE` on the same row
  genuinely blocks (control case).

## Phase 7 (`fix/2395-phase7-testable-gaps`) — close 7 of the 9 testable gaps

**Not a mechanism fix** — the five mechanisms were already closed by Phases 3–6. Phase 7 closes
gaps 1, 2, 3, 4, 6, 7, and 9 from the list below with tests, plus two fixes exploration turned up
along the way (a config collateral-loss bug, and a missing warning; see below). Gaps 5 (`simple`
baseline) and 8 (fabtoken/dlog integration tests) are explicitly excluded from this phase.

- **Gap 1 (settlement release, contention-level).** New `TestHotTokenContentionWithSettlement`
  routes the harness's release step through the real `finality.SelectorManagerProvider`
  wired to a `depmock` resolving to the replica itself, then asserts via `ListLocks` that no
  lock survives its consumer's terminal status — closing the exact blind spot gap 1 named.
  `selector_manager_test.go` also adds the provider's first test (error path, `nil, nil`,
  no-caching).
- **Gap 2 (lock-age assertion).** `TestListLocksReportsLockAge` in `dbtest/tokenlock.go`
  backdates via `LockAt` and asserts `now.Sub(rec.CreatedAt)` lands in the expected window,
  across all three drivers (memory/sqlite/Postgres) for free.
  `driver.IsTerminalStatus` promoted out of `cmd/tokendiag` so `TestReleaseAfterConfirm` can
  assert no record satisfies `IsTerminalStatus(r.Status) && now.Sub(r.CreatedAt) > N` without
  duplicating the predicate.
- **Gap 3 (size-ordering regression test).** New `sherdlock/ordering_test.go`:
  `TestSizeOrderedSelection_SmallestFit` reproduces the exact Xavier scenario (small + large
  token, small request, small token selected) over a real `lazyFetcher` +
  `testutils.MockQueryService`, container-free. Found and fixed two real bugs along the way:
  `MockQueryService.WarmupCache`'s non-deterministic map iteration order (didn't match the real
  fetcher's `ORDER BY amount ASC`), and a nil-dereference panic in
  `SpendableTokensIteratorBy`'s `collections.Map` transformer on a full drain (also affected the
  benchmarks, unnoticed because they don't run under `go test`).
- **Gap 4 (lock-failure classification under contention).** New
  `sherdlock/lock_classification_test.go`: the batch-path rate-limit hard-abort
  (`TestBatchLockRateLimit_HardAborts`, previously unexercised — `TestSelectorRateLimit` only
  covers the single-token path), the deliberate store-error-vs-lost-race asymmetry in both the
  batch and single-token paths (`TestBatchLock*`, `TestSingleLock*` — a store error is retried
  and not counted as a conflict; a lost race is blacklisted and counted), and the
  blacklist-clearing escape hatch (`TestBlacklistClearsWhenScanSeesOnlyBlacklistedCandidates`).
- **Gap 6 (rolling-deploy mixed strategy).** New
  `postgres/tokenlock_mixed_strategy_test.go`'s `TestTokenLockStore_MixedStrategy_RollingDeploy`:
  two `TokenLockStore`s sharing one table, one `insert`-strategy and one `skipLocked`-strategy,
  racing `Lock()` for the same token — confirms exactly one winner and no corruption under a
  mixed-version fleet, the scenario Phase 6's docs claimed but never tested.
- **Gap 7 (`leaseExpiry`/`leaseCleanupTickPeriod: 0` misconfiguration).** Turned out to be a
  **documentation bug, not a reachable misconfiguration**: `Config.GetLeaseExpiry()`/
  `GetLeaseCleanupTickPeriod()` already treat 0 (unset or explicit) as "use the default", so
  YAML can never actually disable the sweep — only an in-process caller bypassing the `Config`
  getters can. `docs/services/selector.md` corrected accordingly, and `sherdlock/manager.go`'s
  `NewManager` now logs a warning when that in-process path is taken, naming both durations.
  `config/driver_test.go`'s existing `TestConfig_GetLeaseExpiry`/`GetLeaseCleanupTickPeriod`
  "default when zero" subtests already pinned the getter behavior this doc fix relies on.
- **Gap 9 (metrics assertions).** `lock_classification_test.go`'s `lockConflictsProvider`
  extended into `lockMetricsProvider` (name-targeted `countingCounter` for
  `lock_conflicts_total`, plus a new `recordingHistogram` for `distinct_tokens_attempted`).
  Five new tests confirm `LockConflicts` increments on a genuine lost race but not on a store
  error (both paths), and `DistinctTokensAttempted` observes the correct count and is never
  observed on the two earliest error returns (closed selector, invalid quantity) — both
  previously entirely untested.

**Bug found and fixed in passing:** `common.LoadStorageConfig` returned a zero-value
`StorageConfig` on *any* validation error, so a typo'd `lockStrategy` silently discarded an
already-successfully-parsed `TableNames`/`SkipPrefix` at the warn-and-continue call sites in
`postgres/driver.go` and `sqlite/driver.go`. Fixed to preserve every option parsed successfully
so far alongside the error.

Excluded from this phase (user directive): the `simple`-driver contention baseline (gap 5) and
running the fabtoken/dlog integration tests (part of gap 8); a fuzz target for the config
parser was also considered and skipped.

---

## Baseline → tip, end to end

| CERT-observed symptom | Mechanism | Fixed in | Verified by |
|---|---|---|---|
| One token (`f8a27fc4...`) re-collided for >6 min, 1296 times | no per-call memory of a lost race | #2398 | lock-attempt count −32% on the fixed baseline harness |
| 1 EUR payment could draw a 200 EUR token instead of a 1 EUR one | no size-based candidate ordering | #2399 (4b) | doc/algorithm change; not independently benchmarked — see gaps |
| Selector started races against tokens it had no chance to win | no lock-aware candidate query | #2399 (4a) | distinct tokens *conflicted* 296→212 |
| Settled transaction's tokens stayed locked and collided for minutes after | no unlock on success, only `OnError` + lease sweep | #2400 | unit tests only — contention harness is structurally blind to this (see gaps) |
| Postgres logs flooded with `zkat_token_locks_pkey` violations | lost races surfaced as server errors | #2402 | zero unique-violations on `LockBatch` path (always used by `sherdlock`); single-token path isolated separately |

Net effect on the one benchmark that spans Phases 2–4 (`TestHotTokenContention`, 3 replicas ×
100 requests): **total lock attempts 7468 → 3414 (−54%), total conflicts 7168 → 3114 (−57%)**,
distinct tokens actually contended 300 → 212. The aggregate *conflict rate* stays high (0.91)
throughout — expected, since the workload is deliberately shaped to have more concurrent
demand than tokens; what moves is wasted work and, as of #2402, whether that residual
contention is expressible as a server error at all.

---

## Further testable gaps

1. **[Closed in Phase 7] Phase 5 (lock release on settlement) has no contention-level regression test.**
   `TestHotTokenContention` deletes/spends tokens synchronously against the store and never
   goes through `finality.Listener`/TTX settlement, so it cannot detect a regression that
   reintroduces the "locked-until-lease-sweep" window. Worth adding a harness variant that
   drives real `ttx` settlement (or at least calls the finality listener) so a future change
   to `runOnStatus`/`applyFinalityLogic` is caught by the same yardstick used for Phases 3–4,
   not only by the four narrow unit tests.
2. **[Closed in Phase 7] No end-to-end timing assertion for the 6-minute-lock scenario itself.** All current
   coverage is either unit-level (`Unlock` fires once) or aggregate-conflict-rate. Nothing
   asserts *lock age* — e.g. "no lock outlives its transaction's terminal status by more than
   N seconds" — which is the literal quantity the CERT report flagged (`ListLocks`'s "leaked"
   flag from Phase 1 could be reused as the assertion primitive here).
3. **[Closed in Phase 7] Phase 4b's size-ordering claim ("1 EUR request no longer draws the 200 EUR token") is
   documented and code-reviewed but not covered by a dedicated regression test** reproducing
   the exact Xavier scenario (wallet with a small and a large token, small payment, assert the
   small token is selected). `TestHotTokenContention` exercises ordering only incidentally
   through its rotating-hot-token shape.
4. **[Closed in Phase 7] `token.SelectorRateLimited` hard-abort path is unexercised by the contention suite.**
   `selectInternal` treats a lock failure wrapping this sentinel as a hard abort rather than a
   skip-and-continue; there's no test establishing this differs correctly from
   `ErrTokenAlreadyLocked` under actual contention (only that the sentinel exists).
5. **[Excluded from Phase 7, user directive] `simple` driver has no equivalent contention baseline.** All of `TestHotTokenContention`,
   the blacklist, the anti-join, and the lock-strategy tests are `sherdlock`-only by design
   (per the docs, `simple` is unordered, no anti-join, releases all locks between retries).
   There is currently no data on how badly `simple` degrades under the same CERT-shaped
   workload, so operators choosing between drivers have no comparable numbers — worth at
   least running `TestHotTokenContention`-equivalent against `simple` once, even without
   fixing it, purely as a documented baseline (mirrors what #2397 did for `sherdlock`).
6. **[Closed in Phase 7] Rolling-deploy mixed-strategy scenario is untested.** Phase 6's docs explicitly flag that
   plain `insert`'s error-surfacing path only matters "for a rolling deploy where some
   replicas have not yet upgraded" — i.e. some replicas on `insert`, others on `skipLocked`
   against the same table concurrently. No test currently exercises that mixed-strategy
   window.
7. **[Closed in Phase 7 — turned out to be a doc bug, not a reachable misconfiguration]
   `leaseExpiry: 0` / `leaseCleanupTickPeriod: 0` was documented as an unguarded footgun.**
   In fact `Config.GetLeaseExpiry()`/`GetLeaseCleanupTickPeriod()` already coerce 0 to the
   default, so this is unreachable from YAML; docs corrected and a warning added for the
   in-process path that *can* still hit it. See the Phase 7 section above.
8. **[Excluded from Phase 7, user directive] Integration tests (fabtoken/dlog T1) are marked "not yet run" in PR #2399, #2400, and
   #2402's own test plans.** These are the closest thing to a true end-to-end reproduction of
   the CERT scenario (real Fabric network, real finality latency) and should be run — with
   `TEST_FILTER="T1"` — before merging the stack, per `AGENTS.md`'s own integration-test
   guidance.
9. **[Closed in Phase 7, tests only — no alerting runbook added] No metric/alert threshold tied to Phase 1's `LockConflicts` / `DistinctTokensAttempted`
   or Phase 6's `RoundTrips()`/`UniqueViolations()` counters.** They exist for diagnosis but
   there's no test or documented runbook asserting an operator would actually get paged before
   the next CERT-style incident reaches the same 85%-on-5-tokens shape.
