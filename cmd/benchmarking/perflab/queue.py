"""SQLite-backed job queue that also serves as the single-run mutex.

Only one benchmark job may run at a time (measurement fidelity depends on an
otherwise-idle host), so ``claim()`` uses ``BEGIN IMMEDIATE`` to atomically
pick and mark the highest-priority pending job as running, and the worker
loop (``cli.py work``) processes jobs one at a time.
"""

from __future__ import annotations

import json
import sqlite3
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

SCHEMA = """
CREATE TABLE IF NOT EXISTS jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL,      -- 'main' | 'pr' | 'confirm' | 'backfill' | 'tier2'
    sha         TEXT NOT NULL,
    baseline    TEXT,               -- set for 'pr'/'confirm'
    suite       TEXT NOT NULL,      -- 'tier1' | 'tier2'
    pr_number   INTEGER,
    priority    INTEGER NOT NULL DEFAULT 100,  -- lower runs first
    payload     TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'pending', -- 'pending'|'running'|'done'|'failed'
    created_at  TEXT NOT NULL,
    claimed_at  TEXT,
    finished_at TEXT,
    error       TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dedup
    ON jobs(kind, sha, suite, COALESCE(baseline, ''))
    WHERE status IN ('pending', 'running');
"""


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


@dataclass
class Job:
    id: int
    kind: str
    sha: str
    baseline: str | None
    suite: str
    pr_number: int | None
    priority: int
    payload: dict[str, Any]


class Queue:
    def __init__(self, db_path: Path):
        db_path.parent.mkdir(parents=True, exist_ok=True)
        self.conn = sqlite3.connect(db_path, timeout=30, isolation_level=None)
        self.conn.row_factory = sqlite3.Row
        self.conn.execute("PRAGMA journal_mode=WAL")
        self.conn.executescript(SCHEMA)

    def enqueue(self, kind: str, sha: str, suite: str, *, baseline: str | None = None,
                pr_number: int | None = None, priority: int = 100,
                payload: dict[str, Any] | None = None) -> int | None:
        """Insert a job, deduped against any pending/running job with the same
        (kind, sha, suite, baseline). Returns the new row id, or None if a
        duplicate already existed."""
        try:
            cur = self.conn.execute(
                "INSERT INTO jobs (kind, sha, baseline, suite, pr_number, priority, payload, created_at) "
                "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                (kind, sha, baseline, suite, pr_number, priority,
                 json.dumps(payload or {}), _now()),
            )
            return cur.lastrowid
        except sqlite3.IntegrityError:
            return None

    def claim(self) -> Job | None:
        """Atomically claim the oldest highest-priority pending job."""
        self.conn.execute("BEGIN IMMEDIATE")
        try:
            row = self.conn.execute(
                "SELECT * FROM jobs WHERE status='pending' "
                "ORDER BY priority ASC, id ASC LIMIT 1"
            ).fetchone()
            if row is None:
                self.conn.execute("COMMIT")
                return None
            self.conn.execute(
                "UPDATE jobs SET status='running', claimed_at=? WHERE id=?",
                (_now(), row["id"]),
            )
            self.conn.execute("COMMIT")
        except Exception:
            self.conn.execute("ROLLBACK")
            raise
        return Job(
            id=row["id"], kind=row["kind"], sha=row["sha"], baseline=row["baseline"],
            suite=row["suite"], pr_number=row["pr_number"], priority=row["priority"],
            payload=json.loads(row["payload"]),
        )

    def complete(self, job_id: int) -> None:
        self.conn.execute(
            "UPDATE jobs SET status='done', finished_at=? WHERE id=?", (_now(), job_id)
        )

    def fail(self, job_id: int, error: str) -> None:
        self.conn.execute(
            "UPDATE jobs SET status='failed', finished_at=?, error=? WHERE id=?",
            (_now(), error[:4000], job_id),
        )

    def pending_count(self) -> int:
        return self.conn.execute(
            "SELECT COUNT(*) FROM jobs WHERE status='pending'"
        ).fetchone()[0]

    def recent(self, limit: int = 50) -> list[sqlite3.Row]:
        return self.conn.execute(
            "SELECT * FROM jobs ORDER BY id DESC LIMIT ?", (limit,)
        ).fetchall()

    def close(self) -> None:
        self.conn.close()


def wait_and_claim(q: Queue, poll_seconds: float = 5.0) -> Job:
    """Block until a job is available, then claim and return it."""
    while True:
        job = q.claim()
        if job is not None:
            return job
        time.sleep(poll_seconds)
