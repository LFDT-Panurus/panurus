# Test & Benchmark Profiler

Automatic hierarchical call tracing and performance profiling for Go tests and benchmarks in Panurus.

## Quick Start

```bash
cd tools/profiler
./profile.sh BenchmarkValidatorTransfer
```

## What It Does

1. Copies your repo to `/tmp/profiler-<PID>` (your files are never touched)
2. Finds your test/benchmark in `token/` directory
3. Instruments all Go packages in `token/`
4. Runs with profiling
5. Saves results to current directory
6. Cleans up automatically

**Safety:** All work happens in `/tmp`. Your repository is never modified.

## Example Output

```
## Call Hierarchy

└── >>> (*Validator).VerifyTransfer [2.5s, 100.00%]
    ├── >>> (*Validator).verifyInputs [1.8s, 72.00%]
    │   └── >>> (*Verifier).Verify x5 [1.5s, 60.00%]
    └── (*Validator).verifyOutputs [0.7s, 28.00%]

## Top 20 Functions by Time

*Note: These functions are marked with `>>>` in the call tree above*

| Function | Total Time | % of Root |
|----------|------------|-----------|
| (*Validator).VerifyTransfer | 2.5s | 100.00% |
| (*Validator).verifyInputs | 1.8s | 72.00% |
| (*Verifier).Verify | 1.5s | 60.00% |
```

**Reading the output:**
- `>>>` marks the top 20 functions by time
- Times are **cumulative** (include all child functions)
- `x5` means called 5 times (total time shown)
- Percentages relative to root function

## Options

```bash
./profile.sh <test_or_benchmark_name> [options]

Options:
  -d, --display <mode>      Display: both, time, percent (default: both)
  -f, --root-function <name> Start from this function (show subtree)
  -m, --min-percent <n>     Hide functions below this % (default: 0)
  -h, --help                Show help
```

**Examples:**
```bash
# Show only percentages
./profile.sh BenchmarkValidatorTransfer -d percent

# Profile a subtree
./profile.sh BenchmarkValidatorTransfer -f VerifyTransfer

# Hide functions using less than 1%
./profile.sh BenchmarkValidatorTransfer -m 1.0

# Profile a test
./profile.sh TestValidatorTransfer
```

## Scope

**Searches for tests/benchmarks in:**
- `token/` directory and all subdirectories

**Instruments:**
- All Go packages in `token/` directory

**Output:**
- Saved to current directory as `<TestName>_profile.md`

## Manual Instrumentation

The `profile.sh` script automatically instruments your code. However, you can also manually instrument code for profiling.

### In Benchmarks

```go
import "github.com/LFDT-Panurus/panurus/tools/profiler/tracer"

func BenchmarkValidatorTransfer(b *testing.B) {
    // Enable tracer
    tracer.Enable()
    defer tracer.Disable()
    
    // Setup code (not profiled)
    validator := setupValidator()
    request := generateRequest()
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        // This will be profiled if functions have tracer.Enter() calls
        err := validator.VerifyTransfer(request)
        if err != nil {
            b.Fatal(err)
        }
    }
    b.StopTimer()
    
    // Print profile
    tracer.PrintWithOptions(tracer.PrintOptions{
        RootFunction:   "VerifyTransfer",
        ShowPercent:    true,
        ShowAbsolute:   true,
        AggregateLoops: true,
    })
}

// Each function you want to profile needs this:
func (v *Validator) VerifyTransfer(req *Request) error {
    defer tracer.Enter("(*Validator).VerifyTransfer")()
    // ... function body ...
}
```

### In Tests

```go
func TestValidatorTransfer(t *testing.T) {
    // Enable tracer
    tracer.Enable()
    defer tracer.Disable()
    
    // Run test
    validator := setupValidator()
    request := generateRequest()
    
    err := validator.VerifyTransfer(request)
    require.NoError(t, err)
    
    // Print profile
    tracer.PrintWithOptions(tracer.PrintOptions{
        RootFunction:   "VerifyTransfer",
        ShowPercent:    true,
        ShowAbsolute:   true,
        AggregateLoops: true,
    })
}
```

**Note:** Manual instrumentation requires adding `defer tracer.Enter("FunctionName")()` to every function you want to profile. The `profile.sh` script does this automatically by:
1. Copying your code to a temporary directory
2. Running `auto-instrument.go` to add tracer calls
3. Running the test/benchmark
4. Cleaning up

For most use cases, use `profile.sh` instead of manual instrumentation.

## Files

- `profile.sh` - Automated profiling script
- `auto-instrument.go` - Adds tracer hooks to Go files
- `inject-tracer.go` - Injects tracer into test/benchmark
- `tracer/` - Runtime tracing library

## Troubleshooting

**Can't find test/benchmark:**
- Check spelling
- Ensure it's in `token/` directory

**Empty output file:**
- Check if test/benchmark runs successfully
- Look for compilation errors in script output

**Times don't add up:**
- Times are cumulative (include children)
- Use percentages to see relative contributions
