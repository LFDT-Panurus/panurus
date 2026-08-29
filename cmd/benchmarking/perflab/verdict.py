"""Regression classification.

Two mechanisms, matching the two job shapes described in
docs/development/perflab.md:

* Pairwise (PR / confirmation jobs): percentage delta between the base and
  head sides of the same interleaved run, neutral if within ``pairwise_delta_pct``
  *or* if the base/head sample ranges overlap. This is the same significance
  test as ``compare_benchmarks._classify``/``_ranges_overlap`` (re-derived here
  rather than imported, since compare_benchmarks.py works from files whereas
  PerfLab already has the samples in SQLite -- but the logic is intentionally
  identical so a human moving between the PR CI report and the PerfLab
  dashboard sees the same rules).
* Historical (solo `main` runs): median/MAD baseline over the last
  ``history_window`` non-noisy runs. A candidate that exceeds both the
  absolute floor and the robust threshold is `suspected`, not `regressed`,
  until a confirmation A/B job (queue.py) proves it.
"""

from __future__ import annotations

import json
import sqlite3
import statistics
from dataclasses import dataclass
from datetime import datetime, timezone

from .config import Thresholds

NEUTRAL, IMPROVED, REGRESSED, SUSPECTED, HARNESS_CHANGED, NEW, REMOVED = (
    "neutral", "improved", "regressed", "suspected", "harness-changed", "new", "removed",
)

# ns/op, B/op, allocs/op: lower is always better for Tier-1's metrics.
_LOWER_IS_BETTER = True

_MAD_CONSISTENCY = 1.4826  # scales MAD to be a normal-consistent std-dev estimator


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _pct(base: float, new: float) -> float:
    return 0.0 if base == 0 else (new - base) / base * 100.0


def _ranges_overlap(a: list[float], b: list[float]) -> bool:
    if len(a) < 2 or len(b) < 2:
        return True
    return min(a) <= max(b) and min(b) <= max(a)


def _series_of(conn: sqlite3.Connection, run_id: str, side: str) -> dict[tuple, list[float]]:
    """{(bench, params_json, workers, metric): [values...]} for one run/side."""
    rows = conn.execute(
        "SELECT bench, params_json, workers, metric, value FROM measurements "
        "WHERE run_id=? AND side=? AND bench NOT LIKE '%-canary-%'",
        (run_id, side),
    ).fetchall()
    out: dict[tuple, list[float]] = {}
    for r in rows:
        key = (r["bench"], r["params_json"], r["workers"], r["metric"])
        out.setdefault(key, []).append(r["value"])
    return out


def canary_values(conn: sqlite3.Connection, run_id: str, side: str = "solo") -> dict[str, list[float]]:
    rows = conn.execute(
        "SELECT bench, value FROM measurements WHERE run_id=? AND side=? "
        "AND bench LIKE '%-canary-%' AND metric='ns/op'",
        (run_id, side),
    ).fetchall()
    out: dict[str, list[float]] = {}
    for r in rows:
        out.setdefault(r["bench"], []).append(r["value"])
    return out


def check_canary(conn: sqlite3.Connection, run_id: str, thresholds: Thresholds,
                  side: str = "solo") -> tuple[bool, str]:
    """Compare this run's before/after canary against the rolling median of
    the last `history_window` non-noisy runs' canary. Returns (noisy, reason).
    """
    vals = canary_values(conn, run_id, side)
    before = vals.get("BenchmarkActionMarshalling-canary-before", [])
    after = vals.get("BenchmarkActionMarshalling-canary-after", [])
    if not before or not after:
        return False, ""  # nothing to compare against; do not block on missing data

    drift_pct = abs(_pct(statistics.median(before), statistics.median(after)))
    if drift_pct > thresholds.canary_band_pct:
        return True, f"canary drifted {drift_pct:.1f}% within the run (before vs after)"

    history = conn.execute(
        "SELECT m.value FROM measurements m JOIN runs r ON r.run_id = m.run_id "
        "WHERE m.bench='BenchmarkActionMarshalling-canary-before' AND m.metric='ns/op' "
        "AND m.side='solo' AND r.noisy=0 AND r.run_id != ? "
        "ORDER BY r.started_at DESC LIMIT ?",
        (run_id, thresholds.history_window * 2),
    ).fetchall()
    if len(history) < 3:
        return False, ""
    baseline = statistics.median([h["value"] for h in history])
    this_value = statistics.median(before)
    hist_drift = abs(_pct(baseline, this_value))
    if hist_drift > thresholds.canary_band_pct:
        return True, f"canary {hist_drift:.1f}% off its {len(history)}-run rolling median"
    return False, ""


@dataclass
class VerdictRow:
    bench: str
    params_json: str
    metric: str
    base_value: float | None
    new_value: float | None
    pct_change: float | None
    status: str


