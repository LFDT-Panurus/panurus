"""Enumerate new `main` commits and open PR heads, and enqueue Tier-1 jobs.

Runs every 5 minutes under `perflab-poll.timer`. Idempotent: a commit or PR
head that has already been enqueued (pending/running: caught by the queue's
unique index) or already has a stored run (done: checked against `runs`
here) is skipped, so re-running poll on a schedule is always safe.
"""

from __future__ import annotations

import sqlite3

from . import gitutil
from .config import PerflabConfig
from .paths import Layout
from .queue import Queue

PRIORITY_PR = 10
PRIORITY_MAIN = 100
PRIORITY_BACKFILL = 200

# Poll only needs a short lookback -- the backfill command owns the deep
# history; polling further back than this would just re-derive dedup work
# `commits_since` already did on the previous tick.
POLL_LOOKBACK_DAYS = 2


def _already_have_main_run(conn: sqlite3.Connection, sha: str) -> bool:
    row = conn.execute(
        "SELECT 1 FROM runs WHERE kind='main' AND commit_sha=? AND status='ok' LIMIT 1",
        (sha,),
    ).fetchone()
    return row is not None


def poll_main(cfg: PerflabConfig, layout: Layout, q: Queue, conn: sqlite3.Connection) -> int:
    """Enqueue any recent `main` commit that has neither a stored run nor a
    pending/running job. Returns the number of jobs newly enqueued."""
    gitutil.clone_or_fetch(f"https://github.com/{cfg.github.owner}/{cfg.github.repo}.git", layout.repo)
    commits = gitutil.commits_since(layout.repo, cfg.github.branch, POLL_LOOKBACK_DAYS)
    enqueued = 0
    for commit in commits:
        if _already_have_main_run(conn, commit.sha):
            continue
        job_id = q.enqueue("main", commit.sha, "tier1", priority=PRIORITY_MAIN)
        if job_id is not None:
            enqueued += 1
    return enqueued


def poll_prs(cfg: PerflabConfig, layout: Layout, q: Queue) -> int:
    """Enqueue an interleaved A/B Tier-1 job for every open PR's current head
    against its merge-base. Re-polling after a PR gets new commits pushed
    naturally enqueues a fresh job for the new head sha (dedup key includes
    sha), so stale heads are simply never re-run again."""
    prs = gitutil.list_open_prs(cfg.github)
    enqueued = 0
    for pr in prs:
        try:
            base = gitutil.merge_base(layout.repo, pr.base_sha, pr.head_sha)
        except gitutil.GitError:
            base = pr.base_sha  # merge-base unavailable (e.g. shallow history); fall back
        job_id = q.enqueue(
            "pr", pr.head_sha, "tier1", baseline=base, pr_number=pr.number, priority=PRIORITY_PR,
        )
        if job_id is not None:
            enqueued += 1
    return enqueued


def backfill(cfg: PerflabConfig, layout: Layout, q: Queue, conn: sqlite3.Connection,
             since_days: int | None = None, stride: int | None = None) -> int:
    """Coarse pass: enqueue every `stride`-th commit from the last `since_days`
    at the lowest priority, so it only fills capacity idle jobs leave behind.
    The fine-grained bisection pass (enqueueing the midpoint between two
    measured commits whose values diverge beyond the robust threshold) is
    driven separately by `cli.py backfill-bisect` once coarse data exists,
    since it needs verdict.py's historical detector to know where to look."""
    gitutil.clone_or_fetch(f"https://github.com/{cfg.github.owner}/{cfg.github.repo}.git", layout.repo)
    days = since_days if since_days is not None else cfg.backfill_since_days
    n = stride if stride is not None else cfg.backfill_stride
    commits = gitutil.commits_since(layout.repo, cfg.github.branch, days)
    enqueued = 0
    for commit in commits[::-1][::n]:  # newest-first, every nth
        if _already_have_main_run(conn, commit.sha):
            continue
        job_id = q.enqueue("main", commit.sha, "tier1", priority=PRIORITY_BACKFILL)
        if job_id is not None:
            enqueued += 1
    return enqueued
