# ZKAT DLog No Graph Hiding Benchmarks

> **Related Documentation:**
> - [Testing Architecture](./dlognogh_architecture.md) - Understanding the test layers
> - [Regression Tests](./dlognogh_regression.md) - Backwards compatibility testing
> - [Driver Specification](../../../dlogwogh.md) - Complete driver documentation including CSP range proofs

## Range Proof Systems

**As of commit 586d4f58**, the driver supports two range proof systems:

1. **Bulletproofs** (Original) - IPA-based range proofs
2. **Compressed Sigma Protocols (CSP)** (New) - Recursive folding with optimized verification

The proof system is selected via the `-proof_type` flag:
- `bulletproof` or `1` - Uses Bulletproof range proofs (default)
- `csp` or `2` - Uses Compressed Sigma Protocol range proofs

**Performance Impact**: CSP proofs offer improved verification performance through optimized Lagrange interpolation, particularly beneficial for high-throughput scenarios. Benchmark results will vary based on the selected proof system.

## Executor Strategies

The driver supports three execution strategies for independent range proof generation and verification, controlled via the `-executor` flag:

- `serial` (default) — Tasks run inline on the calling goroutine. Zero scheduling overhead. Best for latency-sensitive single-proof workloads.
- `unbounded` — One goroutine per range proof. Maximum parallelism but unbounded goroutine creation. Best for small numbers of coarse-grained independent proofs.
- `pool` — Bounded pool of `runtime.NumCPU()` goroutines. Balances throughput and stability. Best for high-concurrency scenarios with multiple output tokens.

The executor strategy adds a new dimension to benchmarking: a given workload can be measured with all three strategies to find the optimal configuration for a specific deployment's latency/throughput tradeoff.

## Benchmark Packages

Packages with benchmark tests:

- `token/core/zkatdlog/nogh/v1/transfer`: 
   - `BenchmarkSender`, `BenchmarkVerificationSenderProof`, `TestParallelBenchmarkSender`, and `TestParallelBenchmarkVerificationSenderProof` are used to benchmark the generation of a transfer action, serialization included. This includes also the generation of ZK proof for a transfer operation.
   - `BenchmarkTransferProofGeneration`, `TestParallelBenchmarkTransferProofGeneration` are used to benchmark the generation of ZK proof alone. This includes proof serialization. 
- `token/core/zkatdlog/nogh/v1/issue`: `BenchmarkIssuer` and `BenchmarkProofVerificationIssuer`
- `token/core/zkatdlog/nogh/v1/validator`: `TestParallelBenchmarkValidatorTransfer`.
- `token/core/zkatdlog/nogh/v1`: `BenchmarkTransferServiceTransfer` and `TestParallelBenchmarkTransferServiceTransfer`.
- `token/core/zkatdlog/nogh/v1`: `BenchmarkIssueServiceIssue` and `TestParallelBenchmarkIssueServiceIssue` benchmark the full `IssueService.Issue()` path (wallet lookup, signer resolution, ZK proof generation, audit-info encoding, and serialization).
- `token/core/zkatdlog/nogh/v1`: `BenchmarkAuditorServiceCheck` and `TestParallelBenchmarkAuditorServiceCheck` benchmark `AuditorService.AuditorCheck()` (action deserialization and Pedersen commitment arithmetic).

The steps necessary to run the benchmarks are very similar.
We give two examples here:
- `token/core/zkatdlog/nogh/v1/transfer#BenchmarkSender`, and
- `token/core/zkatdlog/nogh/v1/transfer#TestParallelBenchmarkSender`

## Benchmark: `token/core/zkatdlog/nogh/v1/transfer#BenchmarkSender`

In this Section, we go through the steps necessary to run the benchmark and interpret the results.
For the other benchmarks the process is the same.

### Overview

`BenchmarkSender` measures the cost of generating a zero-knowledge transfer (ZK transfer) using the DLog no-graph-hiding sender implementation and serializing the resulting transfer object. 
Concretely, each benchmark iteration constructs the required sender environment, invokes `GenerateZKTransfer(...)`, and calls `Serialize()` on the returned transfer - so the measured time includes ZK transfer construction and serialization.

