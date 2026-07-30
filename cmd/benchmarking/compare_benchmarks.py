"""Compare two sets of benchmark outputs and emit a Markdown report.

Given a directory of ``go test -bench`` output files split into a *base* group
(the target branch, e.g. ``main``) and a *PR* group, this pairs each benchmark
row across the two groups and reports the percentage change in latency
(``ns/op``) and throughput (``TPS``). The result is written as a Markdown table
suitable for posting as a pull-request comment.

Files are assigned to a group by a substring match on their name:
``--base-tag`` marks base-branch files, ``--pr-tag`` marks PR-branch files.

Example
-------
    python compare_benchmarks.py \
        --input-dir bench \
        --base-tag _base_ --pr-tag _pr_ \
        --output comment.md
"""

from __future__ import annotations

import argparse
from pathlib import Path

import pandas as pd

from bench_parse import simple_parser

# Measurement/derived columns produced by the parser. These vary run-to-run
# and must never be part of a row's identity key — only the benchmark's input
# parameters (the ``key=value`` sub-tests) identify a row.
_MEASUREMENT_COLS = {"iterations", "ns/op", "tps", "avg (ms)"}
# Only throughput (TPS) is reported. ns/op is still parsed (and excluded from
# row identity via _MEASUREMENT_COLS) but intentionally not shown in the table.
_METRIC_COLS = ("tps",)

# How each metric trends: for throughput (TPS) higher is better. Used only to
# pick the emoji, never the number.
_LOWER_IS_BETTER = {"tps": False}


# Crypto variants recognised in filenames (e.g. ``...-ipa_base_.txt``). The
# variant is not present in the benchmark line itself, so it is carried as a
# column and made part of a row's identity — otherwise ipa and csp rows for the
# same benchmark would collide.
_VARIANTS = ("ipa", "csp")

_DEFAULT_DELTA = 1.0


def _variant_of(name: str) -> str:
    """Extract the crypto variant from a benchmark filename, or 'unknown'."""
    for v in _VARIANTS:
        if f"-{v}_" in name:
            return v
    return "unknown"


def _row_key(row: pd.Series, param_cols: list[str]) -> tuple:
    """Build a hashable identity for a benchmark row from its parameters.

    Missing parameters (``NaN`` after concatenating benchmarks with different
    parameter sets) are normalised to ``None`` so that two rows describing the
    same benchmark compare equal — ``NaN != NaN`` would otherwise break pairing.
    """
    def norm(v):
        return None if pd.isna(v) else v

    return (row["variant"], row["bench"], row["workers"],
            *(norm(row.get(c)) for c in param_cols))


def _load_group(input_dir: Path, tag: str) -> pd.DataFrame:
    """Parse and concatenate every file in ``input_dir`` whose name contains ``tag``.

    Each row is tagged with the crypto ``variant`` parsed from its filename.
    """
    frames = []
    for f in sorted(input_dir.glob("*.txt")):
        if tag not in f.name:
            continue
        df = simple_parser(f)
        if df.empty:
            continue
        df["variant"] = _variant_of(f.name)
        frames.append(df)
    if not frames:
        return pd.DataFrame()
    return pd.concat(frames, ignore_index=True)


def _pct(base: float, new: float) -> float:
    """Percentage change from ``base`` to ``new`` (0 when base is 0)."""
    if base == 0:
        return 0.0
    return (new - base) / base * 100.0


def _ranges_overlap(base_samples: list, pr_samples: list) -> bool:
    """True if the base and PR sample ranges overlap (change not distinguishable).

    Even measured on the same runner, benchmarks jitter sample-to-sample. If the
    two branches' observed [min, max] ranges overlap, the delta cannot be told
    apart from that jitter, so it should not be reported as a real change. With
    fewer than two usable samples on either side there is no range to compare, so
    we conservatively treat it as overlapping (not significant).
    """
    if len(base_samples) < 2 or len(pr_samples) < 2:
        return True
    return min(base_samples) <= max(pr_samples) and min(pr_samples) <= max(base_samples)


def _emoji(metric: str, pct: float, base_samples: list, pr_samples: list, delta: float = _DEFAULT_DELTA) -> str:
    """Pick an indicator for a change, given whether lower is better.

    Returns the neutral marker when the change is within the ±1% noise band or
    when the base/PR sample ranges overlap (i.e. the delta is not statistically
    distinguishable from run-to-run jitter).
    """
    if abs(pct) < delta:  # treat sub-1% as noise
        return "➖"
    if _ranges_overlap(base_samples, pr_samples):
        return "➖"
    improved = (pct < 0) == _LOWER_IS_BETTER[metric]
    return "🟢" if improved else "🔴"


