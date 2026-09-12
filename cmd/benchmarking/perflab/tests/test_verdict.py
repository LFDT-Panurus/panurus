from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from perflab import verdict  # noqa: E402
from perflab.config import Thresholds  # noqa: E402

from .conftest import iso

THRESH = Thresholds()  # pairwise=1.5%, canary_band=5%, floor=3%, mad_mult=4, window=15

_PARAMS = json.dumps({"bits": "32", "curves": "BLS12_381_BBS_GURVY",
                       "num_inputs": "2", "num_outputs": "2"}, sort_keys=True)


def _insert_run(conn, run_id, *, kind, commit, started_at, noisy=0, baseline=None):
    conn.execute(
        "INSERT INTO runs (run_id, kind, suite, commit_sha, baseline_sha, started_at, "
        "finished_at, status, noisy) VALUES (?,?,?,?,?,?,?,?,?)",
        (run_id, kind, "tier1", commit, baseline, started_at, started_at, "ok", noisy),
    )


def _insert_measurement(conn, run_id, side, bench, value, metric="ns/op", sample_idx=0, workers=1):
    conn.execute(
        "INSERT INTO measurements (run_id, side, bench, params_json, workers, cpu, metric, "
        "sample_idx, value) VALUES (?,?,?,?,?,?,?,?,?)",
        (run_id, side, bench, _PARAMS, workers, 1, metric, sample_idx, value),
    )


def test_pairwise_neutral_within_delta(conn):
    run_id = "r-neutral"
    _insert_run(conn, run_id, kind="pr", commit="a" * 40, started_at=iso(0), baseline="b" * 40)
    for i, v in enumerate([500_000, 501_000, 499_000]):
        _insert_measurement(conn, run_id, "base", "BenchmarkSender", v, sample_idx=i)
    for i, v in enumerate([500_500, 501_200, 499_800]):  # <1.5% delta, overlapping range
        _insert_measurement(conn, run_id, "head", "BenchmarkSender", v, sample_idx=i)
    conn.commit()

    rows = verdict.pairwise_verdicts(conn, run_id, THRESH, harness_changed=False)
    assert [r.status for r in rows] == [verdict.NEUTRAL]


def test_pairwise_regressed_clear_slowdown(conn):
    run_id = "r-regressed"
    _insert_run(conn, run_id, kind="pr", commit="a" * 40, started_at=iso(0), baseline="b" * 40)
    for i, v in enumerate([500_000, 501_000, 499_000, 500_500, 499_500, 500_200]):
        _insert_measurement(conn, run_id, "base", "BenchmarkSender", v, sample_idx=i)
    for i, v in enumerate([600_000, 601_000, 599_000, 600_500, 599_500, 600_200]):  # +20%, no overlap
        _insert_measurement(conn, run_id, "head", "BenchmarkSender", v, sample_idx=i)
    conn.commit()

    rows = verdict.pairwise_verdicts(conn, run_id, THRESH, harness_changed=False)
    assert len(rows) == 1
    assert rows[0].status == verdict.REGRESSED
    assert rows[0].pct_change > 15


def test_pairwise_new_and_removed(conn):
    run_id = "r-newrem"
    _insert_run(conn, run_id, kind="pr", commit="a" * 40, started_at=iso(0), baseline="b" * 40)
    _insert_measurement(conn, run_id, "base", "BenchmarkOldOnly", 500_000)
    _insert_measurement(conn, run_id, "head", "BenchmarkNewOnly", 500_000)
    conn.commit()

    rows = {r.bench: r.status for r in verdict.pairwise_verdicts(conn, run_id, THRESH, False)}
    assert rows["BenchmarkOldOnly"] == verdict.REMOVED
    assert rows["BenchmarkNewOnly"] == verdict.NEW


