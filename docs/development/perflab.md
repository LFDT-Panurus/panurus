# PerfLab — continuous ZK benchmark regression tracking

PerfLab is a standalone daemon that continuously benchmarks the `dlognogh` ZK
proof suite (see [`dlognogh.md`](../drivers/benchmark/core/dlognogh/dlognogh.md))
on a dedicated host, so that a slowdown shows up as a data point instead of a
surprise. It runs entirely outside CI: no GitHub Actions runner is registered
and the host never holds a write-scoped GitHub token. **Its verdicts are
report-only** — nothing in this system blocks a merge or posts a PR comment.

Source: `cmd/benchmarking/perflab/`. It is a self-contained Python package;
the only cross-import is `cmd/benchmarking/bench_parse.py` for the canonical
`go test -bench` line parser.

## What it does

- Every ~5 minutes: fetches `main`, benchmarks any new commit once (**Tier-1**,
  ~15–25 min), and benchmarks every open PR's head against its merge-base as
  an interleaved A/B run.
- Nightly at 03:19: runs the full `run_benchmarks.py` sweep (**Tier-2**)
  against current `main` HEAD.
- Publishes results as a static HTML dashboard, an append-only JSONL file, a
  SQLite database, and a CSV export — no server-side app needed to read them.
- Never fails a build. A regression is *reported*, never enforced.

## Layout on the host

```
/opt/perflab/
  src/                  pinned checkout of panurus — PerfLab code itself, updated by `git pull`
  repo/                 fetch-only clone of panurus — the commits/PRs being benchmarked
  worktrees/<run_id>/   ephemeral `git worktree add`, removed after each job
  runs/<run_id>/        raw artifacts (immutable): meta.json, <bench>.<tag>.txt, summary.md
  data/
    measurements.jsonl  append-only, one JSON object per metric per sample
    perflab.sqlite       runs / measurements / verdicts / canary_samples
    export/*.csv         flat CSV export of measurements
  www/                  generated static dashboard
  etc/perflab.yaml      config overrides (see perflab/config.py for every field)
  var/queue.sqlite       job queue + the single-run mutex
```

`$PERFLAB_HOME` overrides the root (default `/opt/perflab`); see `perflab/paths.py`.

## Why two tiers

Tier-1 must run unmodified against a month of history, so it uses only stock
`go test -bench -benchmem` output — the one output format that hasn't changed
in years. The `-proof_type`/`-executor` flags were only added in commit
`586d4f58` (`token/core/zkatdlog/nogh/v1/benchmark/flags.go`); older commits
reject them with `flag provided but not defined`. `perflab/capability.py`
probes each commit's binary for supported flags before building the real
command, rather than hardcoding a date cutoff — so the runner keeps working
if flags are added or removed again later.

Tier-2 always targets a recent commit, so it can safely depend on
`run_benchmarks.py`'s current CLI/CSV output as-is (`perflab/tier2.py`).

## Why every job runs a canary first and last

`BenchmarkActionMarshalling` (pure CPU, no crypto) runs before and after every
Tier-1 job. If it drifts more than `canary_band_pct` (default 5%) from its own
rolling historical median, or the before/after pair disagree with each other
by more than that same band, the whole run is marked `noisy` — still stored
and visible on the dashboard, but **excluded from verdicts and from the
historical baseline**. This is the main defence against reporting thermal or
scheduler noise as a regression; see `perflab/verdict.py::check_canary`.

## Why a "harness changed" verdict exists

A commit that edits `sender_test.go` changes what is being measured, not how
fast it runs. `perflab/gitutil.py::bench_code_hash` hashes the content of the
fixed list of benchmark test files (`runner.py::BENCH_TEST_FILE_GLOBS`) as of
a given sha. When a comparison spans a hash change, the verdict is
`harness-changed` instead of `regressed`/`improved` — the delta is real but is
not attributable to production code.

## Regression detection

**PRs and confirmation jobs (pairwise):** two worktrees, interleaved single
rounds (the same reasoning as `.github/workflows/token-validation-benchmark.yml`
— sampling both sides on the same silicon in the same thermal window makes the
delta a property of the diff, not the hardware). Neutral if the delta is
within `pairwise_delta_pct` (default 1.5%) *or* the sample ranges overlap.

**`main` (historical):** for each series, the baseline is the median of the
last `history_window` (default 15) non-noisy `main` runs, with the median
absolute deviation (MAD) as a robust scale estimate. A commit is `suspected`
when it exceeds both an absolute floor (`historical_floor_pct`, default 3%)
and `historical_mad_multiplier × 1.4826 × MAD` (the constant makes MAD a
normal-consistent estimator of standard deviation). A `suspected` verdict is
**never published as `regressed` directly** — it auto-enqueues a high-priority
(`priority=5`) confirmation job that re-runs that commit against its parent as
an interleaved A/B (`cli.py::_compute_and_print_verdict`). Only a confirmed A/B
delta becomes `regressed` on the dashboard.

