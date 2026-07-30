"""Lightweight parser for `go test -bench` text output.

This module holds only the pure-parsing logic so it can be imported without
pulling in plotting/UI dependencies (plotly, streamlit). Both the plotting
tool (``plotly_plot_node.py``) and the PR comparison tool
(``compare_benchmarks.py``) import :func:`simple_parser` from here, keeping a
single source of truth for how a benchmark line is turned into a row.
"""

from pathlib import Path
from typing import Any

import pandas as pd


def _is_number(s: str) -> bool:
    """Return True if ``s`` parses as a float."""
    try:
        float(s)
        return True
    except ValueError:
        return False


def _split_workers(name: str) -> tuple[str, int]:
    """Split a trailing ``-N`` worker count off a name, defaulting to 1."""
    base, sep, tail = name.rpartition("-")
    if sep and tail.isdigit():
        return base, int(tail)
    return name, 1


def simple_parser(path: Path) -> pd.DataFrame:
    """Parse a ``go test -bench`` output file into a tidy DataFrame.

    Each ``Benchmark...`` line becomes one row: the benchmark name, its
    ``key=value`` sub-parameters, the trailing ``-N`` worker count, the
    iteration count, and every ``<value> <metric-name>`` metric pair
    (e.g. ``ns/op``, ``TPS``). A ``tps`` column and a Little's-Law
    ``avg (ms)`` column are derived when the inputs are present.
    """
    rows = []
    for ln in path.read_text().splitlines():
        if not ln.startswith("Benchmark"):
            continue
        first, *cols = ln.split()
        if not cols:
            continue

        bench_name, *params = first.split("/")
        if params:
            last_param, workers = _split_workers(params[-1])
            row: dict[str, Any] = {"bench": bench_name, "workers": workers}
            for p in [*params[:-1], last_param]:
                if "=" in p:
                    k, v = p.split("=", 1)
                    row[k] = v
        else:
            bench_name, workers = _split_workers(bench_name)
            row = {"bench": bench_name, "workers": workers}

        row["iterations"] = int(cols.pop(0))

        # Each metric is a value followed by its (multi-word) name.
        i = 0
        while i < len(cols):
            j = i + 1
            while j < len(cols) and not _is_number(cols[j]):
                j += 1
            row[" ".join(cols[i + 1:j])] = float(cols[i])
            i = j

        rows.append(row)

    df = pd.DataFrame(rows)
    if df.empty:
        return df

    if "TPS" in df.columns:
        df = df.rename(columns={"TPS": "tps"})
    for col in [c for c in df.columns if c.startswith("ns/op") and "(" in c]:
        label = col.split("(")[1].rstrip(")")
        df[f"{label} (ms)"] = df[col] / 1e6
        df = df.drop(columns=[col])

    if 'tps' in df.columns and 'workers' in df.columns:
        # Little's Law (latency = concurrency / throughput)
        # concurrency = workers, tps is in seconds (so X 1000 to get ms)
        df['avg (ms)'] = df['workers'] * 1000 / df['tps']

    return df