The benchmark is implemented to run the same workload across a matrix of parameters (bit sizes, curve choices, number of inputs and outputs). 
A helper inside the test (`generateBenchmarkCases`) programmatically generates all combinations of the selected parameters.

### Parameters

The benchmark accepts the following tunable parameters:

- Bits: integer bit sizes used for some setup (e.g., 32, 64). This is passed to the test setup code.
- CurveID: the `math.CurveID` used (examples: `BN254`, `BLS12_381_BBS_GURVY`).
- NumInputs: number of input tokens provided to the sender (1, 2, ...).
- NumOutputs: number of outputs produced by the transfer (1, 2, ...).
- ProofType: the range proof system to use (`bulletproof` or `csp`).
- Executor: the execution strategy for independent range proofs (`serial`, `unbounded`, or `pool`).

These parameters can be configured from the command line using the following flags:

```shell
  -bits string
        a comma-separated list of bit sizes (32, 64,...)
  -curves string
        comma-separated list of curves. Supported curves are: FP256BN_AMCL, BN254, FP256BN_AMCL_MIRACL, BLS12_381_BBS, BLS12_381_BBS_GURVY, BLS12_381_BBS_GURVY_FAST_RNG
  -num_inputs string
        a comma-separate list of number of inputs (1,2,3,...)
  -num_outputs string
        a comma-separate list of number of outputs (1,2,3,...)
  -proof_type string
        range proof system: bulletproof (default) or csp
  -executor string
        execution strategy for range proofs: serial (default), unbounded, or pool
```

### Default parameter set used in the benchmark

If no flag is used, the test file currently uses the following parameter slices (so the resulting combinations are the Cartesian product of these lists):

- bits: [32, 64]
- curves: [BN254, BLS12_381_BBS_GURVY, BLS12_381_BBS_GURVY_FAST_RNG]
- inputs: [1, 2, 3]
- outputs: [1, 2, 3]
- executor: serial

This produces 2 (bits) * 3 (curves) * 3 (inputs) * 3 (outputs) = 54 sub-benchmarks. 
Each sub-benchmark runs the standard `b.N` iterations and reports time and allocation statistics.

### How to run

Run the benchmark for the package containing the sender benchmarks:

```sh
# run the BenchmarkSender benchmarks in the transfer package
go test ./token/core/zkatdlog/nogh/v1/transfer -bench=BenchmarkSender -benchmem -count=1 -cpu=1 -timeout 0 -run=^$
```
> Notice that:
> - `-run=^$` has the effect to avoid running any other unit-test present in the package.
> - `-timeout 0` disables the test timeout.

If you want to run the benchmark repeatedly and save results to a file:

```sh
go test ./token/core/zkatdlog/nogh/v1/transfer -bench=BenchmarkSender -benchmem -count=10 -cpu=1 -timeout 0 -run=^$ | tee bench.txt
```

Note: `-count` controls how many times the test binary is executed (useful to reduce variance); `-benchmem` reports allocation statistics.

You can also change the parameters:

```shell
go test ./token/core/zkatdlog/nogh/v1/transfer -test.bench=BenchmarkSender -test.benchmem -test.count=10 -test.cpu=1 -test.timeout 0 -test.run=^$ -bits="32" -curves="BN254" -num_inputs="2" -num_outputs="2" -proof_type="csp" -executor="pool" | tee bench.txt
```

> Notice that in this the above case, the `go test` options must be prefixed with `test.` otherwise the tool will fail.


### Notes and best practices

- Be mindful of the Cartesian explosion: combining many bit sizes, curves, input counts and output counts can produce many sub-benchmarks.  
  For CI or quick local runs, reduce the parameter lists to a small subset (for example: one bit size, one curve, and 1-2 input/output sizes).
- The benchmark creates `b.N` independent sender environments (via `NewBenchmarkSenderEnv`) and runs `GenerateZKTransfer` for each environment in the inner loop — so memory and setup cost scale with `b.N` during setup.
- If you need to measure only the transfer-generation time and omit setup, consider modifying the benchmark to move expensive one-time setup out of the measured region and call `b.ResetTimer()` appropriately (the current benchmark already calls `b.ResetTimer()` before the inner loop).
- When comparing executor strategies, run each strategy in isolation with the same duration and worker count to get a fair comparison. The `pool` executor generally reduces tail latency at the cost of slightly higher GC overhead compared to `serial`.

