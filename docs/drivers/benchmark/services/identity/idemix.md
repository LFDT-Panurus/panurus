# Identity Service - Idemix Benchmarks

Packages with benchmark tests:

- `token/services/identity/idemix`:
    - `TestParallelBenchmarkIdemixKMIdentity`: Generation of a pseudonym.
    - `TestParallelBenchmarkIdemixSign`: Generation of a signature given a pseudonym.
    - `TestParallelBenchmarkIdemixVerify`: Verification of a signature.
    - `TestParallelBenchmarkIdemixDeserializeSigner`: Deserialization of a Signer given a pseudonym.

Here is an execution example:

```shell
➜  panurus git:(1284-dlog-validator-service-benchmark) ✗ go test ./token/services/identity/idemix -test.run=TestParallelBenchmarkIdemixDeserializeSigner -test.v -test.timeout 0 -workers="NumCPU" -duration="10s" -setup_samples=128
=== RUN   TestParallelBenchmarkIdemixDeserializeSigner
Metric           Value      Description
------           -----      -----------
Workers          10         
Total Ops        18494      (Robust Sample)
Duration         10.026s    (Good Duration)
Real Throughput  1844.65/s  Observed Ops/sec (Wall Clock)
Pure Throughput  1845.74/s  Theoretical Max (Low Overhead)

Latency Distribution:
 Min           4.326583ms   
 P50 (Median)  4.409667ms   
 Average       5.417878ms   
 P95           11.517116ms  
 P99           16.813871ms  
 P99.9         26.423944ms  
 Max           98.053292ms  (Stable Tail)

Stability Metrics:
 Std Dev  2.798676ms  
 IQR      259.906µs   Interquartile Range
 Jitter   1.502269ms  Avg delta per worker
 CV       51.66%      Unstable (>20%) - Result is Noisy

System Health & Reliability:
 Error Rate   0.0000%        (100% Success) (0 errors)
 Memory       60665 B/op     Allocated bytes per operation
 Allocs       694 allocs/op  Allocations per operation
 Alloc Rate   103.69 MB/s    Memory pressure on system
 GC Overhead  0.40%          (Healthy)
 GC Pause     39.798795ms    Total Stop-The-World time
 GC Cycles    92             Full garbage collection cycles

Latency Heatmap (Dynamic Range):
Range                     Freq   Distribution Graph
 4.326583ms-5.057208ms    14749  ████████████████████████████████████████ (79.8%)
 5.057208ms-5.911214ms    889    ██ (4.8%)
 5.911214ms-6.909436ms    535    █ (2.9%)
 6.909436ms-8.076226ms    444    █ (2.4%)
 8.076226ms-9.44005ms     434    █ (2.3%)
 9.44005ms-11.034182ms    435    █ (2.4%)
 11.034182ms-12.897514ms  302     (1.6%)
 12.897514ms-15.075505ms  373    █ (2.0%)
 15.075505ms-17.621292ms  196     (1.1%)
 17.621292ms-20.596982ms  78      (0.4%)
 20.596982ms-24.075175ms  32      (0.2%)
 24.075175ms-28.140727ms  15      (0.1%)
 28.140727ms-32.892825ms  5       (0.0%)
 32.892825ms-38.447405ms  2       (0.0%)
 38.447405ms-44.939982ms  1       (0.0%)
 44.939982ms-52.528953ms  2       (0.0%)
 52.528953ms-61.399467ms  1       (0.0%)
 83.88732ms-98.053292ms   1       (0.0%)

--- Analysis & Recommendations ---
[FAIL] High Variance (CV 51.66%). System noise is affecting results. Isolate the machine or increase duration.
[INFO] High Allocations (694/op). This will trigger frequent GC cycles and increase Max Latency.
----------------------------------

--- Throughput Timeline ---
Timeline: [▇▇▇▇▇▇▇█▇▇] (Max: 1906 ops/s)

--- PASS: TestParallelBenchmarkIdemixDeserializeSigner (13.82s)
PASS
ok      github.com/LFDT-Panurus/panurus/token/services/identity/idemix     14.365s

```