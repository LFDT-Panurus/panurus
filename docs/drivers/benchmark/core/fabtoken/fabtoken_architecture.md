# Fabtoken Testing Architecture

This document explains the testing architecture for the Fabtoken implementation in Panurus.

> **Related Documentation:**
> - [Running Benchmarks](./fabtoken.md) - How to run performance benchmarks

## Overview

The Fabtoken tests are organized in a **layered architecture** that mirrors the **code abstraction levels**. Each layer represents a different level of the software stack - from core operations to high-level service APIs.

## Architecture Layers

Each layer represents a different **code abstraction level**.

```
┌─────────────────────────────────────────────────────────────────┐
│ Layer 3: Service Layer (Highest Abstraction)                    │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Generation Service                                     │
│ Location: token/core/fabtoken/v1/                               │
│ Tests: - BenchmarkTransferServiceTransfer                       │
│        - TestParallelBenchmarkTransferServiceTransfer           │
│ Purpose: End-to-end transfer operation generation with vault,   │
│          audit, and metadata handling                           │
├─────────────────────────────────────────────────────────────────┤
│ Issue Generation Service                                        │
│ Location: token/core/fabtoken/v1/                               │
│ Tests: - BenchmarkIssueServiceIssue                             │
│        - TestParallelBenchmarkIssueServiceIssue                  │
│ Purpose: End-to-end issue operation generation through high-level│
│          service API                                            │
├─────────────────────────────────────────────────────────────────┤
│ Auditor Check Service                                           │
│ Location: token/core/fabtoken/v1/                               │
│ Tests: - BenchmarkAuditorServiceCheck                           │
│        - TestParallelBenchmarkAuditorServiceCheck                │
│ Purpose: End-to-end audit verification through high-level       │
│          service API                                            │
├─────────────────────────────────────────────────────────────────┤
│ Transfer/Issue Validator Service                                │
│ Location: token/core/fabtoken/v1/validator/                      │
│ Tests: - BenchmarkValidatorTransfer                             │
│        - TestParallelBenchmarkValidatorTransfer                 │
│        - BenchmarkValidatorIssue                                │
│        - TestParallelBenchmarkValidatorIssue                    │
│ Purpose: Token request validation with signatures, business     │
│          logic, and format verification                         │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ Layer 2: Action Operations                                      │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Generation (token/core/fabtoken/v1/)                   │
│ Tests: - BenchmarkSender                                        │
│        - BenchmarkParallelSender                                │
│        - TestParallelBenchmarkSender                            │
│ Purpose: Transfer action generation and serialization           │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Verification (token/core/fabtoken/v1/)                 │
│ Tests: - BenchmarkVerificationSenderProof                       │
│        - BenchmarkVerificationParallelSenderProof               │
│        - TestParallelBenchmarkVerificationSenderProof           │
│ Purpose: Deserialization and format validation of transfer      │
│          actions                                                │
├─────────────────────────────────────────────────────────────────┤
│ Issue Generation (token/core/fabtoken/v1/)                      │
│ Tests: - BenchmarkIssuer                                        │
│ Purpose: Issue action generation and serialization              │
├─────────────────────────────────────────────────────────────────┤
│ Issue Verification (token/core/fabtoken/v1/)                    │
│ Tests: - BenchmarkProofVerificationIssuer                       │
│ Purpose: Deserialization and verification of issue actions      │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ Layer 1: Core Operations (Lowest Abstraction)                   │
│ Location: token/core/fabtoken/v1/                               │
│ Tests: - BenchmarkTransferProofGeneration                       │
│        - TestParallelBenchmarkTransferProofGeneration           │
│ Purpose: Pure transfer action structure generation and          │
│          serialization                                          │
└─────────────────────────────────────────────────────────────────┘
```

---

## Layer 1: Core Operations

**Location:** `token/core/fabtoken/v1/`

### Tests
- `BenchmarkTransferProofGeneration`
- `TestParallelBenchmarkTransferProofGeneration`

### Purpose
Pure transfer action generation and serialization. In Fabtoken, since there are no complex zero-knowledge proofs, this layer focuses on the core action structure creation.

### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkTransferProofGeneration -benchtime=10s
go test -run=TestParallelBenchmarkTransferProofGeneration -v
```

---

## Layer 2: Action Operations

### Transfer Action Generation

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkSender`
- `BenchmarkParallelSender`
- `TestParallelBenchmarkSender`

