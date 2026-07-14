#!/usr/bin/env python3
"""
run_benchmarks.py:  Automates the full benchmark matrix and produces
benchmark_results.csv with executor strategy as an additional dimension.

New dimensions vs the original script:
  --executor   serial|unbounded|pool|all  (default: all)
  --proof_type bf|csp|all                 (default: bf)

Column naming convention:
  TestParallelBenchmarkSender[pool]/8 tps
  TestParallelBenchmarkSender[pool]/8 lat-p95
  TestParallelBenchmarkSender[pool]/8 lat-avg
  TestParallelBenchmarkSender[pool]/8 lat-std
  TestParallelBenchmarkSender[pool]/8 goroutines
"""

import csv
import os
import sys
import shutil
import subprocess
import re
import argparse
from datetime import datetime
from collections import defaultdict
from itertools import count as counter
from pathlib import Path

# ----- CLI -----

parser = argparse.ArgumentParser(description="run_benchmarks.py:- benchmark automation with executor dimension")
parser.add_argument("--count",      type=int, default=10,   help="Repetitions for non-parallel benchmarks (default: 10)")
parser.add_argument("--timeout",    type=str, default="0",  help="Go test timeout (default: 0 = no timeout)")
parser.add_argument("--benchName",  type=str, default="",   help="Run a single named benchmark (default: all)")
parser.add_argument("--executor",   type=str, default="all",
                    help="Executor strategy: serial|unbounded|pool|all (default: all)")
parser.add_argument("--proof_type", type=str, default="bf",
                    help="Range proof system: bf|csp|all (default: bf)")
parser.add_argument("--duration",   type=str, default="60s",
                    help="Duration for parallel benchmarks (default: 60s)")
parser.add_argument("--cpus",       type=str, default="1,2,4,8,16,32",
                    help="Comma-separated CPU counts for parallel benchmarks (default: 1,2,4,8,16,32)")

args = parser.parse_args()
count      = args.count
timeout    = args.timeout
benchName  = args.benchName
duration   = args.duration

# Resolve executor strategies to run
ALL_EXECUTORS = ["serial", "unbounded", "pool"]
if args.executor == "all":
    executors = ALL_EXECUTORS
elif args.executor in ALL_EXECUTORS:
    executors = [args.executor]
else:
    raise ValueError(f"Invalid --executor value: {args.executor}. Choose serial, unbounded, pool, or all.")

# Resolve proof types to run
ALL_PROOF_TYPES = ["bf", "csp"]
if args.proof_type == "all":
    proof_types = ALL_PROOF_TYPES
elif args.proof_type in ALL_PROOF_TYPES:
    proof_types = [args.proof_type]
else:
    raise ValueError(f"Invalid --proof_type value: {args.proof_type}. Choose bf, csp, or all.")

# ----- CPU counts -----
cpus = [int(c) for c in args.cpus.split(",")]


# ----- Paths -----

TOKENSDK_ROOT              = os.environ.get("TOKENSDK_ROOT", "../../")
TOKENSDK_ROOT              = Path(TOKENSDK_ROOT)
v1_benchmarks_folder       = TOKENSDK_ROOT / "token/core/zkatdlog/nogh/v1"
transfer_benchmarks_folder = TOKENSDK_ROOT / "token/core/zkatdlog/nogh/v1/transfer"
issuer_benchmarks_folder   = TOKENSDK_ROOT / "token/core/zkatdlog/nogh/v1/issue"
validator_benchmarks_folder= TOKENSDK_ROOT / "token/core/zkatdlog/nogh/v1/validator"

results_csv = "benchmark_results.csv"
results_pdf = "benchmark_results.pdf"
results_IO  = "benchmark_IOstats.csv"

output_folder_name = f"benchmark_logs_{datetime.now():%Y-%m-%d_%H-%M-%S}"
output_folder_path = os.path.join(".", output_folder_name)
os.makedirs(output_folder_path, exist_ok=True)

