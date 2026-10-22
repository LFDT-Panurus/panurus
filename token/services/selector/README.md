# Token Selector

This package contains the token selector implementations.
The token selector is responsible to select tokens of a given type and for a given amount from a wallet.

## Available Implementations

- Simple
- Sherdlock (Default)

## Benchmarking
```go
go test -v -run=XXX -bench=BenchmarkSelectorSingle . -benchmem
go test -v -run=XXX -bench=BenchmarkSelectorParallel . -cpu=4,8,16,32,64,128,256,512 -benchmem

go test -ginkgo.v -ginkgo.trace .
```

### Benchmarking against Postgres

`BenchmarkSelectorPostgres` (`benchmark_postgres_test.go`) runs the selectors against a real
Postgres, built the way a node builds them: a generated `core.yaml` read by the FSC config
provider, the multiplexed Postgres driver, the `tokendb`/`tokenlockdb`/`ttxdb` store service
managers, and `sherdlock.NewService` / `simple.NewService` over a `token.ManagementService`.

```bash
make testing-docker-images                      # once: provides the postgres image
make benchmark-selector-postgres                # everything, 100 iterations per case
make benchmark-selector-postgres BENCH_FILTER='hot-wallet/' BENCH_TIME=500x
go test ./token/services/selector/ -run TestBenchPostgres -v   # check the harness itself
```

Without Docker the benchmark and the harness tests skip; with `PANURUS_REQUIRE_POSTGRES` set they
fail instead. They also skip under `-short`.

Environment: `PANURUS_SELECTOR_BENCH_MATRIX=full` runs the full sherdlock cross product
(fetcher x lock strategy x batch locking) in place of the one-axis sweep;
`PANURUS_SELECTOR_BENCH_LARGE=1` adds a 1M-token wallet to `wallet-size`;
`PANURUS_SELECTOR_BENCH_RETRY=5s` sets `token.selector.retryInterval` (the benchmarks default to 20ms
so contended cases do not spend their run asleep; 5s is the production default);
`PANURUS_SELECTOR_BENCH_LOGSPEC` overrides the package's default `error`-level logging (quieted so
the per-request INFO/WARN logs a contended Select emits cannot interleave mid-line with benchmark
result lines and corrupt them for `benchstat` - see `TestMain` in `benchmark_postgres_setup_test.go`),
e.g. `PANURUS_SELECTOR_BENCH_LOGSPEC=debug` to see per-request detail while debugging a failure.

Each case reports `selects/sec`, `p50-ms`/`p95-ms`/`p99-ms` of `Select` alone, `contended/op`
(requests that ended as locked or insufficient funds: expected under contention, not an error),
`lockRT/op` (Postgres lock round trips) and `uniqueViol/op`, followed by the sherdlock metrics.

See [docs/services/selector.md](../../../docs/services/selector.md#benchmarking-against-postgres)
for the configurations and workloads.

### Statistics with benchstat

A single run (`BENCH_TIME`'s default `-benchtime=100x`) tells you a case's numbers; it does not
tell you whether a difference between two cases is real or noise. `BENCH_COUNT` repeats every
case that many times in one `go test` invocation, and `benchstat` turns the repeats into a
median plus a confidence interval:

```bash
make benchmark-selector-postgres BENCH_FILTER='near-exhaustion/retry=(20ms|5s)/(sherdlock\+mixed.*|simple)' \
  BENCH_TIME=300x BENCH_COUNT=5 > near-exhaustion.txt
benchstat -col /retry near-exhaustion.txt
```

**Use one interleaved file and `-col`, not two separate files.** `benchstat` pairs rows by the
full benchmark name after stripping the trailing `-N` GOMAXPROCS suffix. Two cases whose names
differ by a `key=value` segment (e.g. `retry=20ms` vs `retry=5s`) are, by that rule, two
*different* rows, not two variants of the same row — running them into separate files and
handing `benchstat` both produces two unpaired rows with no comparison at all (confirmed by
running it, not assumed). `-col /retry` on a single file tells `benchstat` to project the
`retry=` segment into columns of one comparison table instead, which only works when every
variant's samples are interleaved within that one file, because `go test -count=N` orders a
case's own `N` repeats as a contiguous block — `-col` needs each variant to appear once per
`go test` invocation's interleaving pass for the comparison to align run-for-run rather than
stacking one case's five repeats against another's five with no correspondence between them.
Reuse the base/variant interleaving loop pattern from
`.github/workflows/token-validation-benchmark.yml`'s `run_one` if you need that explicitly.

**`-col` only reaches axes that live in `pgCase.name`'s `key=value` segments** (`retry=`,
`hold=`). `lockStrategy` and `maxConns` live inside the `+`-joined blob `pgConfig.name()`
produces (e.g. `sherdlock+mixed+skipLocked+batch+clean+conns10`) and are not separately
addressable by `-col` — renaming them into `key=value` segments would invalidate the existing
case names across every workload, which this benchmark declines to do. Compare those two by
filtering `BENCH_FILTER` to each value and reading the per-metric numbers by hand instead of
reaching for `-col` on them.