### Collecting and interpreting results

A typical run prints timings per sub-benchmark (ns/op) and allocation statistics. Example command to persist results:

```sh
go test ./token/core/zkatdlog/nogh/v1/transfer -bench=BenchmarkSender -benchmem -count=10 -cpu=1 -timeout 0 -run=^$ | tee bench.txt
```

You can then aggregate/parse the output (e.g., benchstat) to compute averages across `-count` repetitions.

### Results

Example results have been produced on an Apple M1 Max and can be consulted [here](transfer_BenchmarkSender_results.md). 

## Benchmark: `token/core/zkatdlog/nogh/v1/transfer#TestParallelBenchmarkSender`

This is a test that runs multiple instances of the above benchmark in parallel.
This allows the analyst to understand if shared data structures are actual bottlenecks.

It uses a custom-made runner whose documentation can be found [here](../../../../../token/services/benchmark/runner.md).

```shell
go test ./token/core/zkatdlog/nogh/v1/transfer -test.run=TestParallelBenchmarkSender -test.v -test.timeout 0 -bits="32" -curves="BN254" -num_inputs="2" -num_outputs="2" -workers="NumCPU" -duration="10s" -setup_samples=128 -executor="pool" | tee bench.txt
```

The test supports the following flags:
```shell
  -bits string
        a comma-separated list of bit sizes (32, 64,...)
  -curves string
        comma-separated list of curves. Supported curves are: FP256BN_AMCL, BN254, FP256BN_AMCL_MIRACL, BLS12_381_BBS, BLS12_381_BBS_GURVY, BLS12_381_BBS_GURVY_FAST_RNG
  -duration duration
        test duration (1s, 1m, 1h,...) (default 1s)
  -num_inputs string
        a comma-separate list of number of inputs (1,2,3,...)
  -num_outputs string
        a comma-separate list of number of outputs (1,2,3,...)
  -workers string
        a comma-separate list of workers (1,2,3,...,NumCPU), where NumCPU is converted to the number of available CPUs
  -proof_type string
        range proof system: bulletproof (default) or csp
  -executor string
        execution strategy for range proofs: serial (default), unbounded, or pool
  -profile bool
        write pprof profiles to file
  -setup_samples uint
        number of setup samples, 0 disables it
```

### Results