curves = ["BLS12_381_BBS_GURVY"]


# ----- Unit helpers -----

time_mult = {
    "ns": 1, "n": 1,
    "µs": 1_000, "µ": 1_000, "us": 1_000, "u": 1_000,
    "ms": 1_000_000, "m": 1_000_000,
    "s":  1_000_000_000,
}
ram_mult   = {"B": 1, "Ki": 1024, "Mi": 1024**2}
alloc_mult = {"": 1, "k": 1_000, "M": 1_000_000}

def to_ms(value: float, unit: str) -> int:
    return int(value * time_mult[unit] / 1_000_000)


# Counter for run numbering

RUN_COUNTER = counter(0)

def _next_run():
    n = next(RUN_COUNTER)
    return n


# IOstats helper (non-parallel benchmarks only)

def print_IOstats(d, filename):
    file_exists = os.path.exists(filename)
    with open(filename, "a", newline="") as f:
        writer = csv.writer(f)
        if not file_exists:
            writer.writerow(["bench", "bits", "I", "O", "allocs", "RAM bytes", "time ms"])
        for bench in sorted(d.keys()):
            if "time" not in d[bench]:
                continue
            for bits in sorted(d[bench]["time"].keys(), key=int):
                for Inum in sorted(d[bench]["time"][bits].keys(), key=int):
                    for Onum in sorted(d[bench]["time"][bits][Inum].keys(), key=int):
                        allocs = d[bench]["allocs"].get(bits, {}).get(Inum, {}).get(Onum, "")
                        ram    = d[bench]["ram"].get(bits, {}).get(Inum, {}).get(Onum, "")
                        time_v = d[bench]["time"].get(bits, {}).get(Inum, {}).get(Onum, "")
                        writer.writerow([bench, bits, Inum, Onum, allocs, ram, time_v])


# Non-parallel benchmark runner (executor dimension does NOT apply here: 
# these are Go benchmarks that run serially by design)

