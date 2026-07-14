# Tracer Package

Runtime call tracing library for hierarchical performance profiling.

## Usage

### Basic Example

```go
import "github.com/LFDT-Panurus/panurus/tools/profiler/tracer"

func main() {
    tracer.Enable()
    defer tracer.Disable()
    
    myFunction()
    
    tracer.Print()
}

func myFunction() {
    defer tracer.Enter("myFunction")()
    // ... function body ...
}
```

### In Benchmarks

```go
func BenchmarkMyCode(b *testing.B) {
    tracer.Enable()
    defer tracer.Disable()
    
    for i := 0; i < b.N; i++ {
        MyCode()
    }
    
    tracer.PrintWithOptions(tracer.PrintOptions{
        RootFunction:   "MyCode",
        ShowPercent:    true,
        ShowAbsolute:   true,
        AggregateLoops: true,
    })
}
```

## API

### Control

```go
Enable()              // Start profiling
EnableDiscovery()     // Start discovery mode
Disable()             // Stop and clear
IsEnabled() bool      // Check if active
```

### Tracing

```go
// Record function entry/exit
Enter(funcName string) func()

// Usage in every instrumented function:
defer tracer.Enter("FunctionName")()
```

### Output

```go
// Simple output
Print()

// Customized output
PrintWithOptions(opts PrintOptions)

// Top N functions
PrintSummary(topN int)
PrintSummaryWithRoot(topN int, rootFunc string)
```

### Options

```go
type PrintOptions struct {
    RootFunction   string  // Start from this function
    ShowPercent    bool    // Show % of root time
    ShowAbsolute   bool    // Show duration
    AggregateLoops bool    // Combine repeated calls (x2, x5)
    MinPercentage  float64 // Hide functions below this %
}
```

## Output Format

### Call Hierarchy

```
=== Call Hierarchy ===
└── RootFunction [1.5s, 100.00%]
    ├── ChildA [0.9s, 60.00%]
    │   └── GrandchildA x3 [0.6s, 40.00%]
    └── ChildB [0.6s, 40.00%]
```

- Tree shows parent-child relationships
- Times are cumulative (include all children)
- `x3` = function called 3 times (total time shown)
- Percentages relative to root

### Performance Summary

```
=== Performance Summary (Top Functions) ===
Function                                                         Total Time  % of Root
---------------------------------------------------------------------------------------
RootFunction                                                     1.5s        100.00%
ChildA                                                           0.9s         60.00%
GrandchildA                                                      0.6s         40.00%
```

## Discovery Mode

Find which packages are called:

```go
tracer.EnableDiscovery()
defer tracer.Disable()

myFunction()

packages := tracer.GetDiscoveredPackages()
for _, pkg := range packages {
    fmt.Println(pkg)
}
```

## Best Practices

1. **Always use defer:** `defer tracer.Enter("FuncName")()`
2. **Enable/Disable in pairs:** Use `defer tracer.Disable()`
3. **Include receiver type:** `(*Type).Method` for methods
4. **Focus analysis:** Use `RootFunction` option
5. **Filter noise:** Use `MinPercentage` option

## Thread Safety

All functions are thread-safe. Concurrent calls will interleave in the tree but times remain accurate.

## Examples

See [parent directory's README.md](../README.md#manual-instrumentation) for complete examples of manual instrumentation in benchmarks and tests.

For automated profiling without manual instrumentation, use the `profile.sh` script in the parent directory.