Both paths treat a series present on only one side as `new`/`removed` rather
than a regression (matching `compare_benchmarks.py`'s existing behaviour).

## Job queue and priorities

`perflab/queue.py` is a SQLite table doubling as the single-run mutex —
exactly one benchmark job runs at a time, because measurement fidelity
depends on an otherwise-idle host. Lower priority number runs first:

| Job | Priority |
|---|---|
| manual `perflab enqueue` | 1 |
| confirmation (`SUSPECTED` follow-up) | 5 |
| PR head | 10 |
| new `main` commit | 100 |
| nightly Tier-2 | 50 |
| backfill | 200 (lowest — yields to everything above) |

## CLI

```
perflab doctor                                   # host health -> www/health.html
perflab run --sha SHA [--baseline SHA] [--suite tier1|tier2] [--dry-run]
perflab ingest --run-dir PATH
perflab verdict --run-id ID
perflab report                                   # regenerate www/
perflab poll                                     # enqueue new main commits + PR heads
perflab work [--once]                            # drain the queue, one job at a time
perflab backfill [--since-days N] [--stride N]
perflab enqueue --pr N | (--sha SHA [--baseline SHA]) [--suite tier1|tier2]
perflab nightly                                  # enqueue a Tier-2 sweep on main HEAD
```

## systemd units (`perflab/deploy/`)

| Unit | Schedule | Does |
|---|---|---|
| `perflab-poll.timer` | every 5 min | `perflab poll` |
| `perflab-worker.service` | always on, `Restart=always` | `perflab work` — holds the run mutex |
| `perflab-nightly.timer` | daily 03:19 | `perflab nightly` |
| `perflab-report.timer` | every 15 min | `perflab report` (also run after every ingest) |
| `perflab-doctor.timer` | hourly | `perflab doctor` |

All housekeeping units are pinned off the benchmark cores via
`CPUAffinity=0 1`; the benchmarks themselves run under `taskset -c
<bench_cpuset>` (default `2-31`, sized from `nproc` at bootstrap) so a busy
poller or report generator never perturbs a measurement.

## Deploying / bootstrapping a host

Run `cmd/benchmarking/perflab/deploy/bootstrap.sh` as root on the target host.
It is idempotent — safe to re-run after `git pull` in `/opt/perflab/src`:

- creates the `perflab` system user and the `/opt/perflab` layout;
- clones `src/` (PerfLab code) and `repo/` (the target repo being benchmarked);
- sizes and writes `bench_cpuset` into `etc/perflab.yaml`;
- sets the CPU governor to `performance` and disables turbo boost, to cut
  thermal drift between the two sides of an A/B;
- installs and enables the systemd units above;
- writes an nginx vhost serving `www/` on `127.0.0.1:8081` (reach it via
  `ssh -L 8081:localhost:8081 root@<host>`).

Requires `python3` with `pandas`, `pyyaml`, and `jinja2` already available
system-wide (PerfLab intentionally adds no Python dependencies beyond what
these hosts already carry for `run_benchmarks.py`). GitHub access uses only
`urllib.request` from the stdlib — no `requests`, no `gh` CLI.

## Adding a benchmark to Tier-1

Add a `BenchmarkSpec(package, name)` to `TIER1_BENCHMARKS` in
`perflab/config.py`. If it's a new package, `capability.py` will probe it the
first time it runs — no other change needed. Do **not** add it before the
canary entries change position; the canary must remain first and last in the
tuple (`run_tier1_into` uses the tuple order, matched with `is_canary`, to
detect the "before" vs "after" sample).

## Interpreting a verdict

- `neutral` — within noise.
- `improved` / `regressed` — pairwise delta outside the threshold and ranges
  don't overlap (PR/confirm jobs), or a `main` regression that survived
  confirmation.
- `suspected` — a `main` historical candidate awaiting its confirmation A/B;
  check back after the confirmation job (priority 5) drains, usually within
  minutes unless the queue is backed up.
- `harness-changed` — the benchmark test code itself changed between the two
  sides being compared; the delta is not attributable to production code.
- `new` / `removed` — the series only exists on one side.
- A run flagged `noisy` on the Runs page contributed no verdicts at all.

## Known Tier-2 limitations (not fixed, by design)

`run_benchmarks.py`'s `benchmark_results.csv` truncates latencies to integer
milliseconds and can misalign its header when appended across differing
benchmark matrices. `perflab/tier2.py::ingest_tier2` treats that CSV as an
artifact copied verbatim into `runs/<id>/`, not as the source of truth: it
parses the CSV defensively (skip any row/column that doesn't parse as a
number) into the same SQLite `measurements` table Tier-1 uses, which is what
the dashboard actually reads. Fixing the truncation/misalignment in
`run_benchmarks.py` itself is out of scope for PerfLab.

## Verification checklist

Offline (no host needed):

```bash
cd cmd/benchmarking && python3 -m pytest perflab/tests
perflab run --sha HEAD --suite tier1 --dry-run   # prints go test command lines only
```

On the host, before trusting the pipeline:

```bash
perflab doctor
perflab run --sha <recent main sha> --suite tier1
perflab run --sha <sha> --baseline <its parent> --suite tier1   # expect all-neutral
```

Run that last A/B two or three times; run-to-run flapping means the noise
floor (`thresholds.pairwise_delta_pct` / `canary_band_pct`) needs to be raised
before any verdict on this host can be trusted.