def run_and_parse_non_parallel_metrics(bench_name, params, curve="BLS12_381_BBS_GURVY",
                                        folder=transfer_benchmarks_folder, proof_type="bf") -> dict:
    if folder == "":
        folder = transfer_benchmarks_folder

    # Only add proof_type flag for benchmarks that support it (not issuer benchmarks)
    proof_flag = ""
    if folder != issuer_benchmarks_folder:
        proof_flag = f"-proof_type={proof_type}"
    
    cmd = (
        f"go test {folder} -run='^$' -bench={bench_name} -v -benchmem "
        f"-count={count} -cpu=32 -timeout {timeout} "
        f"-curves={curve} {proof_flag} {params} | tee bench.txt; benchstat bench.txt"
    )
    n = _next_run()
    print(f"{n} Running: {cmd}")

    try:
        result = subprocess.run(cmd, shell=True, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as e:
        print(f"Error executing non-parallel benchmark {bench_name}: {e}")
        if e.stdout:
            print(f"Stdout:\n{e.stdout}")
        if e.stderr:
            print(f"Stderr:\n{e.stderr}")
        raise e

    log_path = os.path.join(output_folder_path, f"{bench_name}.log")
    if not os.path.exists(log_path):
        with open(log_path, "w") as f:
            f.write(result.stdout)

    output = result.stdout.splitlines()
    d = defaultdict(lambda: defaultdict(lambda: defaultdict(lambda: defaultdict(dict))))

    # Regexes for benchmarks that embed bits/inputs/outputs in the name
    time_re       = re.compile(r"^\S+.*?bits_(\d+).*?_#i_(\d+).*?_#o_(\d+)\S*\s+([\d.]+)\s*([a-zµ]+)")
    ram_re        = re.compile(r"^\S+.*?bits_(\d+).*?_#i_(\d+).*?_#o_(\d+)\S*\s+([\d.]+)\s*(B|Ki|Mi)")
    alloc_re      = re.compile(r"^\S+.*?bits_(\d+).*?_#i_(\d+).*?_#o_(\d+)\S*\s+([\d.]+)\s*([kM]?)")

    # geomean / simple fallback (matches any benchmark output line)
    time_mean_re  = re.compile(r"^geomean\s+([\d.]+)\s*([a-zµ]+)")
    ram_mean_re   = re.compile(r"^geomean\s+([\d.]+)\s*(B|Ki|Mi)")
    alloc_mean_re = re.compile(r"^geomean\s+([\d.]+)\s*([kM]?)")

    # Simple format: BenchmarkName-N  <count>  <val> ns/op  <ram> B/op  <allocs> allocs/op
    simple_time_re  = re.compile(r"^" + re.escape(bench_name) + r"[\-/\s]\S*\s+\d+\s+([\d.]+)\s+([a-zµ]+)/op")
    simple_ram_re   = re.compile(r"^" + re.escape(bench_name) + r"[\-/\s]\S*\s+\d+\s+[\d.]+\s+[a-zµ]+/op\s+([\d.]+)\s*(B|Ki|Mi)/op")
    simple_alloc_re = re.compile(r"^" + re.escape(bench_name) + r"[\-/\s]\S*\s+\d+\s+[\d.]+\s+[a-zµ]+/op\s+[\d.]+\s+(?:B|Ki|Mi)/op\s+([\d.]+)\s*([kM]?)\s+allocs/op")

    section = None
    time_ms_mean = ram_bytes_mean = allocs_mean = None
    simple_times = []
    simple_rams  = []
    simple_allocs = []

    for line in output:
        line = line.strip()
        if not line:
            continue

        # benchstat section headers
        if "sec/op"     in line: section = "time";   continue
        elif "B/op"     in line: section = "ram";    continue
        elif "allocs/op" in line: section = "allocs"; continue

        if section == "time":
            m = time_mean_re.match(line)
            if m:
                time_ms_mean = to_ms(float(m.group(1)), m.group(2))
                continue
            m = time_re.match(line)
            if m:
                bits, Inum, Onum, val, unit = m.groups()
                d[bench_name]["time"][bits][Inum][Onum] = to_ms(float(val), unit)

        elif section == "ram":
            m = ram_mean_re.match(line)
            if m:
                ram_bytes_mean = int(float(m.group(1)) * ram_mult[m.group(2)])
                continue
            m = ram_re.match(line)
            if m:
                bits, Inum, Onum, val, unit = m.groups()
                d[bench_name]["ram"][bits][Inum][Onum] = int(float(val) * ram_mult[unit])

        elif section == "allocs":
            m = alloc_mean_re.match(line)
            if m:
                allocs_mean = int(float(m.group(1)) * alloc_mult[m.group(2)])
                continue
            m = alloc_re.match(line)
            if m:
                bits, Inum, Onum, val, unit = m.groups()
                d[bench_name]["allocs"][bits][Inum][Onum] = int(float(val) * alloc_mult[unit])

        # Raw go test output fallback (before benchstat processes it)
        m = simple_time_re.match(line)
        if m:
            simple_times.append(to_ms(float(m.group(1)), m.group(2)))
        m = simple_ram_re.match(line)
        if m:
            simple_rams.append(int(float(m.group(1)) * ram_mult[m.group(2)]))
        m = simple_alloc_re.match(line)
        if m:
            simple_allocs.append(int(float(m.group(1)) * alloc_mult[m.group(2)]))

    # Fill in missing means from simple format if benchstat section not found
    if time_ms_mean is None and simple_times:
        time_ms_mean = int(sum(simple_times) / len(simple_times))
    if ram_bytes_mean is None and simple_rams:
        ram_bytes_mean = int(sum(simple_rams) / len(simple_rams))
    if allocs_mean is None and simple_allocs:
        allocs_mean = int(sum(simple_allocs) / len(simple_allocs))

    if None in (time_ms_mean, ram_bytes_mean, allocs_mean):
        # Print raw output to help diagnose future failures
        print(f"WARNING: could not parse all metrics for {bench_name}. Raw output snippet:")
        for l in output[-30:]:
            print(f"  {l}")
        # Use 0 as sentinel rather than crashing the whole run
        time_ms_mean   = time_ms_mean   or 0
        ram_bytes_mean = ram_bytes_mean or 0
        allocs_mean    = allocs_mean    or 0

    print_IOstats(d, results_IO)
    print(f"{bench_name} → time: {time_ms_mean}ms  RAM: {ram_bytes_mean}B  allocs: {allocs_mean}")
    return {
        f"{bench_name} time":   time_ms_mean,
        f"{bench_name} RAM":    ram_bytes_mean,
        f"{bench_name} allocs": allocs_mean,
    }


# Parallel benchmark runner; executor is a first-class dimension here

def run_and_parse_parallel_metrics(bench_name, params, cpu=1,
                                    curve="BLS12_381_BBS_GURVY",
                                    folder=transfer_benchmarks_folder,
                                    executor="serial",
                                    proof_type="bf") -> dict:
    if folder == "":
        folder = transfer_benchmarks_folder

    # Column key encodes executor so all three strategies can coexist in one CSV row
    col_key = f"{bench_name}[{executor}]/{cpu}"

    cmd = (f"go test {folder} "
           f"-test.run={bench_name} -test.v -test.timeout {timeout} "
           f"-bits='32' -num_inputs='2' -num_outputs='2' "
           f"-cpu={cpu} -workers={cpu} -curves={curve} "
           f"-duration='{duration}' -setup_samples=128 "
           f"-executor={executor} -proof_type={proof_type} "
           f"{params}")
    n = _next_run()
    print(f"{n} Running [{executor}]: {cmd}")

    try:
        result = subprocess.run(cmd, shell=True, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as e:
        print(f"Error executing parallel benchmark {bench_name}: {e}")
        if e.stdout:
            print(f"Stdout:\n{e.stdout}")
        if e.stderr:
            print(f"Stderr:\n{e.stderr}")
        raise e

    log_path = os.path.join(output_folder_path, f"{bench_name}-{executor}-{cpu}.log")
    if not os.path.exists(log_path):
        with open(log_path, "w") as f:
            f.write(result.stdout)

    output = result.stdout

    tps_re         = re.compile(r"Real Throughput\s+([\d.]+)/s")
    lat_p95_re     = re.compile(r"P95\s+([\d.]+)(ns|µs|us|ms|s)")
    lat_avg_re     = re.compile(r"Average\s+([\d.]+)(ns|µs|us|ms|s)")
    lat_std_re     = re.compile(r"Std Dev\s+([\d.]+)(ns|µs|us|ms|s)")
    goroutines_re  = re.compile(r"Goroutines Created\s+(\d+)")

    tps_m     = tps_re.search(output)
    p95_m     = lat_p95_re.search(output)
    avg_m     = lat_avg_re.search(output)
    std_m     = lat_std_re.search(output)
    gorout_m  = goroutines_re.search(output)

    if not all([tps_m, p95_m, avg_m, std_m]):
        raise ValueError(f"Failed to parse parallel output for {bench_name} executor={executor} cpu={cpu}")

    tps     = float(tps_m.group(1))
    lat_p95 = to_ms(float(p95_m.group(1)), p95_m.group(2))
    lat_avg = to_ms(float(avg_m.group(1)), avg_m.group(2))
    lat_std = to_ms(float(std_m.group(1)), std_m.group(2))
    goroutines = int(gorout_m.group(1)) if gorout_m else 0

    print(f"  → tps: {tps:.1f}  p95: {lat_p95}ms  avg: {lat_avg}ms  std: {lat_std}ms  goroutines: {goroutines}")
    return {
        f"{col_key} tps":        tps,
        f"{col_key} lat-p95":    lat_p95,
        f"{col_key} lat-avg":    lat_avg,
        f"{col_key} lat-std":    lat_std,
        f"{col_key} goroutines": goroutines,
    }


# ----- Benchmark lists -----

non_parallel_tests = [
    ("BenchmarkValidatorTransfer",       "-num_inputs 1,2 -num_outputs 1,2,3 -bits 32,64", validator_benchmarks_folder),
    ("BenchmarkSender",                  "-num_inputs 1,2,3 -num_outputs 1,2,3",            ""),
    ("BenchmarkVerificationSenderProof", "-num_inputs 1,2,3 -num_outputs 1,2,3 -bits 32,64",""),
    ("BenchmarkTransferProofGeneration", "",                                                 ""),
    ("BenchmarkIssuer",                  "",                                                 issuer_benchmarks_folder),
    ("BenchmarkProofVerificationIssuer", "",                                                 issuer_benchmarks_folder),
    ("BenchmarkTransferServiceTransfer", "",                                                 v1_benchmarks_folder),
    ("BenchmarkIssueServiceIssue",       "",                                                 v1_benchmarks_folder),
    ("BenchmarkAuditorServiceCheck",     "",                                                 v1_benchmarks_folder),
]
parallel_tests = [
    ("TestParallelBenchmarkValidatorTransfer",      "", validator_benchmarks_folder),
    ("TestParallelBenchmarkSender",                 "", ""),
    ("TestParallelBenchmarkVerificationSenderProof","", ""),
    ("TestParallelBenchmarkTransferProofGeneration","", ""),
    ("TestParallelBenchmarkTransferServiceTransfer","", v1_benchmarks_folder),
    ("TestParallelBenchmarkIssueServiceIssue",      "", v1_benchmarks_folder),
    ("TestParallelBenchmarkAuditorServiceCheck",    "", v1_benchmarks_folder),
]


# ----- Run everything -----

results = {}

print("\n*******************************************************")
print("Running non-parallel tests")
for test_name, params, folder in non_parallel_tests:
    if benchName and benchName != test_name:
        continue
    for curve in curves:
        for pt in proof_types:
            results.update(run_and_parse_non_parallel_metrics(
                test_name, params, curve, folder, pt))

print("\n*******************************************************")
print("Running parallel tests (executor × cpu × proof_type)")
for test_name, params, folder in parallel_tests:
    if benchName and benchName != test_name:
        continue
    for curve in curves:
        for pt in proof_types:
            for executor in executors:
                for cpu in cpus:
                    results.update(run_and_parse_parallel_metrics(
                        test_name, params, cpu, curve, folder, executor, pt))


# ----- Persist results -----

if not benchName:
    def append_dict_as_row(filename, data):
        file_exists = os.path.exists(filename)
        ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        row = {"timestamp": ts, **data}
        with open(filename, "a", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=row.keys())
            if not file_exists:
                writer.writeheader()
            writer.writerow(row)

    append_dict_as_row(results_csv, results)

    # Generate plots
    try:
        cmd = f"{sys.executable} plot_benchmark_results.py"
        print(f"\nRunning: {cmd}")
        subprocess.run(cmd, shell=True, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as e:
        print(f"Plot generation failed: {e}")
        if e.stdout:
            print(f"Stdout:\n{e.stdout}")
        if e.stderr:
            print(f"Stderr:\n{e.stderr}")

    # Copy outputs into the log folder
    for src_file in [results_csv, results_pdf, results_IO]:
        src = os.path.join(".", src_file)
        dst = os.path.join(output_folder_path, src_file)
        if os.path.exists(src) and not os.path.exists(dst):
            shutil.copy(src, dst)
            os.remove(src)

    if os.path.exists("bench.txt"):
        os.remove("bench.txt")

print(f"\nDone. Logs in: {output_folder_path}")