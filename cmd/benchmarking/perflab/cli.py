"""`perflab` command-line entry point.

    perflab doctor
    perflab run --sha SHA [--baseline SHA] [--suite tier1] [--dry-run]
    perflab ingest --run-dir PATH
    perflab verdict --run-id ID
    perflab report
    perflab poll
    perflab work [--once]
    perflab backfill [--since-days N] [--stride N]
    perflab enqueue --pr N | (--sha SHA [--baseline SHA])
    perflab nightly

See docs/development/perflab.md for the operator runbook.
"""

from __future__ import annotations

import argparse
import json
import sys
import traceback
from pathlib import Path

from . import capability, db, gitutil, ingest, poll, queue, report, runner, tier2, verdict
from .config import PerflabConfig, load as load_config
from .paths import Layout, default_layout


def _cfg_and_layout() -> tuple[PerflabConfig, Layout]:
    layout = default_layout()
    layout.ensure()
    cfg = load_config(layout.config_path if layout.config_path.exists() else None)
    return cfg, layout


def cmd_doctor(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    rep = runner.doctor(cfg)
    for k, v in rep.checks.items():
        print(f"{k:24s} {v}")
    print()
    print("OK" if rep.ok else "FAIL")
    report.write_health(layout, rep.checks, rep.ok)
    return 0 if rep.ok else 1


def _dry_run_commands(cfg: PerflabConfig) -> None:
    caps = capability.Capabilities()  # optimistic: both probe flags assumed supported
    for spec in cfg.tier1.benchmarks:
        cmd = runner._bench_cmd(Path("."), spec, cfg, caps, cfg.tier1.count,
                                 cfg.tier1.benchtime, cfg.tier1.cpu)
        print(" ".join(["taskset", "-c", cfg.bench_cpuset, *cmd]))


def cmd_run(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    if args.dry_run:
        _dry_run_commands(cfg)
        return 0

    if args.suite == "tier2":
        run_id = tier2.run_tier2(cfg, layout, args.sha, kind=args.kind or "main",
                                  pr_number=args.pr_number)
        conn = db.connect(layout.db_path)
        tier2.ingest_tier2(conn, layout.measurements_jsonl, layout.run_dir(run_id))
        conn.close()
        print(f"run_id={run_id} (tier2 -- no verdict engine, see dashboard for the raw series)")
        return 0

    if args.baseline:
        run_id = runner.run_ab(cfg, layout, args.baseline, args.sha,
                                kind=args.kind or "confirm", pr_number=args.pr_number)
    else:
        run_id = runner.run_solo(cfg, layout, args.sha, kind=args.kind or "main")

    conn = db.connect(layout.db_path)
    ingest.ingest_run(conn, layout.measurements_jsonl, layout.run_dir(run_id))
    _compute_and_print_verdict(conn, cfg, run_id)
    conn.close()
    print(f"run_id={run_id}")
    return 0


def cmd_ingest(args: argparse.Namespace) -> int:
    _, layout = _cfg_and_layout()
    conn = db.connect(layout.db_path)
    run_id = ingest.ingest_run(conn, layout.measurements_jsonl, Path(args.run_dir))
    conn.close()
    print(f"ingested run_id={run_id}")
    return 0


def _compute_and_print_verdict(conn, cfg: PerflabConfig, run_id: str) -> list[verdict.VerdictRow]:
    row = conn.execute("SELECT kind, bench_code_hash FROM runs WHERE run_id=?", (run_id,)).fetchone()
    kind = row["kind"]
    solo_noisy, solo_reason = verdict.check_canary(conn, run_id, cfg.thresholds, side="solo")
    ab_noisy = False
    if kind in ("pr", "confirm"):
        for side in ("base", "head"):
            n, reason = verdict.check_canary(conn, run_id, cfg.thresholds, side=side)
            if n:
                ab_noisy, solo_reason = True, reason
    noisy = solo_noisy or ab_noisy
    conn.execute("UPDATE runs SET noisy=?, noisy_reason=? WHERE run_id=?",
                 (1 if noisy else 0, solo_reason, run_id))
    conn.commit()

    harness_changed = False
    baseline_sha = conn.execute(
        "SELECT baseline_sha FROM runs WHERE run_id=?", (run_id,)
    ).fetchone()["baseline_sha"]
    if baseline_sha:
        base_hash_row = conn.execute(
            "SELECT bench_code_hash FROM runs WHERE commit_sha=? ORDER BY started_at DESC LIMIT 1",
            (baseline_sha,),
        ).fetchone()
        # meta.json stores base_bench_code_hash on the run itself for A/B jobs;
        # fall back to a same-sha lookup if that ever isn't available.
        head_hash = row["bench_code_hash"]
        if base_hash_row and head_hash and base_hash_row["bench_code_hash"] != head_hash:
            harness_changed = True

    if kind in ("pr", "confirm"):
        rows = verdict.pairwise_verdicts(conn, run_id, cfg.thresholds, harness_changed)
    else:
        rows = verdict.historical_verdicts(conn, run_id, cfg.thresholds, harness_changed)

    verdict.persist(conn, run_id, rows)
    summary = verdict.render_summary_md(run_id, kind, rows)
    run_dir = default_layout().run_dir(run_id)
    if run_dir.exists():
        (run_dir / "summary.md").write_text(summary)
    print(summary)

    if kind == "main" and not noisy:
        suspects = [r for r in rows if r.status == verdict.SUSPECTED]
        if suspects:
            commit_sha = conn.execute(
                "SELECT commit_sha, parent_sha FROM runs WHERE run_id=?", (run_id,)
            ).fetchone()
            if commit_sha["parent_sha"]:
                q = queue.Queue(default_layout().queue_db_path)
                q.enqueue("confirm", commit_sha["commit_sha"], "tier1",
                          baseline=commit_sha["parent_sha"], priority=5,
                          payload={"reason": "historical-suspect",
                                   "benches": [r.bench for r in suspects]})
                q.close()
                print(f"enqueued confirmation job for {len(suspects)} suspected series")
    return rows


def cmd_verdict(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    conn = db.connect(layout.db_path)
    _compute_and_print_verdict(conn, cfg, args.run_id)
    conn.close()
    return 0


def cmd_report(args: argparse.Namespace) -> int:
    _, layout = _cfg_and_layout()
    conn = db.connect(layout.db_path)
    report.generate(layout, conn)
    conn.close()
    print(f"wrote {layout.www}/index.html")
    return 0


def cmd_poll(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    conn = db.connect(layout.db_path)
    q = queue.Queue(layout.queue_db_path)
    n_main = poll.poll_main(cfg, layout, q, conn)
    n_pr = poll.poll_prs(cfg, layout, q)
    q.close()
    conn.close()
    print(f"enqueued {n_main} main job(s), {n_pr} PR job(s)")
    return 0


def _run_one_job(cfg: PerflabConfig, layout: Layout, job: queue.Job) -> None:
    conn = db.connect(layout.db_path)
    try:
        if job.suite == "tier2":
            run_id = tier2.run_tier2(cfg, layout, job.sha, kind=job.kind, pr_number=job.pr_number)
            tier2.ingest_tier2(conn, layout.measurements_jsonl, layout.run_dir(run_id))
            return
        if job.kind in ("pr", "confirm"):
            run_id = runner.run_ab(cfg, layout, job.baseline, job.sha,
                                    kind=job.kind, pr_number=job.pr_number)
        else:
            run_id = runner.run_solo(cfg, layout, job.sha, kind=job.kind)
        ingest.ingest_run(conn, layout.measurements_jsonl, layout.run_dir(run_id))
        _compute_and_print_verdict(conn, cfg, run_id)
    finally:
        conn.close()


def cmd_work(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    q = queue.Queue(layout.queue_db_path)
    processed = 0
    while True:
        rep = runner.doctor(cfg)
        if not rep.ok:
            print("doctor check failed, refusing to claim a job:", json.dumps(rep.checks))
            if args.once:
                return 1
            import time
            time.sleep(30)
            continue

        job = q.claim()
        if job is None:
            if args.once:
                print("no pending jobs")
                return 0
            import time
            time.sleep(5)
            continue

        print(f"running job #{job.id}: {job.kind} {job.sha[:9]} suite={job.suite}")
        try:
            _run_one_job(cfg, layout, job)
            q.complete(job.id)
            conn = db.connect(layout.db_path)
            report.generate(layout, conn)
            conn.close()
        except Exception as exc:  # noqa: BLE001 - job failures must not kill the worker loop
            traceback.print_exc()
            q.fail(job.id, str(exc))
        processed += 1
        if args.once:
            return 0


def cmd_backfill(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    conn = db.connect(layout.db_path)
    q = queue.Queue(layout.queue_db_path)
    n = poll.backfill(cfg, layout, q, conn, since_days=args.since_days, stride=args.stride)
    q.close()
    conn.close()
    print(f"enqueued {n} backfill job(s)")
    return 0


def cmd_nightly(args: argparse.Namespace) -> int:
    """Enqueue a Tier-2 deep sweep on current `main` HEAD (perflab-nightly.timer)."""
    cfg, layout = _cfg_and_layout()
    gitutil.clone_or_fetch(f"https://github.com/{cfg.github.owner}/{cfg.github.repo}.git", layout.repo)
    head = gitutil.resolve_ref(layout.repo, f"origin/{cfg.github.branch}")
    q = queue.Queue(layout.queue_db_path)
    job_id = q.enqueue("main", head, "tier2", priority=50)
    q.close()
    print(f"enqueued tier2 job_id={job_id} for {head[:9]}" if job_id else "already queued (deduped)")
    return 0


def cmd_enqueue(args: argparse.Namespace) -> int:
    cfg, layout = _cfg_and_layout()
    q = queue.Queue(layout.queue_db_path)
    if args.pr is not None:
        prs = {p.number: p for p in gitutil.list_open_prs(cfg.github)}
        pr = prs.get(args.pr)
        if pr is None:
            print(f"PR #{args.pr} not found among open PRs", file=sys.stderr)
            return 1
        base = gitutil.merge_base(layout.repo, pr.base_sha, pr.head_sha)
        job_id = q.enqueue("pr", pr.head_sha, args.suite, baseline=base,
                            pr_number=pr.number, priority=1)
    else:
        job_id = q.enqueue("confirm" if args.baseline else "main", args.sha, args.suite,
                            baseline=args.baseline, priority=1)
    q.close()
    print(f"enqueued job_id={job_id}" if job_id else "already queued (deduped)")
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="perflab")
    sub = p.add_subparsers(dest="command", required=True)

    sub.add_parser("doctor").set_defaults(func=cmd_doctor)

    pr = sub.add_parser("run")
    pr.add_argument("--sha", required=True)
    pr.add_argument("--baseline")
    pr.add_argument("--suite", default="tier1", choices=["tier1", "tier2"])
    pr.add_argument("--kind", choices=["main", "pr", "confirm"])
    pr.add_argument("--pr-number", type=int, dest="pr_number")
    pr.add_argument("--dry-run", action="store_true")
    pr.set_defaults(func=cmd_run)

    pi = sub.add_parser("ingest")
    pi.add_argument("--run-dir", required=True)
    pi.set_defaults(func=cmd_ingest)

    pv = sub.add_parser("verdict")
    pv.add_argument("--run-id", required=True)
    pv.set_defaults(func=cmd_verdict)

    sub.add_parser("report").set_defaults(func=cmd_report)
    sub.add_parser("poll").set_defaults(func=cmd_poll)

    pw = sub.add_parser("work")
    pw.add_argument("--once", action="store_true", help="process at most one job, then exit")
    pw.set_defaults(func=cmd_work)

    pb = sub.add_parser("backfill")
    pb.add_argument("--since-days", type=int, dest="since_days")
    pb.add_argument("--stride", type=int)
    pb.set_defaults(func=cmd_backfill)

    pe = sub.add_parser("enqueue")
    pe.add_argument("--pr", type=int)
    pe.add_argument("--sha")
    pe.add_argument("--baseline")
    pe.add_argument("--suite", default="tier1", choices=["tier1", "tier2"])
    pe.set_defaults(func=cmd_enqueue)

    sub.add_parser("nightly").set_defaults(func=cmd_nightly)

    return p


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