#### Purpose
Complete transfer action creation from inputs to serialized output.

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkSender -benchtime=10s
go test -bench=BenchmarkParallelSender -benchtime=10s
go test -run=TestParallelBenchmarkSender -v
```

### Transfer Action Verification

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkVerificationSenderProof`
- `BenchmarkVerificationParallelSenderProof`
- `TestParallelBenchmarkVerificationSenderProof`

#### Purpose
Deserialization and verification of transfer actions.

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkVerificationSenderProof -benchtime=10s
go test -bench=BenchmarkVerificationParallelSenderProof -benchtime=10s
go test -run=TestParallelBenchmarkVerificationSenderProof -v
```

### Issue Action Generation

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkIssuer`

#### Purpose
Complete issue action creation from inputs to serialized output.

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkIssuer -benchtime=10s
```

### Issue Action Verification

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkProofVerificationIssuer`

#### Purpose
Deserialization and verification of issue actions.

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkProofVerificationIssuer -benchtime=10s
```

---

## Layer 3: Service Layer

The Service Layer provides the highest level of abstraction for complete end-to-end operations through the Service APIs.

### Transfer Generation Service

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkTransferServiceTransfer`
- `TestParallelBenchmarkTransferServiceTransfer`

#### Purpose
Complete end-to-end transfer operation generation through the high-level Transfer Service API.

#### Includes
- Token Loading from vault
- Input Preparation
- Sender Initialization
- Output Processing
- Action Generation and Serialization
- Audit Information collection

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkTransferServiceTransfer -benchtime=10s
go test -run=TestParallelBenchmarkTransferServiceTransfer -v
```

### Issue Generation Service

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkIssueServiceIssue`
- `TestParallelBenchmarkIssueServiceIssue`

#### Purpose
End-to-end issue operation generation through the high-level Issue Service API.

#### Includes
- Wallet lookup and signer resolution
- Output token creation
- Action and metadata serialization

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkIssueServiceIssue -benchtime=10s
go test -run=TestParallelBenchmarkIssueServiceIssue -v
```

### Auditor Check Service

**Location:** `token/core/fabtoken/v1/`

#### Tests
- `BenchmarkAuditorServiceCheck`
- `TestParallelBenchmarkAuditorServiceCheck`

#### Purpose
End-to-end audit verification through the high-level Auditor Service API.

#### Includes
- Action deserialization
- Request verification
- Identity matching via the driver deserializer

#### Example Commands
```bash
cd token/core/fabtoken/v1
go test -bench=BenchmarkAuditorServiceCheck -benchtime=10s
go test -run=TestParallelBenchmarkAuditorServiceCheck -v
```

### Transfer/Issue Validator Service

**Location:** `token/core/fabtoken/v1/validator/`

#### Tests
- `BenchmarkValidatorTransfer`
- `TestParallelBenchmarkValidatorTransfer`
- `BenchmarkValidatorIssue`
- `TestParallelBenchmarkValidatorIssue`

#### Purpose
Complete validation pipeline performance, including all signature and business logic checks.

#### Example Commands
```bash
cd token/core/fabtoken/v1/validator
go test -bench=BenchmarkValidatorTransfer -benchtime=10s
go test -run=TestParallelBenchmarkValidatorTransfer -v
go test -bench=BenchmarkValidatorIssue -benchtime=10s
go test -run=TestParallelBenchmarkValidatorIssue -v
```

---

## Testing Strategy

### Understanding the Layers

Each layer tests a **different code abstraction level independently**:

- **Layer 1** tests the core action structure generation.
- **Layer 2** tests the action creation/verification code.
- **Layer 3** tests the service layer code:
  - **Transfer Generation Service**: end-to-end transfer generation
  - **Issue Generation Service**: end-to-end issue generation
  - **Auditor Check Service**: audit verification
  - **Transfer Validator Service**: validation logic

---

## Parallel Testing Variants

Most layers include parallel test variants that run benchmarks concurrently:
- **`Benchmark*Parallel`**: Go's built-in parallel benchmarking (`*testing.B`)
- **`TestParallelBenchmark*`**: Custom parallel benchmarking framework (`*testing.T`)

These help identify concurrency issues and measure performance under load.
