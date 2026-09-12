"""Executes a Tier-1 (or A/B) benchmark job: worktree, probe, run, record.

Tier-1 deliberately uses only stock `go test -bench -benchmem` output (see
config.py) so that it works unmodified on the oldest commits in the backfill
window -- there is no way to teach an old commit about a new output format.
"""

from __future__ import annotations

import json
import os
import platform
import subprocess
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

from . import capability, gitutil
from .config import BenchmarkSpec, PerflabConfig

BENCH_TEST_FILE_GLOBS = [
    "token/core/zkatdlog/nogh/v1/transfer/sender_test.go",
    "token/core/zkatdlog/nogh/v1/transfer/bftransfer_test.go",
    "token/core/zkatdlog/nogh/v1/transfer/action_test.go",
    "token/core/zkatdlog/nogh/v1/issue/issuer_test.go",
    "token/core/zkatdlog/nogh/v1/issue/action_test.go",
    "token/core/zkatdlog/nogh/v1/validator/validator_test.go",
    "token/core/zkatdlog/nogh/v1/transfer_test.go",
    "token/core/zkatdlog/nogh/v1/issue_test.go",
    "token/core/zkatdlog/nogh/v1/auditor_test.go",
]


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _run_id(kind: str, sha: str) -> str:
    ts = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return f"{ts}-{kind}-{sha[:9]}"


@dataclass
class DoctorReport:
    checks: dict[str, str]
    ok: bool


def doctor(cfg: PerflabConfig) -> DoctorReport:
    checks: dict[str, str] = {}
    ok = True

    load1, load5, load15 = os.getloadavg()
    checks["load_average"] = f"{load1:.2f} {load5:.2f} {load15:.2f}"
    if load1 > cfg.thresholds.max_load_average:
        checks["load_average_status"] = "FAIL: exceeds max_load_average"
        ok = False
    else:
        checks["load_average_status"] = "ok"

    checks["nproc"] = str(os.cpu_count())

    gov_path = Path("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")
    if gov_path.exists():
        gov = gov_path.read_text().strip()
        checks["governor"] = gov
        if gov != "performance":
            checks["governor_status"] = "WARN: expected 'performance'"
        else:
            checks["governor_status"] = "ok"
    else:
        checks["governor"] = "unavailable"

    turbo_path = Path("/sys/devices/system/cpu/intel_pstate/no_turbo")
    if turbo_path.exists():
        no_turbo = turbo_path.read_text().strip()
        checks["turbo_disabled"] = "yes" if no_turbo == "1" else "no"
        if no_turbo != "1":
            checks["turbo_status"] = "WARN: turbo is enabled, expect more A/B noise"
        else:
            checks["turbo_status"] = "ok"
    else:
        checks["turbo_disabled"] = "unavailable"

    for name, cmd in (("go", ["go", "version"]), ("git", ["git", "--version"]),
                       ("taskset", ["taskset", "-V"])):
        proc = subprocess.run(cmd, capture_output=True, text=True)
        checks[name] = proc.stdout.strip() or proc.stderr.strip()
        if proc.returncode != 0:
            ok = False

    return DoctorReport(checks=checks, ok=ok)


def _bench_cmd(worktree: Path, spec: BenchmarkSpec, cfg: PerflabConfig,
                caps: capability.Capabilities, count: int, benchtime: str,
                cpu: int, extra_params: dict[str, str] | None = None) -> list[str]:
    cmd = [
        "go", "test", f"./{spec.package}",
        "-run=^$", f"-bench={spec.name}", "-benchmem",
        f"-count={count}", f"-benchtime={benchtime}", f"-cpu={cpu}", "-timeout=0",
    ]
    params = dict(cfg.tier1.params)
    if extra_params:
        params.update(extra_params)
    for k, v in params.items():
        cmd.append(f"-{k}={v}")
    if "-proof_type" in caps.supported_flags:
        cmd.append("-proof_type=bulletproof")
    if "-executor" in caps.supported_flags:
        cmd.append("-executor=serial")
    return cmd


def _run_one(worktree: Path, cmd: list[str], out_path: Path, cpuset: str,
             timeout: int = 1800) -> tuple[int, str]:
    full = ["taskset", "-c", cpuset, *cmd]
    proc = subprocess.run(full, cwd=worktree, capture_output=True, text=True, timeout=timeout)
    out_path.write_text(proc.stdout + "\n" + proc.stderr if proc.returncode != 0 else proc.stdout)
    return proc.returncode, proc.stderr


def _probe_for_package(worktree: Path, package: str,
                        cache: dict[str, capability.Capabilities]) -> capability.Capabilities:
    if package not in cache:
        cache[package] = capability.probe(worktree, package)
    return cache[package]


