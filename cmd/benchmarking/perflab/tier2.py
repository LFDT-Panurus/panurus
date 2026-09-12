"""Tier-2: nightly deep sweep, wrapping `run_benchmarks.py` unchanged.

Tier-2 does not need capability probing or a bespoke parser -- it always runs
against a recent commit (never the multi-year backfill window Tier-1 must
survive), so it is safe to depend on `run_benchmarks.py`'s current CLI and
CSV/PDF output as-is. PerfLab treats that CSV as an artifact, not as its
source of truth (see docs/development/perflab.md#tier-2-limitations): the
authoritative copy is the SQLite `measurements` rows this module inserts
directly by parsing `run_benchmarks.py`'s own `benchmark_results.csv`.
"""

from __future__ import annotations

import csv
import json
import os
import platform
import subprocess
from datetime import datetime, timezone
from pathlib import Path

from . import gitutil
from .config import PerflabConfig
from .paths import Layout
from .runner import BENCH_TEST_FILE_GLOBS, _now_iso, _run_id


def run_tier2(cfg: PerflabConfig, layout: Layout, sha: str, *,
              kind: str = "main", pr_number: int | None = None,
              timeout: int = 6 * 3600) -> str:
    run_id = _run_id(f"{kind}-tier2", sha)
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True, exist_ok=True)
    worktree = layout.worktrees / run_id
    bench_dir = worktree / "cmd" / "benchmarking"
    started = _now_iso()
    try:
        gitutil.add_worktree(layout.repo, sha, worktree)
        cmd = [
            "taskset", "-c", cfg.bench_cpuset, "python3", "run_benchmarks.py",
            "--proof_type", cfg.tier2.proof_type, "--executor", cfg.tier2.executor,
            "--cpus", cfg.tier2.cpus, "--duration", cfg.tier2.duration,
            "--count", str(cfg.tier2.count),
        ]
        env = dict(os.environ, TOKENSDK_ROOT=str(worktree))
        proc = subprocess.run(cmd, cwd=bench_dir, env=env, capture_output=True,
                               text=True, timeout=timeout)
        (run_dir / "tier2.stdout.log").write_text(proc.stdout)
        (run_dir / "tier2.stderr.log").write_text(proc.stderr)
        if proc.returncode != 0:
            raise RuntimeError(f"run_benchmarks.py failed (rc={proc.returncode}): "
                                f"{proc.stderr[-2000:]}")

        for name in ("benchmark_results.csv", "benchmark_IOstats.csv", "benchmark_results.pdf"):
            src = bench_dir / name
            if src.exists():
                (run_dir / name).write_bytes(src.read_bytes())
        for log_dir in bench_dir.glob("benchmark_logs_*"):
            dest = run_dir / log_dir.name
            dest.mkdir(exist_ok=True)
            for f in log_dir.iterdir():
                (dest / f.name).write_bytes(f.read_bytes())

        meta = {
            "run_id": run_id, "kind": kind, "suite": "tier2", "commit": sha,
            "baseline": None, "pr_number": pr_number,
            "started_at": started, "finished_at": _now_iso(),
            "host": platform.node(), "nproc": os.cpu_count(),
            "go_version": gitutil.go_version(worktree),
            "bench_code_hash": gitutil.bench_code_hash(layout.repo, sha, BENCH_TEST_FILE_GLOBS),
            "cpuset": cfg.bench_cpuset,
            "params": {"proof_type": cfg.tier2.proof_type, "executor": cfg.tier2.executor,
                       "cpus": cfg.tier2.cpus, "duration": cfg.tier2.duration,
                       "count": cfg.tier2.count},
        }
        (run_dir / "meta.json").write_text(json.dumps(meta, indent=2))
        return run_id
    finally:
        gitutil.remove_worktree(layout.repo, worktree)


def ingest_tier2(conn, jsonl_path: Path, run_dir: Path) -> str:
    """Parse `benchmark_results.csv` -- known to truncate latencies to integer
    ms and to misalign its header across differing matrices (see module
    docstring) -- into `measurements` rows, tolerating both quirks by reading
    it as a plain dict-per-row CSV and skipping any row missing a numeric
    value rather than trusting column position."""
    meta = json.loads((run_dir / "meta.json").read_text())
    run_id = meta["run_id"]
    conn.execute("DELETE FROM measurements WHERE run_id=?", (run_id,))
    conn.execute("DELETE FROM runs WHERE run_id=?", (run_id,))

    params_json = json.dumps(meta.get("params", {}), sort_keys=True)
    conn.execute(
        "INSERT INTO runs (run_id, kind, suite, commit_sha, pr_number, host, go_version, "
        "bench_code_hash, started_at, finished_at, status) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
        (run_id, meta["kind"], "tier2", meta["commit"], meta.get("pr_number"),
         meta.get("host"), meta.get("go_version"), meta.get("bench_code_hash"),
         meta["started_at"], meta.get("finished_at"), "ok"),
    )

    csv_path = run_dir / "benchmark_results.csv"
    jsonl_lines = []
    if csv_path.exists():
        with csv_path.open(newline="") as f:
            reader = csv.DictReader(f)
            for sample_idx, row in enumerate(reader):
                bench = row.get("benchmark") or row.get("bench") or "unknown"
                for metric_col, metric_name in (
                    ("tps", "tps"), ("lat-p95", "p95"), ("lat-avg", "avg"), ("lat-std", "stddev"),
                ):
                    raw = row.get(metric_col)
                    if raw is None or raw == "":
                        continue
                    try:
                        value = float(raw)
                    except ValueError:
                        continue
                    conn.execute(
                        "INSERT INTO measurements (run_id, side, bench, params_json, workers, "
                        "cpu, metric, sample_idx, value) VALUES (?,?,?,?,?,?,?,?,?)",
                        (run_id, "solo", bench, params_json,
                         int(row.get("goroutines", 1) or 1), int(row.get("cpu", 1) or 1),
                         metric_name, sample_idx, value),
                    )
                    jsonl_lines.append(json.dumps({
                        "run_id": run_id, "kind": meta["kind"], "suite": "tier2",
                        "commit": meta["commit"], "bench": bench, "metric": metric_name,
                        "value": value, "sample": sample_idx,
                        "ingested_at": datetime.now(timezone.utc).isoformat(),
                    }))
    conn.commit()
    if jsonl_lines:
        with jsonl_path.open("a") as f:
            f.write("\n".join(jsonl_lines) + "\n")
    return run_id