def build_report(base: pd.DataFrame, pr: pd.DataFrame, delta: float = _DEFAULT_DELTA) -> str:
    """Render the Markdown comparison table for two parsed benchmark groups."""
    if base.empty or pr.empty:
        missing = "base" if base.empty else "PR"
        return f"⚠️ No benchmark results found for the **{missing}** branch."

    # Parameter columns identify a row: everything except variant/bench/workers,
    # the measurements, and any derived latency column (``... (ms)``).
    non_param = {"variant", "bench", "workers", *_MEASUREMENT_COLS}
    param_cols = sorted(
        c for c in set(base.columns) | set(pr.columns)
        if c not in non_param and not c.endswith("(ms)")
    )

    # With ``go test -count=N`` each benchmark line appears N times. Average the
    # metrics per identity key so the comparison reflects the mean, not whichever
    # repeat happened to be parsed last.
    def by_key(df: pd.DataFrame) -> dict:
        acc: dict = {}
        for _, r in df.iterrows():
            acc.setdefault(_row_key(r, param_cols), []).append(r)
        out = {}
        for key, rows in acc.items():
            agg = rows[0].copy()
            for metric in _METRIC_COLS:
                vals = [r.get(metric)
                        for r in rows if not pd.isna(r.get(metric))]
                agg[metric] = sum(vals) / len(vals) if vals else float("nan")
                # Retain the raw per-count samples so the significance guard in
                # ``_emoji`` can compare the base/PR ranges, not just the means.
                # A plain string key avoids pandas treating a tuple as a
                # multi-index label on the Series.
                agg[f"_samples_{metric}"] = vals
            out[key] = agg
        return out

    base_by_key = by_key(base)
    pr_by_key = by_key(pr)

    lines = [
        "## 📊 Token Validation Benchmark",
        "",
        (
            "Comparison of this PR against the base branch. "
            f"🟢 improvement · 🔴 regression · ➖ within ±{delta}% noise."
        ),
        "",
        "| Variant | Benchmark | Params | Workers | TPS (base → PR) | Δ TPS |",
        "|---|---|---|---|---|---|",
    ]

    for key in sorted(pr_by_key.keys() & base_by_key.keys(), key=lambda k: tuple(map(str, k))):
        b, p = base_by_key[key], pr_by_key[key]
        variant, bench, workers, *param_vals = key
        params = ", ".join(
            f"{c}={v}" for c, v in zip(param_cols, param_vals) if v is not None
        ) or "—"

        cells = [variant, bench, params, str(workers)]
        for metric in _METRIC_COLS:
            bv, pv = b.get(metric), p.get(metric)
            if pd.isna(bv) or pd.isna(pv):
                cells += ["n/a", "n/a"]
                continue
            pct = _pct(bv, pv)
            base_samples = b.get(f"_samples_{metric}", [])
            pr_samples = p.get(f"_samples_{metric}", [])
            cells.append(f"{bv:,.0f} → {pv:,.0f}")
            cells.append(
                f"{_emoji(metric, pct, base_samples, pr_samples)} {pct:+.1f}%")
        lines.append("| " + " | ".join(cells) + " |")

    only_pr = pr_by_key.keys() - base_by_key.keys()
    only_base = base_by_key.keys() - pr_by_key.keys()
    if only_pr:
        lines += ["",
                  f"> ℹ️ {len(only_pr)} benchmark(s) present only on the PR branch (new)."]
    if only_base:
        lines += ["",
                  f"> ℹ️ {len(only_base)} benchmark(s) present only on the base branch (removed/renamed)."]

    return "\n".join(lines) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input-dir", type=Path, required=True,
                        help="Directory containing base and PR .txt outputs")
    parser.add_argument("--base-tag", default="_base_",
                        help="Filename substring marking base-branch files")
    parser.add_argument("--delta", type=float, default=_DEFAULT_DELTA,
                        help="Percent for comparison that is neglected, default: 1")
    parser.add_argument("--pr-tag", default="_pr_",
                        help="Filename substring marking PR-branch files")
    parser.add_argument("--output", type=Path, default=Path("comment.md"),
                        help="Where to write the Markdown report")
    args = parser.parse_args()

    if not args.input_dir.exists():
        raise FileNotFoundError(args.input_dir)
    if not (0 <= args.delta <= 100):
        raise ValueError("Delta must be between 1 and 100")

    base = _load_group(args.input_dir, args.base_tag)
    pr = _load_group(args.input_dir, args.pr_tag)
    report = build_report(base, pr, delta=args.delta)

    args.output.write_text(report)
    print(report)


if __name__ == "__main__":
    main()
