#!/usr/bin/env python3
"""
plot_benchmark_results.py: Generates comparison plots across executor
strategies (serial, unbounded, pool) for each parallel benchmark test.

Executor strategies:
  serial: executes tasks sequentially in the same goroutine (baseline, no concurrency)
  unbounded: spawns a new goroutine per task (maximum concurrency, higher overhead)
  pool: uses a fixed-size worker pool (bounded concurrency, better control and stability)

Reads benchmark_results.csv produced by run_benchmarks.py and outputs
a PDF with two plots per test:
  Left:  TPS vs worker count, one line per executor strategy
  Right: TPS vs mean latency (with std error bars), one series per
         executor × worker combination
"""

import sys
import pandas as pd
import matplotlib.pyplot as plt
from matplotlib.backends.backend_pdf import PdfPages
import re
import argparse
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument(
    "results_file",
    nargs="?",
    default="benchmark_results.csv",
    help="Path to the results CSV file (default: benchmark_results.csv)"
)
args = parser.parse_args()

csv_path = args.results_file
pdf_path = str(Path(csv_path).with_suffix(".pdf"))

test_names = [
    "TestParallelBenchmarkSender",
    "TestParallelBenchmarkVerificationSenderProof",
    "TestParallelBenchmarkTransferProofGeneration",
    "TestParallelBenchmarkTransferServiceTransfer",
    "TestParallelBenchmarkValidatorTransfer",
    "TestParallelBenchmarkIssueServiceIssue",
    "TestParallelBenchmarkAuditorServiceCheck",
]

executors     = ["serial", "unbounded", "pool"]
executor_colors = {
    "serial":    "tab:blue",
    "unbounded": "tab:orange",
    "pool":      "tab:green",
}
executor_markers = {
    "serial":    "o",
    "unbounded": "s",
    "pool":      "^",
}

if not Path(csv_path).exists():
    print(f"Error: Results file '{csv_path}' not found.", file=sys.stderr)
    sys.exit(1)

df = pd.read_csv(csv_path)
if df.empty:
    print(f"Error: Results file '{csv_path}' is empty.", file=sys.stderr)
    sys.exit(1)

last_row  = df.iloc[-1]
timestamp = last_row.get("timestamp", "N/A")

# Column pattern: TestParallelBenchmarkSender[pool]/8 tps
col_re = re.compile(
    r"^(.+?)\[(\w+)\]/(\d+)\s+(tps|lat-p95|lat-avg|lat-std|goroutines)$"
)

def get_value(row, test, executor, cpu, metric):
    col = f"{test}[{executor}]/{cpu} {metric}"
    val = row.get(col, None)
    if val is None or pd.isna(val):
        # Fall back to old-style column without executor tag
        col_old = f"{test}/{cpu} {metric}"
        val = row.get(col_old, None)
    return val

with PdfPages(pdf_path) as pdf:
    for test_name in test_names:

        # Discover which worker counts are present for this test
        worker_set = set()
        for col in df.columns:
            m = col_re.match(col)
            if m and m.group(1) == test_name:
                worker_set.add(int(m.group(3)))
        if not worker_set:
            # Fall back to old-style columns without executor tag
            old_re = re.compile(rf"^{re.escape(test_name)}/(\d+)\s+tps$")
            for col in df.columns:
                om = old_re.match(col)
                if om:
                    worker_set.add(int(om.group(1)))
        if not worker_set:
            continue

        workers_sorted = sorted(worker_set)

        fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(18, 7))
        fig.suptitle(f"{test_name}\n(last run: {timestamp})", fontsize=12)

        # ---- Left plot: TPS vs workers, one line per executor ----
        for executor in executors:
            tps_vals = []
            w_vals   = []
            for cpu in workers_sorted:
                v = get_value(last_row, test_name, executor, cpu, "tps")
                if v is not None and not pd.isna(v):
                    tps_vals.append(float(v))
                    w_vals.append(cpu)
            if tps_vals:
                ax1.plot(
                    w_vals, tps_vals,
                    marker=executor_markers[executor],
                    linestyle="-",
                    color=executor_colors[executor],
                    label=executor,
                )

        ax1.set_xlabel("Worker count")
        ax1.set_ylabel("TPS")
        ax1.set_title("TPS vs Worker Count")
        ax1.legend(title="Executor")
        ax1.grid(True)

        # ---- Right plot: TPS vs mean latency with error bars ----
        # One point per (executor, worker) combination
        p95_marker = "X"
        plotted_executors = set()
        for executor in executors:
            for cpu in workers_sorted:
                tps   = get_value(last_row, test_name, executor, cpu, "tps")
                avg   = get_value(last_row, test_name, executor, cpu, "lat-avg")
                std   = get_value(last_row, test_name, executor, cpu, "lat-std")
                p95   = get_value(last_row, test_name, executor, cpu, "lat-p95")

                if any(v is None or pd.isna(v) for v in [tps, avg, std, p95]):
                    continue

                label = executor if executor not in plotted_executors else None
                ax2.errorbar(
                    float(tps), float(avg),
                    yerr=float(std),
                    marker=executor_markers[executor],
                    color=executor_colors[executor],
                    label=label,
                    capsize=4,
                    capthick=1.5,
                    linestyle="None",
                )
                plotted_executors.add(executor)
                ax2.scatter(
                    float(tps), float(p95),
                    marker=p95_marker,
                    color=executor_colors[executor],
                    s=80,
                    zorder=3,
                )
                # Annotate worker count
                ax2.annotate(
                    str(cpu),
                    (float(tps), float(avg)),
                    textcoords="offset points",
                    xytext=(4, 4),
                    fontsize=7,
                    color=executor_colors[executor],
                )

        # Dummy entry for p95 marker in legend
        ax2.scatter([], [], marker=p95_marker, color="black", label="p95")

        ax2.set_xlabel("Throughput (TPS)")
        ax2.set_ylabel("Mean Latency [ms]")
        ax2.set_title("TPS vs Latency (dot=mean±std, X=p95, label=workers)")
        ax2.legend(title="Executor")
        ax2.grid(True)

        plt.tight_layout()
        pdf.savefig(fig)
        plt.close(fig)

    print(f"Report saved as {pdf_path}")