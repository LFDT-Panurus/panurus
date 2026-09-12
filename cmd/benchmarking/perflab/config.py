"""Suite definitions and thresholds, loadable from ``etc/perflab.yaml``.

A single :class:`PerflabConfig` covers both tiers described in
``docs/development/perflab.md``. Defaults reproduce the plan's Tier-1 suite
exactly (see ``DEFAULT_CONFIG``); an installation only needs a
``perflab.yaml`` to override the GitHub repo, thresholds, or CPU pinning.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

# The commit at which -proof_type/-executor were introduced
# (token/core/zkatdlog/nogh/v1/benchmark/flags.go). Commits before this reject
# those flags with "flag provided but not defined"; the runner capability-probes
# rather than hardcoding this, but it is recorded here for documentation.
FLAGS_INTRODUCED_AT = "586d4f58"


@dataclass(frozen=True)
class BenchmarkSpec:
    """One `go test -bench` target."""

    package: str  # relative to repo root, e.g. token/core/zkatdlog/nogh/v1/transfer
    name: str     # exact -bench regexp, e.g. ^BenchmarkSender$
    is_canary: bool = False


# Tier-1: one fixed, cheap configuration that is valid on every commit in the
# backfill window (default proof type / executor, no flags that might not
# exist yet). See dlognogh.md "Benchmark Packages".
TIER1_BENCHMARKS: tuple[BenchmarkSpec, ...] = (
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/transfer", "^BenchmarkActionMarshalling$", is_canary=True),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/transfer", "^BenchmarkSender$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/transfer", "^BenchmarkVerificationSenderProof$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/transfer", "^BenchmarkTransferProofGeneration$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/issue", "^BenchmarkIssuer$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/issue", "^BenchmarkProofVerificationIssuer$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/validator", "^BenchmarkValidatorTransfer$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1", "^BenchmarkTransferServiceTransfer$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1", "^BenchmarkIssueServiceIssue$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1", "^BenchmarkAuditorServiceCheck$"),
    BenchmarkSpec("token/core/zkatdlog/nogh/v1/transfer", "^BenchmarkActionMarshalling$", is_canary=True),
)

# Params applied to every Tier-1 benchmark that supports them (probed per
# commit; silently dropped when a flag does not exist yet -- see capability.py).
TIER1_PARAMS: dict[str, str] = {
    "bits": "32",
    "curves": "BLS12_381_BBS_GURVY",
    "num_inputs": "2",
    "num_outputs": "2",
}


@dataclass(frozen=True)
class Thresholds:
    # Percent change below which a delta is treated as noise (matches
    # compare_benchmarks.py's --delta default of 1, widened slightly because
    # Tier-1 uses a shorter -benchtime than the PR workflow's 10s).
    pairwise_delta_pct: float = 1.5
    # Canary run-to-run deviation (from its rolling median) above which a run
    # is flagged `noisy` and excluded from verdicts/baselines.
    canary_band_pct: float = 5.0
    # Historical (solo `main`) detector: number of most-recent non-noisy runs
    # used to compute the median/MAD baseline for each series.
    history_window: int = 15
    # A candidate must exceed both this absolute floor...
    historical_floor_pct: float = 3.0
    # ...and this robust z-score-like threshold (MAD scaled to be a normal-
    # consistent estimator of std dev via the 1.4826 constant).
    historical_mad_multiplier: float = 4.0
    # Refuse to start a benchmarking job if 1-minute load average exceeds this.
    max_load_average: float = 4.0


@dataclass(frozen=True)
class Tier1Config:
    benchmarks: tuple[BenchmarkSpec, ...] = TIER1_BENCHMARKS
    params: dict[str, str] = field(default_factory=lambda: dict(TIER1_PARAMS))
    count: int = 6
    benchtime: str = "2s"
    cpu: int = 1


@dataclass(frozen=True)
class Tier2Config:
    proof_type: str = "all"
    executor: str = "all"
    cpus: str = "1,2,4,8,16,32"
    duration: str = "60s"
    count: int = 10


@dataclass(frozen=True)
class GitHubConfig:
    owner: str = "LFDT-Panurus"
    repo: str = "panurus"
    branch: str = "main"
    # Optional fine-grained PAT with NO write scopes, read via env var name
    # (never stored in the config file itself). None => unauthenticated REST
    # (60 req/hour), which is enough for polling every 5 minutes.
    token_env: str | None = "PERFLAB_GITHUB_TOKEN"


@dataclass(frozen=True)
class PerflabConfig:
    github: GitHubConfig = field(default_factory=GitHubConfig)
    tier1: Tier1Config = field(default_factory=Tier1Config)
    tier2: Tier2Config = field(default_factory=Tier2Config)
    thresholds: Thresholds = field(default_factory=Thresholds)
    # Cores reserved for benchmark jobs (taskset -c). Housekeeping (poller,
    # report generator) is pinned to the complement via systemd CPUAffinity=.
    bench_cpuset: str = "2-31"
    raw_retention_days_gzip: int = 7
    raw_retention_days_delete: int = 90
    backfill_since_days: int = 30
    backfill_stride: int = 8


def _merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for k, v in override.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _merge(out[k], v)
        else:
            out[k] = v
    return out


def load(path: Path | None) -> PerflabConfig:
    """Load config from ``path``, falling back to defaults for anything absent."""
    cfg = PerflabConfig()
    if path is None or not path.exists():
        return cfg
    raw = yaml.safe_load(path.read_text()) or {}

    gh = raw.get("github", {})
    github = GitHubConfig(
        owner=gh.get("owner", cfg.github.owner),
        repo=gh.get("repo", cfg.github.repo),
        branch=gh.get("branch", cfg.github.branch),
        token_env=gh.get("token_env", cfg.github.token_env),
    )

    th = raw.get("thresholds", {})
    thresholds = Thresholds(
        pairwise_delta_pct=th.get("pairwise_delta_pct", cfg.thresholds.pairwise_delta_pct),
        canary_band_pct=th.get("canary_band_pct", cfg.thresholds.canary_band_pct),
        history_window=th.get("history_window", cfg.thresholds.history_window),
        historical_floor_pct=th.get("historical_floor_pct", cfg.thresholds.historical_floor_pct),
        historical_mad_multiplier=th.get("historical_mad_multiplier", cfg.thresholds.historical_mad_multiplier),
        max_load_average=th.get("max_load_average", cfg.thresholds.max_load_average),
    )

    t1 = raw.get("tier1", {})
    tier1 = Tier1Config(
        count=t1.get("count", cfg.tier1.count),
        benchtime=t1.get("benchtime", cfg.tier1.benchtime),
        cpu=t1.get("cpu", cfg.tier1.cpu),
        params=_merge(cfg.tier1.params, t1.get("params", {})),
    )

    t2 = raw.get("tier2", {})
    tier2 = Tier2Config(
        proof_type=t2.get("proof_type", cfg.tier2.proof_type),
        executor=t2.get("executor", cfg.tier2.executor),
        cpus=t2.get("cpus", cfg.tier2.cpus),
        duration=t2.get("duration", cfg.tier2.duration),
        count=t2.get("count", cfg.tier2.count),
    )

    return PerflabConfig(
        github=github,
        tier1=tier1,
        tier2=tier2,
        thresholds=thresholds,
        bench_cpuset=raw.get("bench_cpuset", cfg.bench_cpuset),
        raw_retention_days_gzip=raw.get("raw_retention_days_gzip", cfg.raw_retention_days_gzip),
        raw_retention_days_delete=raw.get("raw_retention_days_delete", cfg.raw_retention_days_delete),
        backfill_since_days=raw.get("backfill_since_days", cfg.backfill_since_days),
        backfill_stride=raw.get("backfill_stride", cfg.backfill_stride),
    )
