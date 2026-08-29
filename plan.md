# PerfLab — continuous performance regression infrastructure

## Goal

Build PerfLab: a daemon deployable on `dectrust22.vpc.cloud9.ibm.com` that benchmarks every
`main` commit and every open PR of Panurus (starting with the last 30 days of history),
detects performance regressions robustly against host noise, and publishes results as
JSONL/SQLite/CSV plus a static HTML dashboard. Report-only — no blocking checks, no PR
comments, no alerts. Full design: `/Users/adc/.claude/plans/design-an-infrastructure-to-wild-bubble.md`.

All code lives under `cmd/benchmarking/perflab/` (Python, reuses `bench_parse.py` and
`compare_benchmarks.py` from the parent `cmd/benchmarking/` directory).

## Implementation Progress

1. [x] `perflab/config.py`, `perflab/queue.py`, `perflab/cli.py` skeleton + `perflab doctor`
   — doctor logic in `runner.doctor`, wired into `cli.py cmd_doctor`, also writes `www/health.html`.
2. [x] `perflab/runner.py` Tier-1 solo path with capability probing
   — `run_solo`/`run_tier1_into`; `meta.json` records `parent` sha via `gitutil.parent_of` so
   historical `suspected` verdicts can auto-enqueue a confirmation job.
3. [x] `perflab/ingest.py` + JSONL/SQLite schema + pytest fixtures
   — `perflab/tests/` has 14 passing offline tests against synthetic + one real pre-`586d4f58`
   fixture; no `go test`/git/network required to run them.
4. [x] `perflab/verdict.py` — pairwise wrapper over `compare_benchmarks`, then MAD-based historical detection + confirmation enqueue
   — confirmation enqueue lives in `cli.py::_compute_and_print_verdict` (priority 5, keyed off
   `runs.parent_sha`).
5. [x] `perflab/report.py` + static dashboard (read `dataviz` skill first)
   — dependency-free inline-SVG charts (`svg_chart.py`) following the dataviz palette; pages:
   overview (sparklines + worst-delta-first), per-benchmark detail, PRs, runs, health, CSV export.
6. [x] `perflab/poll.py` + systemd units + `deploy/bootstrap.sh`; host tuning steps
   — `poll.py` (main/PR/backfill enqueue); all 5 systemd unit pairs written
   (`perflab-poll`, `perflab-worker`, `perflab-nightly`, `perflab-report`, `perflab-doctor`);
   `deploy/bootstrap.sh` does user creation, layout, governor/turbo tuning, unit install, nginx vhost.
7. [x] A/B interleaved path for PRs and confirmation jobs
   — `runner.run_ab`, wired into `cli.py cmd_run` / `_run_one_job`.
8. [x] Tier-2 wrapper around existing `run_benchmarks.py`
   — `tier2.py` (`run_tier2`/`ingest_tier2`); `cli.py cmd_nightly` enqueues it on `main` HEAD;
   `nightly` subcommand registered, `--suite` choices on `run`/`enqueue` include `tier2`.
9. [ ] Launch backfill (`perflab backfill --since 30d --stride 8`)
   — not started: requires a deployed installation on `dectrust22` (repo cloned there), which
   has not happened yet. `poll.backfill()`/`cli.cmd_backfill` are implemented and ready.
10. [x] `docs/development/perflab.md`; link from dlognogh benchmark doc and `AGENTS.md`
    — runbook written; linked from `dlognogh.md`'s "Related Documentation" and from AGENTS.md's
    Automation Runbooks list.

## Notes & Decisions

- Deployment: standalone systemd daemon + git polling, NOT a GitHub Actions self-hosted runner.
  No write GitHub token on the host (report-only).
- Two-tier suite: fast fixed-config Tier-1 per commit/PR (~15-25 min); full `run_benchmarks.py`
  sweep nightly on `main` HEAD only (Tier-2).
- Storage: append-only JSONL (source of truth) + SQLite (queryable) + generated static HTML
  dashboard + CSV export.
- Tier-1 must use plain `go test -bench` output only (machine-readable, stable across history);
  the custom `token/services/benchmark` runner is text-only (no JSON/CSV marshaller) and is
  confined to Tier-2 where scraping is acceptable.
- `-proof_type`/`-executor` flags don't exist before commit `586d4f58` and the package panics on
  bad values — runner must capability-probe per commit, not assume flags exist.
- Never compare across a change to the benchmark test files themselves (`bench_code_hash`) —
  label `harness-changed` instead of emitting a verdict.