def run_tier1_into(worktree: Path, out_dir: Path, cfg: PerflabConfig, tag: str,
                    count: int | None = None) -> dict[str, Path]:
    """Run every Tier-1 benchmark in `worktree`, writing `<bench>.<tag>.txt`
    files into out_dir. Returns {bench_name: file_path}. `tag` is 'solo',
    'base', or 'head' -- the same filename-substring convention
    compare_benchmarks.py already uses for grouping."""
    out_dir.mkdir(parents=True, exist_ok=True)
    caps_cache: dict[str, capability.Capabilities] = {}
    files: dict[str, Path] = {}
    n = count if count is not None else cfg.tier1.count
    canaries_seen = 0
    for spec in cfg.tier1.benchmarks:
        caps = _probe_for_package(worktree, spec.package, caps_cache)
        cmd = _bench_cmd(worktree, spec, cfg, caps, n, cfg.tier1.benchtime, cfg.tier1.cpu)
        bench_id = spec.name.strip("^$")
        if spec.is_canary:
            # The canary runs first AND last (config.TIER1_BENCHMARKS lists it
            # twice) so both samples must land in distinct files, or the
            # second run silently overwrites the first's output.
            suffix = "-canary-before" if canaries_seen == 0 else "-canary-after"
            canaries_seen += 1
        else:
            suffix = ""
        out_path = out_dir / f"{bench_id}{suffix}.{tag}.txt"
        rc, stderr = _run_one(worktree, cmd, out_path, cfg.bench_cpuset)
        if rc != 0:
            raise RuntimeError(f"benchmark {bench_id} failed (rc={rc}): {stderr[-2000:]}")
        files[f"{bench_id}{suffix}"] = out_path
    return files


def run_solo(cfg: PerflabConfig, layout, sha: str, kind: str = "main") -> str:
    """Run Tier-1 once against a single commit. Returns the run_id."""
    run_id = _run_id(kind, sha)
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True, exist_ok=True)
    worktree = layout.worktrees / run_id
    started = _now_iso()
    try:
        gitutil.add_worktree(layout.repo, sha, worktree)
        files = run_tier1_into(worktree, run_dir, cfg, tag="solo")
        meta = {
            "run_id": run_id, "kind": kind, "suite": "tier1", "commit": sha,
            "baseline": None, "parent": gitutil.parent_of(layout.repo, sha),
            "started_at": started, "finished_at": _now_iso(),
            "host": platform.node(), "nproc": os.cpu_count(),
            "go_version": gitutil.go_version(worktree),
            "bench_code_hash": gitutil.bench_code_hash(layout.repo, sha, BENCH_TEST_FILE_GLOBS),
            "cpuset": cfg.bench_cpuset, "params": cfg.tier1.params,
            "files": {k: str(v.name) for k, v in files.items()},
        }
        (run_dir / "meta.json").write_text(json.dumps(meta, indent=2))
        return run_id
    finally:
        gitutil.remove_worktree(layout.repo, worktree)


def run_ab(cfg: PerflabConfig, layout, base_sha: str, head_sha: str, *,
           kind: str = "pr", pr_number: int | None = None, rounds: int | None = None) -> str:
    """Interleaved A/B: alternate single-count rounds between two worktrees on
    this same host, the pattern documented in
    .github/workflows/token-validation-benchmark.yml, so the delta reflects
    the diff rather than host-to-host variance (there is only one host here,
    but thermal/scheduler drift across a long solo run is exactly what
    interleaving also cancels)."""
    run_id = _run_id(kind, head_sha)
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True, exist_ok=True)
    base_wt = layout.worktrees / f"{run_id}-base"
    head_wt = layout.worktrees / f"{run_id}-head"
    started = _now_iso()
    n_rounds = rounds if rounds is not None else cfg.tier1.count
    try:
        gitutil.add_worktree(layout.repo, base_sha, base_wt)
        gitutil.add_worktree(layout.repo, head_sha, head_wt)
        for round_idx in range(n_rounds):
            run_tier1_into(base_wt, run_dir, cfg, tag=f"base_{round_idx}", count=1)
            run_tier1_into(head_wt, run_dir, cfg, tag=f"head_{round_idx}", count=1)
        meta = {
            "run_id": run_id, "kind": kind, "suite": "tier1", "commit": head_sha,
            "baseline": base_sha, "pr_number": pr_number,
            "started_at": started, "finished_at": _now_iso(),
            "host": platform.node(), "nproc": os.cpu_count(),
            "go_version": gitutil.go_version(head_wt),
            "bench_code_hash": gitutil.bench_code_hash(layout.repo, head_sha, BENCH_TEST_FILE_GLOBS),
            "base_bench_code_hash": gitutil.bench_code_hash(layout.repo, base_sha, BENCH_TEST_FILE_GLOBS),
            "cpuset": cfg.bench_cpuset, "rounds": n_rounds, "params": cfg.tier1.params,
        }
        (run_dir / "meta.json").write_text(json.dumps(meta, indent=2))
        return run_id
    finally:
        gitutil.remove_worktree(layout.repo, base_wt)
        gitutil.remove_worktree(layout.repo, head_wt)
