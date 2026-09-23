from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from perflab import ingest  # noqa: E402

from .conftest import iso, write_bench_file, write_meta


def test_ingest_solo_run_multi_sample(layout, conn):
    run_id = "20260801T000000Z-main-abc123456"
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True)
    write_meta(run_dir, run_id=run_id, kind="main", commit="abc123456"*4, started_at=iso(0))
    write_bench_file(run_dir, "BenchmarkSender", "solo", [500_000, 502_000, 498_000, 501_000, 499_500, 500_500])

    got_id = ingest.ingest_run(conn, layout.measurements_jsonl, run_dir)
    assert got_id == run_id

    rows = conn.execute(
        "SELECT * FROM measurements WHERE run_id=? AND metric='ns/op'", (run_id,)
    ).fetchall()
    assert len(rows) == 6
    assert {r["sample_idx"] for r in rows} == {0, 1, 2, 3, 4, 5}
    assert all(r["side"] == "solo" for r in rows)

    # every metric present (ns/op, B/op, allocs/op) x 6 samples
    all_rows = conn.execute("SELECT * FROM measurements WHERE run_id=?", (run_id,)).fetchall()
    assert len(all_rows) == 18

    lines = layout.measurements_jsonl.read_text().strip().splitlines()
    assert len(lines) == 18


def test_ingest_is_idempotent(layout, conn):
    run_id = "20260801T010000Z-main-def456789"
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True)
    write_meta(run_dir, run_id=run_id, kind="main", commit="def456789"*4, started_at=iso(0))
    write_bench_file(run_dir, "BenchmarkSender", "solo", [500_000])

    ingest.ingest_run(conn, layout.measurements_jsonl, run_dir)
    ingest.ingest_run(conn, layout.measurements_jsonl, run_dir)  # re-ingest

    rows = conn.execute("SELECT * FROM measurements WHERE run_id=?", (run_id,)).fetchall()
    assert len(rows) == 3  # not doubled
    runs = conn.execute("SELECT * FROM runs WHERE run_id=?", (run_id,)).fetchall()
    assert len(runs) == 1

    # jsonl grows by an append each call though -- that's fine, it's a log.
    lines = layout.measurements_jsonl.read_text().strip().splitlines()
    assert len(lines) == 6


def test_ingest_ab_run_tags_sides_and_sample_index(layout, conn):
    run_id = "20260801T020000Z-pr-000000001"
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True)
    write_meta(run_dir, run_id=run_id, kind="pr", commit="0" * 40, started_at=iso(0),
               baseline="1" * 40)
    for i, (b, h) in enumerate([(500_000, 505_000), (501_000, 506_000), (499_000, 504_000)]):
        write_bench_file(run_dir, "BenchmarkSender", f"base_{i}", [b])
        write_bench_file(run_dir, "BenchmarkSender", f"head_{i}", [h])

    ingest.ingest_run(conn, layout.measurements_jsonl, run_dir)

    base_rows = conn.execute(
        "SELECT sample_idx, value FROM measurements WHERE run_id=? AND side='base' "
        "AND metric='ns/op' ORDER BY sample_idx", (run_id,),
    ).fetchall()
    head_rows = conn.execute(
        "SELECT sample_idx, value FROM measurements WHERE run_id=? AND side='head' "
        "AND metric='ns/op' ORDER BY sample_idx", (run_id,),
    ).fetchall()
    assert [r["sample_idx"] for r in base_rows] == [0, 1, 2]
    assert [r["value"] for r in base_rows] == [500_000, 501_000, 499_000]
    assert [r["value"] for r in head_rows] == [505_000, 506_000, 504_000]


def test_real_pre_flags_fixture_parses(layout, conn):
    """A real captured output file predating -proof_type/-executor (the flags
    only change the go test command line, never the text format `simple_parser`
    reads), confirming ingest works unmodified across that boundary."""
    run_id = "20260701T000000Z-main-fadefade0"
    run_dir = layout.run_dir(run_id)
    run_dir.mkdir(parents=True)
    write_meta(run_dir, run_id=run_id, kind="main", commit="fadefade0" * 4, started_at=iso(30))
    fixture = Path(__file__).parent / "fixtures" / "pre_586d4f58_sender.txt"
    (run_dir / "BenchmarkSender.solo.txt").write_text(fixture.read_text())

    ingest.ingest_run(conn, layout.measurements_jsonl, run_dir)
    rows = conn.execute(
        "SELECT * FROM measurements WHERE run_id=? AND metric='ns/op'", (run_id,)
    ).fetchall()
    assert len(rows) > 0
