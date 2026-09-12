"""Shared fixtures for offline PerfLab tests.

Everything here runs without a host, a git checkout, or `go test` -- it builds
synthetic run directories (`meta.json` + `<bench>.<tag>.txt` files) in the
exact shape `runner.py` produces, then exercises `ingest.py`/`verdict.py`
against them directly. See docs/development/perflab.md#testing.
"""

from __future__ import annotations

import json
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # cmd/benchmarking/

from perflab import db as perflab_db  # noqa: E402
from perflab.paths import Layout  # noqa: E402


def bench_lines(bench: str, values_ns: list[float], *, b_per_op: int = 24095,
                 allocs_per_op: int = 436, iterations: int = 2000) -> str:
    """Render `values_ns` as `count=len(values_ns)` real `go test -bench` lines
    for one sub-benchmark, matching the shape in
    docs/drivers/benchmark/core/dlognogh/transfer_results.txt."""
    label = f"{bench}/Setup(bits_32,_curve_BLS12_381_BBS_GURVY,_#i_2,_#o_2)_with_1_workers"
    lines = [
        f"{label}\t{iterations}\t{v:.0f} ns/op\t{b_per_op} B/op\t{allocs_per_op} allocs/op"
        for v in values_ns
    ]
    return "\n".join(lines) + "\n"


def write_bench_file(run_dir: Path, bench_key: str, tag: str, values_ns: list[float]) -> None:
    (run_dir / f"{bench_key}.{tag}.txt").write_text(
        bench_lines(bench_key.split("-canary")[0], values_ns)
    )


def write_meta(run_dir: Path, *, run_id: str, kind: str, commit: str, started_at: str,
                baseline: str | None = None, bench_code_hash: str = "aaaa1111bbbb2222",
                base_bench_code_hash: str | None = None, parent: str | None = None) -> None:
    meta = {
        "run_id": run_id, "kind": kind, "suite": "tier1", "commit": commit,
        "baseline": baseline, "parent": parent, "started_at": started_at,
        "finished_at": started_at, "host": "test-host", "nproc": 8,
        "go_version": "go1.26.5", "bench_code_hash": bench_code_hash,
        "cpuset": "2-7", "params": {"bits": "32", "curves": "BLS12_381_BBS_GURVY",
                                     "num_inputs": "2", "num_outputs": "2"},
    }
    if base_bench_code_hash is not None:
        meta["base_bench_code_hash"] = base_bench_code_hash
    (run_dir / "meta.json").write_text(json.dumps(meta, indent=2))


@pytest.fixture
def layout(tmp_path) -> Layout:
    lay = Layout(home=tmp_path / "perflab")
    lay.ensure()
    return lay


@pytest.fixture
def conn(layout):
    c = perflab_db.connect(layout.db_path)
    yield c
    c.close()


def iso(days_ago: int) -> str:
    return (datetime.now(timezone.utc) - timedelta(days=days_ago)).isoformat()