def test_pairwise_harness_changed_overrides_regression(conn):
    run_id = "r-harness"
    _insert_run(conn, run_id, kind="pr", commit="a" * 40, started_at=iso(0), baseline="b" * 40)
    for i, v in enumerate([500_000] * 4):
        _insert_measurement(conn, run_id, "base", "BenchmarkSender", v, sample_idx=i)
    for i, v in enumerate([700_000] * 4):  # +40%, would be REGRESSED otherwise
        _insert_measurement(conn, run_id, "head", "BenchmarkSender", v, sample_idx=i)
    conn.commit()

    rows = verdict.pairwise_verdicts(conn, run_id, THRESH, harness_changed=True)
    assert rows[0].status == verdict.HARNESS_CHANGED


def _seed_history(conn, n=15, center=500_000, jitter=1000):
    for i in range(n):
        run_id = f"hist-{i}"
        v = center + (jitter if i % 2 == 0 else -jitter)
        _insert_run(conn, run_id, kind="main", commit=f"{i:040d}", started_at=iso(30 - i))
        _insert_measurement(conn, run_id, "solo", "BenchmarkSender", v)
    conn.commit()


def test_historical_neutral_within_floor(conn):
    _seed_history(conn)
    run_id = "cand-neutral"
    _insert_run(conn, run_id, kind="main", commit="f" * 40, started_at=iso(0))
    _insert_measurement(conn, run_id, "solo", "BenchmarkSender", 501_000)  # ~0.2% off median
    conn.commit()

    rows = verdict.historical_verdicts(conn, run_id, THRESH, harness_changed=False)
    assert rows[0].status == verdict.NEUTRAL


def test_historical_suspected_clear_regression(conn):
    _seed_history(conn)
    run_id = "cand-suspect"
    _insert_run(conn, run_id, kind="main", commit="e" * 40, started_at=iso(0))
    _insert_measurement(conn, run_id, "solo", "BenchmarkSender", 600_000)  # +20%
    conn.commit()

    rows = verdict.historical_verdicts(conn, run_id, THRESH, harness_changed=False)
    assert rows[0].status == verdict.SUSPECTED
    assert rows[0].pct_change > 15


def test_historical_needs_min_history(conn):
    # only 3 prior runs -- below the len(history) < 5 floor -- must not alarm.
    _seed_history(conn, n=3)
    run_id = "cand-too-early"
    _insert_run(conn, run_id, kind="main", commit="d" * 40, started_at=iso(0))
    _insert_measurement(conn, run_id, "solo", "BenchmarkSender", 900_000)
    conn.commit()

    rows = verdict.historical_verdicts(conn, run_id, THRESH, harness_changed=False)
    assert rows[0].status == verdict.NEUTRAL


def test_canary_within_run_drift_marks_noisy(conn):
    run_id = "r-canary-noisy"
    _insert_run(conn, run_id, kind="main", commit="c" * 40, started_at=iso(0))
    _insert_measurement(conn, run_id, "solo", "BenchmarkActionMarshalling-canary-before", 100_000)
    _insert_measurement(conn, run_id, "solo", "BenchmarkActionMarshalling-canary-after", 112_000)  # +12%
    conn.commit()

    noisy, reason = verdict.check_canary(conn, run_id, THRESH, side="solo")
    assert noisy
    assert "within the run" in reason


def test_canary_stable_run_not_noisy(conn):
    run_id = "r-canary-stable"
    _insert_run(conn, run_id, kind="main", commit="c" * 40, started_at=iso(0))
    _insert_measurement(conn, run_id, "solo", "BenchmarkActionMarshalling-canary-before", 100_000)
    _insert_measurement(conn, run_id, "solo", "BenchmarkActionMarshalling-canary-after", 101_000)
    conn.commit()

    noisy, _ = verdict.check_canary(conn, run_id, THRESH, side="solo")
    assert not noisy


def test_persist_and_rerun_replaces_rows(conn):
    run_id = "r-persist"
    rows = [verdict.VerdictRow("BenchmarkSender", _PARAMS, "ns/op", 500_000, 505_000, 1.0, verdict.NEUTRAL)]
    _insert_run(conn, run_id, kind="pr", commit="a" * 40, started_at=iso(0))
    verdict.persist(conn, run_id, rows)
    verdict.persist(conn, run_id, rows)  # re-persisting must not duplicate
    got = conn.execute("SELECT * FROM verdicts WHERE run_id=?", (run_id,)).fetchall()
    assert len(got) == 1
