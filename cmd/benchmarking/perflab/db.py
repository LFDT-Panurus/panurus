"""SQLite schema and connection helper for the PerfLab results database.

This mirrors ``measurements.jsonl`` (the append-only source of truth) in a
queryable form. ``ingest.py`` is the only writer of ``runs``/``measurements``;
``verdict.py`` writes ``verdicts``; ``report.py`` only reads.
"""

from __future__ import annotations

import sqlite3
from pathlib import Path

SCHEMA = """
CREATE TABLE IF NOT EXISTS runs (
    run_id          TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,      -- 'main' | 'pr' | 'confirm'
    suite           TEXT NOT NULL,      -- 'tier1' | 'tier2'
    commit_sha      TEXT NOT NULL,
    commit_time     TEXT,
    parent_sha      TEXT,
    pr_number       INTEGER,
    baseline_sha    TEXT,               -- set for 'pr'/'confirm' (the A side)
    host            TEXT,
    go_version      TEXT,
    bench_code_hash TEXT,
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    noisy           INTEGER NOT NULL DEFAULT 0,
    noisy_reason    TEXT,
    status          TEXT NOT NULL DEFAULT 'ok'  -- 'ok' | 'failed'
);

CREATE TABLE IF NOT EXISTS measurements (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT NOT NULL REFERENCES runs(run_id),
    side        TEXT NOT NULL,          -- 'solo' | 'base' | 'head'
    bench       TEXT NOT NULL,
    params_json TEXT NOT NULL,          -- canonical json of {bits, curve, i, o, ...}
    workers     INTEGER NOT NULL DEFAULT 1,
    cpu         INTEGER NOT NULL DEFAULT 1,
    metric      TEXT NOT NULL,          -- 'ns/op' | 'B/op' | 'allocs/op' | 'tps' | ...
    sample_idx  INTEGER NOT NULL,
    value       REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meas_run ON measurements(run_id);
CREATE INDEX IF NOT EXISTS idx_meas_series
    ON measurements(bench, params_json, workers, cpu, metric);

CREATE TABLE IF NOT EXISTS verdicts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT NOT NULL REFERENCES runs(run_id),
    bench       TEXT NOT NULL,
    params_json TEXT NOT NULL,
    metric      TEXT NOT NULL,
    base_value  REAL,
    new_value   REAL,
    pct_change  REAL,
    status      TEXT NOT NULL,          -- 'neutral' | 'improved' | 'regressed' |
                                          -- 'suspected' | 'harness-changed' | 'new' | 'removed'
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_verdicts_run ON verdicts(run_id);

CREATE TABLE IF NOT EXISTS canary_samples (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT NOT NULL REFERENCES runs(run_id),
    position    TEXT NOT NULL,          -- 'before' | 'after'
    ns_per_op   REAL NOT NULL,
    created_at  TEXT NOT NULL
);
"""


def connect(db_path: Path) -> sqlite3.Connection:
    db_path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(db_path, timeout=30)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA foreign_keys=ON")
    conn.executescript(SCHEMA)
    return conn