def pairwise_verdicts(conn: sqlite3.Connection, run_id: str, thresholds: Thresholds,
                       harness_changed: bool) -> list[VerdictRow]:
    """Classify a PR/confirmation run's head side against its base side."""
    base = _series_of(conn, run_id, "base")
    head = _series_of(conn, run_id, "head")
    keys = set(base) | set(head)
    out: list[VerdictRow] = []
    for key in sorted(keys, key=lambda k: tuple(map(str, k))):
        bench, params_json, workers, metric = key
        b_vals, h_vals = base.get(key), head.get(key)
        if b_vals is None:
            out.append(VerdictRow(bench, params_json, metric, None,
                                   statistics.mean(h_vals), None, NEW))
            continue
        if h_vals is None:
            out.append(VerdictRow(bench, params_json, metric, statistics.mean(b_vals),
                                   None, None, REMOVED))
            continue
        b_mean, h_mean = statistics.mean(b_vals), statistics.mean(h_vals)
        pct = _pct(b_mean, h_mean)
        if harness_changed:
            status = HARNESS_CHANGED
        elif abs(pct) < thresholds.pairwise_delta_pct or _ranges_overlap(b_vals, h_vals):
            status = NEUTRAL
        else:
            improved = (pct < 0) == _LOWER_IS_BETTER
            status = IMPROVED if improved else REGRESSED
        out.append(VerdictRow(bench, params_json, metric, b_mean, h_mean, pct, status))
    return out


def historical_verdicts(conn: sqlite3.Connection, run_id: str, thresholds: Thresholds,
                         harness_changed: bool) -> list[VerdictRow]:
    """Classify a solo `main` run against the median/MAD of recent non-noisy
    `main` runs on the same series. Never returns REGRESSED directly -- only
    NEUTRAL or SUSPECTED; a suspected series must be confirmed by an
    interleaved A/B job before it is trusted (see cli.py `verdict`)."""
    this = _series_of(conn, run_id, "solo")
    out: list[VerdictRow] = []
    for key, vals in sorted(this.items(), key=lambda kv: tuple(map(str, kv[0]))):
        bench, params_json, workers, metric = key
        new_value = statistics.mean(vals)
        history = conn.execute(
            "SELECT m.value FROM measurements m JOIN runs r ON r.run_id = m.run_id "
            "WHERE r.kind='main' AND r.noisy=0 AND m.side='solo' AND m.bench=? "
            "AND m.params_json=? AND m.workers=? AND m.metric=? AND r.run_id != ? "
            "GROUP BY r.run_id ORDER BY r.started_at DESC LIMIT ?",
            (bench, params_json, workers, metric, run_id, thresholds.history_window),
        ).fetchall()
        if len(history) < 5:
            out.append(VerdictRow(bench, params_json, metric, None, new_value, None, NEUTRAL))
            continue
        hist_vals = [h["value"] for h in history]
        baseline = statistics.median(hist_vals)
        mad = statistics.median([abs(v - baseline) for v in hist_vals]) or 1e-9
        robust_std = mad * _MAD_CONSISTENCY
        pct = _pct(baseline, new_value)
        if harness_changed:
            status = HARNESS_CHANGED
        elif abs(pct) < thresholds.historical_floor_pct:
            status = NEUTRAL
        elif abs(new_value - baseline) > thresholds.historical_mad_multiplier * robust_std:
            status = SUSPECTED if (pct > 0) == _LOWER_IS_BETTER else IMPROVED
        else:
            status = NEUTRAL
        out.append(VerdictRow(bench, params_json, metric, baseline, new_value, pct, status))
    return out


def persist(conn: sqlite3.Connection, run_id: str, rows: list[VerdictRow]) -> None:
    conn.execute("DELETE FROM verdicts WHERE run_id=?", (run_id,))
    now = _now_iso()
    conn.executemany(
        "INSERT INTO verdicts (run_id, bench, params_json, metric, base_value, new_value, "
        "pct_change, status, created_at) VALUES (?,?,?,?,?,?,?,?,?)",
        [(run_id, r.bench, r.params_json, r.metric, r.base_value, r.new_value,
          r.pct_change, r.status, now) for r in rows],
    )
    conn.commit()


def render_summary_md(run_id: str, kind: str, rows: list[VerdictRow]) -> str:
    emoji = {NEUTRAL: "➖", IMPROVED: "\U0001F7E2", REGRESSED: "\U0001F534",
             SUSPECTED: "\U0001F7E1", HARNESS_CHANGED: "❓", NEW: "ℹ️",
             REMOVED: "ℹ️"}
    lines = [f"# PerfLab summary: `{run_id}` ({kind})", "",
              "| Benchmark | Metric | Base | New | Δ | Status |",
              "|---|---|---|---|---|---|"]
    for r in rows:
        base_s = f"{r.base_value:,.0f}" if r.base_value is not None else "n/a"
        new_s = f"{r.new_value:,.0f}" if r.new_value is not None else "n/a"
        pct_s = f"{r.pct_change:+.1f}%" if r.pct_change is not None else "n/a"
        lines.append(f"| {r.bench} | {r.metric} | {base_s} | {new_s} | {pct_s} | "
                      f"{emoji.get(r.status, '')} {r.status} |")
    return "\n".join(lines) + "\n"
