# Benchmarks

## Tools

- [Go Tools for Benchmarks](./tools.md)
- Custom Analysis Tools:
  - [`memcheck`](./../../../token/services/benchmark/cmd/memcheck/README.md): Go Pprof Memory Analyzer
  - [`traceinspector`](./../../../token/services/benchmark/cmd/memcheck/README.md): Go Pprof Trace Analyzer

## Benchmarks

### Core Token Drivers

- [ZKAT DLog No Graph-Hiding Benchmarks](core/dlognogh/dlognogh.md) - How to run benchmarks
- [ZKAT DLog Testing Architecture](core/dlognogh/dlognogh_architecture.md) - Understanding the test layers
- [ZKAT DLog Regression Tests](core/dlognogh/dlognogh_regression.md) - Backwards compatibility testing

- [Fabtoken Benchmarks](core/fabtoken/fabtoken.md) - How to run benchmarks
- [Fabtoken Testing Architecture](core/fabtoken/fabtoken_architecture.md) - Understanding the test layers

### Services

- [Identity Service - Idemix](services/identity/idemix.md)

### Node Level Benchmarks
- [Token Validation Service Benchmark](token_validation_service_benchmark.md)

### Foundational Types
- `Quantity` operations (`token/token/quantity_test.go`) — micro-benchmarks for `BigQuantity`/`UInt64Quantity` `Add`, `Sub`, `Cmp`, and `ToQuantity` parsing. Run with `go test -run='^$' -bench=. -benchmem ./token/token/`