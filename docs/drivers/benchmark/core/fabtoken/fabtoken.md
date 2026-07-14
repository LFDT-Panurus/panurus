# Fabtoken Benchmarks

This document provides an overview of the performance benchmarks for the Fabtoken driver in Panurus.

> **Related Documentation:**
> - [Testing Architecture](./fabtoken_architecture.md) - Understanding the test layers

## Overview

The Fabtoken benchmarks measure the performance of token operations (Issue, Transfer, and Validation) at different abstraction layers. Unlike the ZKAT DLog driver, Fabtoken does not use complex zero-knowledge proofs, making its operations significantly faster and simpler.

## Benchmark Packages

The following packages contain benchmark tests for Fabtoken:

- `token/core/fabtoken/v1`:
    - **Layer 3 (Service)**: `BenchmarkTransferServiceTransfer`, `BenchmarkIssueServiceIssue`, `BenchmarkAuditorServiceCheck`
    - **Layer 2 (Action Generation)**: `BenchmarkSender`, `BenchmarkIssuer`
    - **Layer 2 (Action Verification)**: `BenchmarkVerificationSenderProof`, `BenchmarkProofVerificationIssuer`
    - **Layer 1 (Core)**: `BenchmarkTransferProofGeneration`
- `token/core/fabtoken/v1/validator`:
    - **Layer 3 (Service)**: `BenchmarkValidatorTransfer`, `BenchmarkValidatorIssue`

## Parameters

Fabtoken benchmarks support the same programmatic parameter system as other drivers:

- **NumInputs**: Number of input tokens (for Transfer).
- **NumOutputs**: Number of output tokens (for Issue and Transfer).

These are controlled via the `benchmark` package and can be customized in the test code or via flags if implemented in the future (currently using defaults from `GenerateCasesWithDefaults`).

## How to Run

### Run Service Layer Benchmarks
```bash
# Transfer Service
go test ./token/core/fabtoken/v1 -bench=BenchmarkTransferServiceTransfer -benchmem -run=^$

# Issue Service
go test ./token/core/fabtoken/v1 -bench=BenchmarkIssueServiceIssue -benchmem -run=^$

# Auditor Service
go test ./token/core/fabtoken/v1 -bench=BenchmarkAuditorServiceCheck -benchmem -run=^$
```

### Run Action Layer Benchmarks
```bash
# Transfer Action Generation
go test ./token/core/fabtoken/v1 -bench=BenchmarkSender -benchmem -run=^$

# Issue Action Generation
go test ./token/core/fabtoken/v1 -bench=BenchmarkIssuer -benchmem -run=^$
```

### Run Validator Benchmarks
```bash
go test ./token/core/fabtoken/v1/validator -bench=BenchmarkValidatorTransfer -benchmem -run=^$
go test ./token/core/fabtoken/v1/validator -bench=BenchmarkValidatorIssue -benchmem -run=^$
```

## Parallel Benchmarking

Most benchmarks include parallel variants to measure performance under concurrent load:

- **Built-in Parallelism**: `BenchmarkParallelSender`, `BenchmarkVerificationParallelSenderProof`
- **Custom Parallel Framework**: `TestParallelBenchmarkTransferServiceTransfer`, `TestParallelBenchmarkIssueServiceIssue`, `TestParallelBenchmarkAuditorServiceCheck`, `TestParallelBenchmarkValidatorTransfer`, etc.

To run the custom parallel benchmarks (using `*testing.T`):
```bash
go test ./token/core/fabtoken/v1 -run=TestParallelBenchmarkTransferServiceTransfer -v
```

## Understanding Results

The benchmarks report:
- `ns/op`: Time taken per operation.
- `B/op`: Memory allocated per operation.
- `allocs/op`: Number of memory allocations per operation.

Lower values indicate better performance. Since Fabtoken uses standard cryptographic signatures (like ECDSA or RSA) instead of ZK proofs, you should expect much lower `ns/op` compared to the DLog driver.