```shell
=== RUN   TestParallelBenchmarkSender
=== RUN   TestParallelBenchmarkSender/Setup(bits_32,_curve_BN254,_#i_2,_#o_2)_with_10_workers
Metric           Value     Description
------           -----     -----------
Workers          10        
Total Ops        2415      (Low Sample Size)
Duration         10.029s   (Good Duration)
Real Throughput  240.80/s  Observed Ops/sec (Wall Clock)
Pure Throughput  241.47/s  Theoretical Max (Low Overhead)

Latency Distribution:
 Min           31.760236ms  
 P50 (Median)  41.257005ms  
 Average       41.41284ms   
 P95           45.007531ms  
 P99           46.786355ms  
 P99.9         48.476513ms  
 Max           49.139729ms  (Stable Tail)

Stability Metrics:
 Std Dev  2.026146ms  
 IQR      2.616459ms  Interquartile Range
 Jitter   1.960907ms  Avg delta per worker
 CV       4.89%       Excellent Stability (<5%)

System Health & Reliability:
 Error Rate   0.0000%          (100% Success) (0 errors)
 Memory       1787240 B/op     Allocated bytes per operation
 Allocs       19166 allocs/op  Allocations per operation
 Alloc Rate   397.47 MB/s      Memory pressure on system
 GC Overhead  7.00%            (Severe GC Thrashing)
 GC Pause     702.07326ms      Total Stop-The-World time
 GC Cycles    2160             Full garbage collection cycles

Latency Heatmap (Dynamic Range):
Range                     Freq  Distribution Graph
 31.760236ms-32.460946ms  2      (0.1%)
 33.177115ms-33.909085ms  1      (0.0%)
 35.421828ms-36.203322ms  2      (0.1%)
 36.203322ms-37.002058ms  12    █ (0.5%)
 37.002058ms-37.818416ms  54    ████ (2.2%)
 37.818416ms-38.652784ms  103   ████████ (4.3%)
 38.652784ms-39.505561ms  230   ████████████████████ (9.5%)
 39.505561ms-40.377152ms  349   ██████████████████████████████ (14.5%)
 40.377152ms-41.267973ms  460   ████████████████████████████████████████ (19.0%)
 41.267973ms-42.178447ms  421   ████████████████████████████████████ (17.4%)
 42.178447ms-43.109009ms  311   ███████████████████████████ (12.9%)
 43.109009ms-44.060101ms  232   ████████████████████ (9.6%)
 44.060101ms-45.032177ms  119   ██████████ (4.9%)
 45.032177ms-46.025699ms  81    ███████ (3.4%)
 46.025699ms-47.041141ms  19    █ (0.8%)
 47.041141ms-48.078986ms  15    █ (0.6%)
 48.078986ms-49.139729ms  4      (0.2%)

--- Analysis & Recommendations ---
[WARN] Low sample size (2415). Results may not be statistically significant. Run for longer.
[INFO] High Allocations (19166/op). This will trigger frequent GC cycles and increase Max Latency.
----------------------------------

--- Throughput Timeline ---
Timeline: [▇█▇▇▇▇▇▇▇] (Max: 247 ops/s)

--- PASS: TestParallelBenchmarkSender (13.29s)
    --- PASS: TestParallelBenchmarkSender/Setup(bits_32,_curve_BN254,_#i_2,_#o_2)_with_10_workers (13.28s)
PASS
ok      github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer       13.320s
```

### Running selected benchmarks with run_benchmarks.py
The run_benchmarks.py script runs selected benchmarks and summarizes their results in a csv file, so it could be followed as more optimizations are added. 
To run this script goto the ../panurus/cmd/benchmarking folder and run
```shell
python run_benchmarks.py
```

This creates a subfolder that collects the logs of all the benchmarks and the csv file (**benchmark_results.csv**) that has a separate row for every invocation of the script with the selected metrics collected for all the benchmarks. 
The folder is named **benchmark_logs_<date>** for example **benchmark_logs_2026-01-19_06-56-41**, where the date indicates the time when the script was run.

The benchmarks are ran only for the BLS12_381_BBS_GURVY curve and repeated for 1, 2, 4, 8, 16, and 32 cpus.
The script supports the following flags:

`--count`
: the number of times to run the benchmark

`--timeout`
: the maximum time (in seconds) to for the benchmark. Thje default is 0, implying no limit.

`--benchName`
: The single benchmark that should be run by the script. The default is to run the whole selection of benchmarks.

**Plots summarizing the benchmark results: benchmark_results.pdf**

As mentioned above, the run_benchmark.py script produces the results in the file benchmark_results.csv.
A separate script **plot_lat_vs_tps.py** is used to produce a corresponding file **benchmark_results.pdf** that includes figures that summarize the benchmark results. this pdf file is also saved in the benchmark_logs folder along with the other results. 
For every benchmark there will be a plot of the throughput vs the number of cpus, and another plot of the latency results vs. the throughput for the different number of cpus tested. 

**Summary of the effects of bit counts and number of I/O tokens: benchmark_IOstats.csv**

The run_benchmark.py script also runs the benchmarks under different setting of the number of bits (32/64) and the number of input/output tokens, and produces a table summarizing the time, RAM and number of allocations for each combination. This summary is saved in the benchmark_logs folder under **benchmark_results.csv**.

**Example runs:**

- Running all the selected benchmarks:
```shell
python run_benchmarks.py --benchName BenchmarkSender --timeout 4s --count 5
```
- Running just one selected benchmark 5 times for no more than 4 seconds:
```shell
python run_benchmarks.py --benchName BenchmarkSender --timeout 4s --count 5
```

