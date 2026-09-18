"""Turn a run's raw `go test -bench` files into rows in measurements.jsonl + SQLite.

Reuses `bench_parse.simple_parser` (the single source of truth for parsing a
benchmark line, also used by compare_benchmarks.py and the plotting tools) for
the numeric metrics. Tier-1 fixes one parameter combination per invocation
(see config.Tier1Config), so -- unlike compare_benchmarks.py, which must
disambiguate many sub-benchmarks per file -- a Tier-1 output file always
contains exactly one series, repeated `count` (or, for A/B rounds, 1) times;
those repeats become `sample_idx` 0..N-1 in insertion order.
"""

from __future__ import annotations

import json
import sys
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))  # cmd/benchmarking/
from bench_parse import simple_parser  # noqa: E402

_METRICS = ("ns/op", "B/op", "allocs/op")


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _parse_tag(filename: str) -> tuple[str, str]:
    """`<bench>[-canary].<tag>.txt` -> (bench_key, tag)."""
    stem = filename.rsplit(".txt", 1)[0]
    bench_key, tag = stem.rsplit(".", 1)
    return bench_key, tag


def _side_of(tag: str) -> str:
    if tag == "solo":
        return "solo"
    if tag.startswith("base_"):
        return "base"
    if tag.startswith("head_"):
        return "head"
    return "solo"


def ingest_run(conn, jsonl_path: Path, run_dir: Path) -> str:
    """Ingest one run directory. Returns the run_id. Idempotent: re-ingesting
    the same run_id first deletes its prior rows, so a fixed bug in ingest
    logic can be re-run without hand-editing the database."""
    meta = json.loads((run_dir / "meta.json").read_text())
    run_id = meta["run_id"]

    conn.execute("DELETE FROM measurements WHERE run_id=?", (run_id,))
    conn.execute("DELETE FROM runs WHERE run_id=?", (run_id,))

    params_json = json.dumps(meta.get("params", {}), sort_keys=True)
    conn.execute(
        "INSERT INTO runs (run_id, kind, suite, commit_sha, commit_time, parent_sha, "
        "pr_number, baseline_sha, host, go_version, bench_code_hash, started_at, "
        "finished_at, status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
        (
            run_id, meta["kind"], meta["suite"], meta["commit"], meta.get("commit_time"),
            meta.get("parent"), meta.get("pr_number"), meta.get("baseline"),
            meta.get("host"), meta.get("go_version"), meta.get("bench_code_hash"),
            meta["started_at"], meta.get("finished_at"), "ok",
        ),
    )

    jsonl_lines = []
    for txt_file in sorted(run_dir.glob("*.txt")):
        bench_key, tag = _parse_tag(txt_file.name)
        side = _side_of(tag)
        df = simple_parser(txt_file)
        if df.empty:
            continue
        base_sample_idx = 0
        if side in ("base", "head"):
            base_sample_idx = int(tag.rsplit("_", 1)[1])
        for i, row in df.iterrows():
            sample_idx = base_sample_idx if side in ("base", "head") else i
            for metric in _METRICS:
                if metric not in row or row[metric] != row[metric]:  # NaN check
                    continue
                value = float(row[metric])
                conn.execute(
                    "INSERT INTO measurements (run_id, side, bench, params_json, workers, "
                    "cpu, metric, sample_idx, value) VALUES (?,?,?,?,?,?,?,?,?)",
                    (run_id, side, bench_key, params_json, int(row.get("workers", 1)),
                     1, metric, int(sample_idx), value),
                )
                jsonl_lines.append(json.dumps({
                    "run_id": run_id, "kind": meta["kind"], "suite": meta["suite"],
                    "commit": meta["commit"], "pr": meta.get("pr_number"), "side": side,
                    "bench": bench_key, "params": meta.get("params", {}),
                    "cpu": 1, "workers": int(row.get("workers", 1)),
                    "metric": metric, "value": value, "sample": int(sample_idx),
                    "host": meta.get("host"), "go": meta.get("go_version"),
                    "bench_code_hash": meta.get("bench_code_hash"),
                    "ingested_at": _now_iso(),
                }))

    conn.commit()
    if jsonl_lines:
        with jsonl_path.open("a") as f:
            f.write("\n".join(jsonl_lines) + "\n")
    return run_id
