# Selector Benchmark Report: Sherdlock vs Simple on Postgres

This report is the output of `BenchmarkSelectorPostgres` (see
[Benchmarking against Postgres](selector.md#benchmarking-against-postgres)). Its goal is to say
which selector configuration suits which workload, and whether the numbers are believable.

## Setup and how far to trust the numbers

- Apple M1 Max (10 cores), Docker with 6 CPUs / 8 GB, `postgres:16.4`. **The benchmark client and
  Postgres share the machine**, over loopback. Real deployments add network latency to every round
  trip, which makes round-trip count matter more than it does here.
- Retry interval 20 ms (benchmark default; production default is 5 s). Contended cases therefore
  look much better than production would.
- Pass 1: the full sweep at 100 iterations per case (97 cases). Pass 2: the contended and lifecycle
  workloads repeated at 300-1000 iterations, 2-3 runs each. The figures below are pass-2 means
  where available, otherwise pass 1.
- **Run-to-run noise is large**, often 1.5-2x on contended cases (e.g. hot-wallet baseline: 376/s at
  100 iterations, 237/s at 1000, range 210-266). Only differences larger than about 2x are
  treated as findings. Differences of 10-30% are noise. Findings between 1.3x and 2x are marked
  *suggestive* below: they rest on 2-3 runs and should be confirmed with `-count=5` and `benchstat`.
- The wallet-size, selection-size and fragmented tables are pass 1 only (single run, 100
  iterations). Only their large effects (5x and up) are reliable.
- Throughput is `selects/sec` for the whole operation (select, optional settle, release); latencies
  are for `Select` alone.

## Results

### Uncontended: latency floor (1 client, 10k wallet, select 100 tokens)

| Config | selects/s | p50 ms |
|---|---|---|
| sherdlock mixed / eager, batch | 158-194 | 4.2-4.9 |
| sherdlock mixed + onConflict | 129-196 | 4.0-4.8 |
| sherdlock mixed + skipLocked | 97-159 | 5.0-6.0 |
| sherdlock lazy | 51-59 | 16-18 |
| sherdlock single-lock (one `Lock` per token) | 9-15 | 67-91 |
| simple | 47-54 | 18-20 |

### Wallet size (1 client, select 100)

| Tokens | mixed | lazy | eager | simple |
|---|---|---|---|---|
| 1,000 | 279/s | 151/s | 230/s | 179/s |
| 100,000 | 172/s | **11/s** | 172/s | **5.9/s** |

### Selection size (1 client, 10k wallet, selects/s)

| Tokens per Select | batch (insert) | batch (skipLocked) | single-lock | simple |
|---|---|---|---|---|
| 1 | 601 | 668 | 666 | 64 |
| 10 | 581 | 570 | 156 | 66 |
| 100 | 214 | 162 | 15 | 55 |
| 500 | 67 | 41 | **2.6** (p99 1.1 s) | 17 |

### Fragmented wallet (power-law denominations, 1 client)

| Request | mixed | lazy | eager | simple |
|---|---|---|---|---|
| 50 | 346/s | 37/s | 364/s | 31/s |
| 500 | 56/s | 25/s | 54/s | 30/s |
| 5000 | **6.3/s** (p50 154 ms) | 5.5/s | 6.4/s | **33/s** |
| mix | 15.7/s (p95 165 ms) | 11.6/s | 15.8/s | 31/s |

### Hot wallet: 16 clients, one wallet, 1.5x supply (1000 iterations x3)

| Config | selects/s | p50 ms | p99 ms | lockRT/op | uniqueViol/op |
|---|---|---|---|---|---|
| sherdlock mixed, insert (default) | 237 | 56 | 304 | 10.5 | 0 |
| eager | 240 | 58 | 168 | 9.5 | 0 |
| lazy | 230 | 74 | 308 | 5.9 | 0 |
| onConflict | 249 | 54 | 225 | 10.1 | 0 |
| skipLocked | 257 | 54 | 223 | 11.0 | 0 |
| pool 50 connections | 146 | 100 | 314 | 8.8 | 0 |
| lease cleaner off | 274 | 51 | 224 | 9.5 | 0 |
| **single-lock** | **20** | 678 | 2604 | 156 | **56** |
| simple | 313 | 41 | 156 | n/a | n/a |

Distinct amounts (every client ranks candidates in the same order): default 387/s, onConflict 382,
skipLocked 346, eager/lazy 312-319, pool 50 290, **single-lock 30/s (101 unique violations/op)**,
simple 1458/s.

### Disjoint wallets (16 clients, one wallet each, 100 iterations)

sherdlock mixed 1028/s, eager 908, lazy 683, **simple 320**.

### Multi-replica: 3 nodes, one database, 24 clients (1000 iterations x3)

| Config | selects/s | p50 ms | p99 ms |
|---|---|---|---|
| insert (default) | 300 | 72 | 168 |
| onConflict | 320 | 68 | 150 |
| skipLocked | **191** | 118 | 215 |
| single-lock | **47** | 499 | 678 |

`simple` is not run here: its locks live in one process and cannot coordinate replicas.

### Settle (select, spend 5 or 100 inputs, re-create outputs, release)

| Case | mixed | lazy | eager | simple |
|---|---|---|---|---|
| 5 inputs, 1 client (/s) | 30-120 | 47-94 | 60-87 | 61-99 |
| 5 inputs, 16 clients (/s) | 415-456 | 370-510 | 411-479 | 407-500 |
| 100 inputs, 16 clients (/s) | 13-20 (p99 7.2 s) | 12-31 | 26-30 (p99 1.6-4.6 s) | 26-34 (p99 0.3-1.1 s) |

At 5 inputs all configurations are indistinguishable within noise. At 100 inputs and 16 clients
the pool of 10 is the bottleneck for everyone (settlement holds a connection ~250 ms); sherdlock
mixed has the worst tail. `simple` reports 7-13% of requests as `contended` (a token it picked was
spent meanwhile); sherdlock reports none.

### Near-exhaustion (supply for 10 of 16 clients, 3 ms hold)

| Config | selects/s | p50 ms | p99 ms | contended/op |
|---|---|---|---|---|
| sherdlock mixed | 38 | 352 | 1280 | 0.05 |
| sherdlock lazy | 43 | 309 | 924 | 0.11 |
| sherdlock eager | 24 (13-34) | 559 | 3035 | 0.06 |
| simple | 150 | 51 | 1631 | 0 |

**Retry interval: sherdlock closes most of its gap to `simple` at the production 5s interval,
but not enough to flip the ordering.** `-benchtime=300x -count=5` on `retry=20ms` vs `retry=5s`
(`benchstat -col /retry`, p=0.008 throughout, statistically significant):

| Config | retry=20ms selects/s | retry=5s selects/s | Δ |
|---|---|---|---|
| sherdlock mixed | 37.91 | 62.37 | +64.5% |
| simple | 438.80 | 65.92 | -85.0% |

At 20ms simple leads by ~11.6x; at the production 5s default the two land at 62.37/s vs
65.92/s — a ~1.06x gap, essentially a tie, simple still (barely) ahead. The mechanism: sherdlock
pays for *more* backoff dead time per failed `Select` than simple at a long interval (its
`StubbornSelector` loop runs 4 attempts and sleeps after all 4, including a wasted sleep after
the attempt that already failed for good, worst case `(numRetries+1) * retryInterval`; simple's
loop runs 3 attempts, sleeps after only 2, worst case `numRetries * retryInterval`) — so
sherdlock's *relative* slowdown from a longer interval is smaller than simple's, not because
sherdlock's backoff is cheaper in absolute terms, but because simple is giving up more of its
20ms lead to the same fixed retry-interval math. The gap stays under the 2x bar this report
uses to generalize a finding, so it is reported here as an observation specific to this
workload's balance-check/retry-cost shape, not promoted into the cross-workload recommendation
table below — see the `hot-wallet` cross-check immediately following, which does not reproduce
the effect at all.

**Cross-check on `hot-wallet` (16 clients, 1.5x supply, no retry needed in the common case):**
the same `retry=20ms`/`retry=5s` comparison (`-benchtime=100x -count=5`) shows **no significant
effect** on either driver (sherdlock 223.9→205.2/s, p=1.000; simple 461.6→483.8/s, p=0.421 —
neither passes the p<0.05 bar), and the ~2x sherdlock-vs-simple gap is unchanged at either
interval. This is expected: `hot-wallet`'s supply covers 1.5x demand, so most requests succeed
without exhausting retries and spending backoff time at all, unlike `near-exhaustion`'s 0.625x
ratio where coming up short is common. The two workloads disagree, which is exactly the signal
this report's reporting rule is designed to catch: a retry-interval effect real enough to move
near-exhaustion's numbers by double digits is not a property of the selectors in general, it is
a property of *how often a workload actually pays for backoff*.

### Stale locks (30% of wallet locked by a crashed replica)

| | selects/s | stale locks left |
|---|---|---|
| cleaner on | 165-196 (reruns; the 47-78 of the first two passes did not reproduce, see sanity check 3) | 0 |
| cleaner off | 163-180 | 3000 (never released) |

## Findings

1. **Batch locking is not optional once a Select takes more than a few tokens.** Single-lock costs
   one round trip per token, so its penalty grows with the selection size: none at 1 token (666/s
   vs 601/s), 3.7x at 10, 14x at 100, 26x at 500. It is 12x slower on the single-node hot wallet
   and 6x slower multi-replica. Under contention it also produces tens of unique-violation retries
   per operation. On a network database the gap widens.
2. **Fetcher: `mixed` (default) or `eager`; `lazy` only for tiny wallets.** `lazy` re-reads the
   spendable set per Select: 15x slower at 100k tokens, 3-10x slower on single-client workloads.
   Under 16-way contention the fetchers are within noise. `eager` and `mixed` track each other
   because `mixed` serves from the eager cache whenever it can.
3. **Lock strategy: keep `insert` (or `onConflict`); `skipLocked` looks like a loss (suggestive).**
   `onConflict` is equivalent to `insert`. `skipLocked` is equal in the single-node hot wallet but
   35% slower in multi-replica, 20-40% slower at 100-500 tokens per Select, and slower
   uncontended; the direction is consistent across workloads but each gap is within 1.3-1.6x.
   Its one attraction (no deadlock potential) was not needed. The zero unique violations for
   `insert` is structural, not evidence of absent races: `LockBatch` never issues a plain INSERT
   (it uses `ON CONFLICT DO NOTHING` under every strategy), and the counter only moves on the
   single-token `Lock` path. Lost races under batch locking show up as `lockRT/op` and
   lock conflicts instead.
4. **Fixing `LockBatch`'s insertion order removed a deadlock, not just reduced contention.**
   Before the fix, a multi-row claim inserted lock rows in whatever order the query planner
   chose, which varies with table size and window length; since candidates are shuffled within
   equal-amount runs (the bucketed shuffle), concurrent claimants listed overlapping windows in
   different orders, and inserting the same keys in different orders deadlocks — detected only
   after Postgres's `deadlock_timeout` (1s default), costing that second plus a retry per
   occurrence. Measured on the `multi-replica` workload with `insert`: 26-56 selects/s and a p95
   of 1.2-2.3s before fixing the order, about 280-360 selects/s and a p95 of about 110-145ms
   after. `skipLocked` did not deadlock in the overlapping-claims test with or without the
   ordering, so the fix (inserting in a fixed `(tx_id, idx)` order under every strategy) costs a
   sort of at most `maxBatchWindow` (256) rows to keep the three strategies' behaviour the same.
5. **Pool size: bigger was not better where measured (suggestive).** 50 connections were 40%
   slower than 10 on the hot wallet (more concurrent backends fighting over the same rows). Pool
   size was only varied in that one workload, so nothing is known about the settle case, where
   the pool of 10 is the bottleneck.
6. **Installing an overtaken cache snapshot rather than discarding it matters under sustained
   invalidation.** The `cachedFetcher` compares its invalidation count before and after each
   refresh query; a snapshot whose refresh was overtaken by an invalidation is installed but
   left marked stale, so the next reader pays for a refresh that reads *after* the invalidation
   instead of looping on the same discarded attempt. On a wallet settling continuously under 16
   clients, a discard-on-overtake rule left every refresh perpetually overtaken before it
   finished — 3 snapshots installed against 1,691 discarded in 100 selections — serving a
   snapshot full of long-spent tokens and failing 74-78% of selections with
   `SelectorSufficientButLockedFunds` on a wallet that could pay; with the install rule in place,
   none failed and throughput was five to ten times higher. `lazy` reads through and is
   unaffected either way.
7. **Sherdlock vs simple.**
   - Sherdlock wins when the wallet is large (30x at 100k tokens: simple reads the whole wallet per
     Select), when many wallets are served in parallel (3x on disjoint wallets), for small and
     medium selections, and always for multi-node deployments (simple cannot coordinate).
   - Simple wins on large selections from fragmented wallets (request 5000: 33/s vs 6/s, since
     sherdlock writes thousands of durable lock rows in 17 round trips), under heavy single-wallet
     contention on one node (1.3x on a hot wallet, which is within noise; 3.7x with distinct
     amounts) and when demand exceeds supply (4x), because in-process locking costs no round
     trips. These wins disappear when the node is not alone.
8. **The lease cleaner is a correctness feature, not a performance one.** With it off, crashed
   replicas' locks stay forever (3000 of 10000 tokens hidden). It shows no cost in the hot-wallet,
   uncontended or settle runs.
9. **Retry interval narrows the sherdlock-vs-simple gap on near-exhaustion, but the effect does
   not generalize (workload-specific, not a cross-workload recommendation).** At the production
   5s `retryInterval` default, near-exhaustion's sherdlock-vs-simple gap collapses from ~11.6x
   (at the benchmark's 20ms default) to a ~1.06x near-tie (p=0.008, n=5, statistically
   significant) — see the near-exhaustion section above for the full numbers and mechanism.
   The same comparison on `hot-wallet` shows **no significant effect** (p=1.000 and p=0.421) and
   no change to its ~2x gap. Per this report's own rule for generalizing a finding beyond a
   single workload, this stays an observation about near-exhaustion's balance-check/retry-cost
   shape specifically, not a recommendation to change `retryInterval` for sherdlock-vs-simple
   choice in general.

### Recommendation by workload

| Workload | Recommended | Avoid |
|---|---|---|
| Single wallet, small/medium selections, one node | sherdlock default (mixed, insert, batch, cleaner on) | lazy, single-lock |
| Many wallets / large wallets | sherdlock mixed or eager | simple, lazy |
| Multiple nodes on one database | sherdlock default; `onConflict` is equally good | skipLocked, single-lock, simple |
| Wallets fragmented into dust where requests need hundreds to thousands of inputs | Fix the fragmentation (consolidate); otherwise accept sherdlock's cost or use simple on a single node | single-lock |
| Heavy single-node hot wallet where throughput matters most | simple is faster (partly because its in-process locker costs no round trips, see sanity check 6), but only on a single node with no replicas or failover: it cannot coordinate across processes | |
| Long transactions holding the pool | Keep settlement short. Pool size was not varied for this workload; measure before changing it | raising the pool on the strength of the hot-wallet result (it got worse there) |

## Sanity check of the results

Checks that passed:

- **Throughput matches latency** (Little's law, using the pass-2 table figures): uncontended
  1/p50 ≈ selects/s; hot wallet 16 clients / 56 ms ≈ 286/s (measured 237); multi-replica 24 clients
  / 72 ms ≈ 333/s (measured 300). The model overestimates by 10-20%, which is expected since
  p50 understates the mean when the tail is long.
- **Round-trip counters match the design**: batch = 1 round trip (2 above 256 tokens), single-lock =
  exactly the selection size (100, 500), fragmented 5000 = 17 (≈ 4,300 tokens / 256-token window).
- **Single-lock's unique violations are real**: zero in every batch case, 55-130/op only in
  single-lock under contention.
- **Settle** reproduces the p99 of seconds described in the doc for 100 inputs / 16 clients.
- **Simple has no round trips for locks** (`lockRT=0`) and loses on wallet size, as its full
  per-Select read predicts.

Things that do not make sense, or do not match the documentation:

1. **`contended/op` is 0 for the hot wallet, multi-replica and disjoint workloads.** Supply is 1.5x
   demand and Selects release immediately, so these workloads measure lock *conflicts and retries*
   (lockRT up to 10/op) but never a failed request. That is as designed, but the numbers should not
   be read as "no contention".
2. **Near-exhaustion fails far fewer requests than first assumed.** The workload was designed on
   the premise that about a third of requests find too little free; measured `contended/op` is
   0.03-0.11 for sherdlock and 0 for simple, because retries with backoff absorb most shortages.
   The workload mostly measures retry cost (sherdlock p50 280-560 ms, 40 lock round trips/op), not
   failure. The code comment and the benchmark docs now say so; a workload that forces failures
   would need a longer hold or less supply.
3. **Cleaner-on was 2-3x slower than cleaner-off in the stale-locks case (47-78 vs 163-180/s):
   investigated, not reproducible, so it was a measurement artifact.** Re-running with the same
   command (`-benchtime=300x -count=3`, both cases) gave cleaner-on 172-192/s and cleaner-off
   168-170/s; four isolated reruns gave 165-193/s for both. Postgres bloat was ruled out with a
   probe that reproduced the setup (10k tokens, 3000 locks planted an hour old):
   - With the cleaner on, the 3000 deleted rows leave 3000 dead tuples and the lock table is empty
     afterwards; autovacuum had not run. Throughput was 164/s before and 192/s after
     `VACUUM ANALYZE`, a gain within noise.
   - Dead tuples build up in both configurations from ordinary lock/unlock churn (15-20k dead
     tuples after 200 selects), so they do not distinguish the cleaner.
   - `ANALYZE` on the tokens table made no difference either.
   The low figures came from the first two passes of this run only. The cause is not identified
   (a disturbed machine is a guess, not a finding); the cleaner showed no measurable throughput
   cost in any reproducible run.
4. **Settle with one client and 5 inputs varies 4x between passes** (mixed 120/s then 30/s; eager
   87 then 60). Settlement churns token IDs in a 7-token wallet and the result depends on timing;
   no configuration conclusion should be drawn from it.
5. **Pass-1 vs pass-2 hot-wallet throughput differs by 35%** (376 vs 237/s) from the same code.
   Longer runs expose Postgres table growth in the lock table and, on a laptop, thermal and
   scheduling effects. Absolute numbers are not portable; only the ordering of configurations is.
6. **Simple's fast hot-wallet and distinct-amounts results (313/s, 1458/s) are partly an artifact
   of the single process**: the in-process locker is faster than any database lock, which is
   exactly what it gives up in multi-node deployments. This should not be read as simple being
   "faster" in general.
7. **Eager is 2x worse than the others in near-exhaustion (24/s, range 13-34)** but equal in every
   other workload. Variance is too large to say whether this is a real effect.

## Gaps in coverage

- **Retry interval.** Runs use 20 ms; production defaults to 5 s. Every contended result favours
  retrying, and the production setting was never measured. At 5 s, contended latency would be
  dominated by the interval, which may change the ranking of configurations that differ in how
  often they must retry.
- **Network latency.** The database is on loopback. The report's central cost, round trips, is
  therefore under-priced. Adding a latency proxy (0.5, 1, 5 ms) would turn the round-trip counters
  into predictions.
- **Lock hold time.** Zero everywhere except near-exhaustion (3 ms). Real transactions hold locks
  for the duration of endorsement collection, which changes the hot-wallet and multi-replica
  picture.
- **Not reported:** eager-cache staleness across replicas (`stale_candidates_total`), eager-cache
  memory at 100k tokens, Postgres CPU, and lock-table and dead-tuple growth over long runs.
- **Not covered:** multiple token types, lease-expiry timing, and Postgres configurations other
  than `postgres:16.4` with its default settings.
- **Postgres CPU is not isolated:** client and database share the machine's cores, so a
  CPU-bound result cannot be attributed to either side.
- **Pool size** was varied in the hot-wallet workload only.

## Relation to the in-memory benchmarks

`benchmark_test.go` (`BenchmarkSelectorSingle`, `BenchmarkSelectorParallel`) measures a different
thing and its numbers must not be compared with this report's:

| | `benchmark_test.go` | this report |
|---|---|---|
| Storage | `MockQueryService`, pre-sorted in-memory cache | Postgres 16 |
| Locking | in-process locker for sherdlock and simple alike | lock table (sherdlock) or in-process (simple) |
| Batch locking | `benchBatchLocker` loops single `Lock` calls: no round-trip saving | real `LockBatch` |
| Retry backoff | 2 ms | 20 ms |
| Measures | selector CPU and algorithm cost | round trips, contention, wallet size, multi-replica |

The in-memory suite is the better regression signal for selector logic. It cannot show the
wallet-size gap, multi-replica behaviour, stale locks, the cleaner, or the batch-vs-single saving
(its `sherdlock+batchlock` setting does not save round trips). Its parallel benchmark also runs
`clients x GOMAXPROCS` goroutines, so its client counts differ from this report's.

Measured on the same machine (M1 Max, 2026-10-20, one run each: `-benchtime=50x` for Single and
`100x` for Parallel, so treat them as orders of magnitude only). The first attempt at
`-benchtime=2000x -count=3` was abandoned: the lazy 10k-token setting alone ran for over four
minutes.

Wallet sizes come from `testutils.NumTokensPerWallet` = 1,000,000, not 10k: Single runs on 1M
tokens, the `parallelism` case on 10M, and the contention cases on 1,000.

`BenchmarkSelectorSingle` (1 client, 1M tokens):

| Setting | selects/s | ms/op |
|---|---|---|
| sherdlock (lazy) | 8.7 | 115 |
| sherdlock + lock | 9.3 | 107 |
| sherdlock + batchlock | 9.5 | 105 |
| sherdlock + cached | 64 | 15.6 |
| sherdlock + mixed | 64 | 15.7 |
| selector + nolock (simple) | 27,254 | 0.037 |
| selector + lock (simple) | 15,998 | 0.063 |

`BenchmarkSelectorParallel`:

| Setting | selects/s |
|---|---|
| sherdlock | 23 |
| sherdlock + lock | 24 |
| sherdlock + lock + parallelism (10 clients, 10M tokens) | 0.36 |
| selector + nolock (simple) | 31,898 |
| selector + lock (simple) | 1,709 |

The three contention settings (`sherdlock+lock+contention`, `+stubborn`, `batchlock+contention`)
ran to completion but their throughput lines were not captured: the output was interleaved with
sherdlock WARN/INFO log lines ("Exceeded max number of immediate retries", "Lost lock race").
Rerun with `FABRIC_LOGGING_SPEC=fatal` (8 clients, 1,000 tokens, `-benchtime=100x`, single run):

| Setting | selects/s | contended/op | lock_conflicts_total |
|---|---|---|---|
| sherdlock+lock+contention (no backoff) | 33.0 | 86% | 1,079,087 |
| sherdlock+lock+contention+stubborn (2 ms backoff) | 8.6 | 83% | 4,217,888 |
| sherdlock+batchlock+contention | 105.4 | 86% | 863,680 |

All three contend on 83-86% of requests, as the workload (8 clients, 1,000 tokens, no retry
backoff for the first case) is designed to. Two things stand out:

- **`+stubborn` is slower than its no-backoff counterpart**, not faster: 8.6/s vs 33.0/s, with
  4x the lock conflicts. The 2 ms backoff does not resolve contention here, it multiplies it:
  each retry re-attempts the same hot, still-contended prefix instead of waiting it out, so more
  retries just mean more conflicts per eventual success. This is the in-memory locker's
  zero-latency round trip working against the backoff's purpose; it should not be read onto the
  Postgres retry-interval question in "Gaps in coverage" below, where a round trip costs real
  time and 5 s is long enough to let a hot prefix clear.
- **Batch locking is ~3x faster than the single-lock path under contention** (105.4/s vs
  33.0/s) with slightly fewer lock conflicts, consistent with finding 1's Postgres result that
  batch locking's advantage grows with contention, not just with selection size.

How to read this:

- **The ordering is the opposite of the Postgres report.** Simple is 400-3000x faster here, while
  on Postgres it is slower on large wallets and comparable on small ones. This follows from the
  setup: a mock store with a pre-sorted cache, and an in-process locker shared by both selectors
  (which is also why report sanity check 6 applies with extra force).
- **`benchBatchLocker` loops single `Lock` calls**, so `sherdlock+batchlock` (9.5/s) is not a
  measurement of batch locking; it cannot show the round-trip saving.
- **The lazy cost is mostly explained by wallet size.** Lazy sherdlock reads the whole candidate
  list per Select (`ReadAllPointers`): about 110 ms over 1M tokens is roughly 110 ns per token of
  allocation and conversion, with no database involved. The simple selector stops after about
  100 tokens of the pre-sorted cache, which accounts for most of the 400-3000x gap. This also
  explains why the `-benchtime=2000x` run was abandoned (2000 x 115 ms is about 230 s per case).
- **The cached/mixed figure of about 15 ms per Select over 1M tokens is `NewPermutation`'s full-wallet
  shuffle, confirmed by profiling.** A CPU profile of `sherdlock+cached` (`pprof`, `-cpuprofile`)
  attributes the time inside `Select` to `bucketedIterator.NewPermutation`
  (`sherdlock/fetcher.go`), which `rand.Shuffle`s the *entire* cached candidate list on every
  cache hit, not just the tokens a Select needs. An isolated microbenchmark of that function
  confirms the cost and its scaling:

  | wallet size | uniform amounts (one bucket) | distinct amounts (every token its own bucket) |
  |---|---|---|
  | 1,000 | 9.8 µs | 5.7 µs |
  | 10,000 | 93.8 µs | 46.6 µs |
  | 100,000 | 1.10 ms | 0.57 ms |
  | 1,000,000 | 32.7 ms | 7.6 ms |

  At 1M tokens with uniform amounts (this benchmark's actual setup: every token has quantity 1,
  so the whole wallet is one shuffle bucket), the shuffle alone costs ~33 ms, the same order of
  magnitude as the measured 15 ms per-Select figure and consistent with it being the dominant
  cost rather than bucketing or map lookups. Cost is O(wallet size), not O(selection size): a
  100-token Select pays to shuffle however many tokens the wallet holds. It is not a correctness
  bug - the anti-hot-spot comment on `bucketedIterator` explains why the shuffle exists - but it
  is a real optimization target for large eagerly-cached wallets, and the ~4x gap between the
  uniform and distinct columns is explained by the per-bucket shuffle design: distinct amounts
  put every token in its own one-element bucket, which is cheaper to "shuffle" than one
  million-element bucket.
- **The 0.36/s `parallelism` result** (10 clients, 10M tokens, lazy) is the same lazy-scan cost
  at 10x the wallet size, with `clients x GOMAXPROCS` goroutines competing for it.
- Do not read any of these as a statement about production behaviour.

## Status of review findings

The report was reviewed against `benchmark_test.go` and the Postgres harness. Resolution:

| # | Finding | Status |
|---|---|---|
| 1 | "10-50x in every workload" overstated for single-lock | Fixed: penalty now stated per selection size |
| 2 | Conclusions drawn from sub-2x differences | Fixed: marked *suggestive*; pass-1-only tables noted |
| 3 | Pool-size recommendation unsupported | Fixed: advice removed, scope of measurement stated |
| 4 | Stale-locks table showed retracted figures | Fixed: reruns shown, cause marked unidentified |
| 5 | Near-exhaustion comment and doc claimed a third fail | Fixed in code comment, `selector.md` and this report; workload itself unchanged |
| 6 | Zero unique violations read as "no races" | Fixed: explained as structural (`LockBatch` never plain-INSERTs) |
| 7 | 20 ms retry, loopback DB, zero hold time | Documented under Gaps in coverage; **not measured** |
| 8 | Sherdlock in-memory CPU cost | Fixed: lazy cost explained by the 1M-token wallet; the 15 ms cached/mixed cost identified as `NewPermutation`'s full-wallet shuffle, confirmed by profiling and a standalone benchmark |
| 9 | In-memory wallet sizes stated as 10k/100k | Fixed: 1M/10M per `testutils.NumTokensPerWallet` |
| 10 | Little's law check mixed pass-1 and pass-2 figures | Fixed: recomputed from the table |
| 11 | Hot-wallet recommendation row unqualified | Fixed: single node only, no failover |

Remaining work: a 5 s retry run, a latency proxy, a nonzero hold time on hot-wallet and
multi-replica, and `-count=5` with `benchstat` on the sub-2x findings. (The in-memory sherdlock
cached/mixed path is profiled above, and the three in-memory contention settings have been
rerun with logging quieted; see "How to read this" above for both.)

A full report re-verification (2026-10-20, same machine) reran every workload at pass-1 scale
(100 iterations) plus hot-wallet, near-exhaustion and stale-locks at pass-2 scale
(300-1000 iterations, 3-8 runs). Every figure reproduced within the report's own stated noise
bands, including the hot-wallet/simple gap (confirmed 1.5-2x across repeated runs, not just the
1.3x single sample the report calls "within noise" - still inside the *suggestive* 1.3-2x band
the report already uses to hedge this finding) and the stale-locks non-reproduction (cleaner=on
105-167/s vs cleaner=off 103-183/s across 8 runs each, overlapping ranges, confirming sanity
check 3's "measurement artifact" conclusion). No discrepancy was found beyond the two items
closed out above.

## Reproducing

```bash
PANURUS_REQUIRE_POSTGRES=1 make benchmark-selector-postgres                       # pass 1
go test ./token/services/selector/ -run '^$' -benchtime=1000x -count=3 \
    -bench 'BenchmarkSelectorPostgres/hot-wallet'                                  # pass 2 style
```

For stronger conclusions: run on a dedicated machine with Postgres on its own host, use
`PANURUS_SELECTOR_BENCH_RETRY=5s` for production-like retry behaviour, and `-count=5` with
`benchstat`.

**The retry-interval finding above** (near-exhaustion's sherdlock-vs-simple gap closing from
~11.6x to ~1.06x at the production 5s interval) was produced this way — one interleaved file,
`benchstat -col /retry` to project the axis into comparison columns:

```bash
PANURUS_REQUIRE_POSTGRES=1 go test ./token/services/selector/ -run '^$' -benchtime=300x -count=5 \
  -bench 'BenchmarkSelectorPostgres/near-exhaustion/retry=(20ms|5s)/(sherdlock\+mixed.*|simple)' \
  -timeout 90m > near-exhaustion.txt
benchstat -col /retry near-exhaustion.txt
```

See [token/services/selector/README.md](../../token/services/selector/README.md#statistics-with-benchstat)
for why this must be one interleaved file rather than two separate ones, and for the
`PANURUS_SELECTOR_BENCH_LOGSPEC` knob this relies on to keep the output `benchstat`-clean.

**`BenchmarkNewPermutation`** (`token/services/selector/sherdlock/fetcher_bench_test.go`)
measures the cost of `bucketedIterator.NewPermutation` — the shuffle
[the ordering and sufficiency window mechanism](selector.md#ordering-and-the-sufficiency-window)
runs on every cache hit under the `eager`/`mixed` fetcher strategies, regardless of how few
tokens the request actually needs:

```bash
go test ./token/services/selector/sherdlock/ -run '^$' -bench BenchmarkNewPermutation -benchmem -count=5
```

At 1M cached tokens: ~14-18ms per call when every token shares one amount (the worst case —
the shuffle sees the whole list as a single bucket), ~6ms when every token has a distinct
amount (the common case for a realistic wallet — every bucket has size 1, so the cost is
almost entirely the slice copy, not the shuffle itself); both cases make exactly 2 allocations
regardless of size. At 1k tokens the same two cases cost ~11μs and ~7μs respectively — the
cost is linear in cache size across the whole range measured (1k/10k/100k/1M). This is a real,
measurable cost every deployment running the default `mixed` strategy pays, not a hypothetical
one; see [issue #2017](https://github.com/LFDT-Panurus/panurus/issues/2017) for the broader
amount-aware-selection angle this falls under. No optimization of `NewPermutation` has been
made as of this writing.
