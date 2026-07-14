# ZK-ATDLOG Testing Architecture

This document explains the testing architecture for the Zero-Knowledge Anonymous Token Discrete Logarithm (ZK-ATDLOG) implementation in Panurus.

> **Related Documentation:**
> - [Running Benchmarks](./dlognogh.md) - How to run performance benchmarks
> - [Regression Tests](./dlognogh_regression.md) - Backwards compatibility testing

## Overview

The ZK-ATDLOG tests are organized in a **layered architecture** that mirrors the **code abstraction levels**. Each layer represents a different level of the software stack - from low-level cryptographic primitives to high-level service APIs.

## Architecture Layers

Each layer represents a different **code abstraction level**.

```
┌─────────────────────────────────────────────────────────────────┐
│ Layer 3: Service Layer (Highest Abstraction)                    │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Generation Service                                     │
│ Location: token/core/zkatdlog/nogh/v1/                          │
│ Tests: - BenchmarkTransferServiceTransfer                       │
│        - TestParallelBenchmarkTransferServiceTransfer           │
│ Purpose: End-to-end transfer operation generation with vault,   │
│          audit, and metadata handling                           │
├─────────────────────────────────────────────────────────────────┤
│ Issue Generation Service                                        │
│ Location: token/core/zkatdlog/nogh/v1/                          │
│ Tests: - BenchmarkIssueServiceIssue                             │
│        - TestParallelBenchmarkIssueServiceIssue                 │
│ Purpose: End-to-end issue operation: wallet lookup, signer      │
│          resolution, ZK proof, audit-info encoding, serialize   │
├─────────────────────────────────────────────────────────────────┤
│ Auditor Check Service                                           │
│ Location: token/core/zkatdlog/nogh/v1/                          │
│ Tests: - BenchmarkAuditorServiceCheck                           │
│        - TestParallelBenchmarkAuditorServiceCheck               │
│ Purpose: Audit verification: action deserialization and         │
│          Pedersen commitment arithmetic                         │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Validator Service                                      │
│ Location: token/core/zkatdlog/nogh/v1/validator/                │
│ Tests: - BenchmarkValidatorTransfer                             │
│        - TestParallelBenchmarkValidatorTransfer                 │
│ Purpose: Transfer validation with signatures, business logic,   │
│          and cryptographic verification                         │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ Layer 2: Action Operations                                      │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Generation (token/core/zkatdlog/nogh/v1/transfer/)     │
│ Tests: - BenchmarkSender                                        │
│        - BenchmarkParallelSender                                │
│        - TestParallelBenchmarkSender                            │
│ Purpose: Transfer action generation and serialization           │
├─────────────────────────────────────────────────────────────────┤
│ Transfer Verification (token/core/zkatdlog/nogh/v1/transfer/)   │
│ Tests: - BenchmarkVerificationSenderProof                       │
│        - BenchmarkVerificationParallelSenderProof               │
│        - TestParallelBenchmarkVerificationSenderProof           │
│ Purpose: Deserialization and cryptographic format validation    │
│          and verification of transfer actions                   │
├─────────────────────────────────────────────────────────────────┤
│ Issue Generation (token/core/zkatdlog/nogh/v1/issue/)           │
│ Tests: - BenchmarkIssuer                                        │
│ Purpose: Issue action generation and serialization              │
├─────────────────────────────────────────────────────────────────┤
│ Issue Verification (token/core/zkatdlog/nogh/v1/issue/)         │
│ Tests: - BenchmarkProofVerificationIssuer                       │
│ Purpose: Deserialization and cryptographic verification of      │
│          issue actions                                          │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ Layer 1: Core Cryptographic Operations (Lowest Abstraction)     │
│ Location: token/core/zkatdlog/nogh/v1/transfer/                 │
│          token/core/zkatdlog/nogh/v1/crypto/rp/                 │
│ Tests: - BenchmarkTransferProofGeneration                       │
│        - TestParallelBenchmarkTransferProofGeneration           │
│ Purpose: Pure transfer ZK proof generation and serialization    │
│                                                                  │
│ Range Proof Systems (as of commit 586d4f58):                    │
│ • Bulletproofs: crypto/rp/bulletproof/ (IPA-based)              │
│ • CSP: crypto/rp/csp/ (Compressed Sigma Protocols)              │
└─────────────────────────────────────────────────────────────────┘
```

---

## Layer 1: Core Cryptographic Operations

**Location:** `token/core/zkatdlog/nogh/v1/transfer/` and `token/core/zkatdlog/nogh/v1/crypto/rp/`

**Range Proof Systems**: As of commit 586d4f58, two range proof implementations are available:
- **Bulletproofs** (`crypto/rp/bulletproof/`): IPA-based range proofs
- **CSP** (`crypto/rp/csp/`): Compressed Sigma Protocol range proofs with optimized verification

### Tests
- `BenchmarkTransferProofGeneration`
- `TestParallelBenchmarkTransferProofGeneration`

### Purpose
Pure zero-knowledge proof generation and serialization for transfer operations. The parallel version runs the same benchmark in multiple goroutines.

### Includes
- ZK proof computation (range proofs, sum proofs, type proofs)
- Proof serialization to bytes

### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1/transfer
go test -bench=BenchmarkTransferProofGeneration -benchtime=10s
go test -run=TestParallelBenchmarkTransferProofGeneration -v
```

---

## Layer 2: Action Operations

### Transfer Action Generation

**Location:** `token/core/zkatdlog/nogh/v1/transfer/`

#### Tests
- `BenchmarkSender`
- `BenchmarkParallelSender`
- `TestParallelBenchmarkSender`

#### Purpose
Complete transfer action creation from inputs to serialized output.

**Note:** `BenchmarkParallelSender` is a Go benchmark (uses `*testing.B`), while `TestParallelBenchmarkSender` is a test (uses `*testing.T`) that runs custom benchmarking. Same functionality, different frameworks.

#### Includes
- ZK proof generation (range proofs, sum proofs, type proofs)
- Input token handling
- Output token creation
- Action serialization

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1/transfer
go test -bench=BenchmarkSender -benchtime=10s
go test -bench=BenchmarkParallelSender -benchtime=10s
go test -run=TestParallelBenchmarkSender -v
```

### Transfer Action Verification

**Location:** `token/core/zkatdlog/nogh/v1/transfer/`

#### Tests
- `BenchmarkVerificationSenderProof`
- `BenchmarkVerificationParallelSenderProof`
- `TestParallelBenchmarkVerificationSenderProof`

#### Purpose
Deserialization and cryptographic format validation and verification of transfer actions.

#### Includes
- Action deserialization
- ZKP format validation
- ZK proof verification (range proofs, sum proofs, type proofs)

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1/transfer
go test -bench=BenchmarkVerificationSenderProof -benchtime=10s
go test -bench=BenchmarkVerificationParallelSenderProof -benchtime=10s
go test -run=TestParallelBenchmarkVerificationSenderProof -v
```

### Issue Action Generation

**Location:** `token/core/zkatdlog/nogh/v1/issue/`

#### Tests
- `BenchmarkIssuer`

#### Purpose
Complete issue action creation from inputs to serialized output.

#### Includes
- ZK proof generation (range proofs, same-type proofs)
- Output token creation
- Action serialization

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1/issue
go test -bench=BenchmarkIssuer -benchtime=10s
go test -bench=BenchmarkIssuer -benchmem
go test -bench=BenchmarkIssuer -cpuprofile=cpu.prof
```

### Issue Action Verification

**Location:** `token/core/zkatdlog/nogh/v1/issue/`

#### Tests
- `BenchmarkProofVerificationIssuer`

#### Purpose
Deserialization and cryptographic verification of issue actions.

#### Includes
- Action deserialization
- ZKP format validation
- ZK proof verification (range proofs, same-type proofs)

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1/issue
go test -bench=BenchmarkProofVerificationIssuer -benchtime=10s
```

---

## Layer 3: Service Layer

The Service Layer provides the highest level of abstraction, covering end-to-end operation generation and validation through the high-level service APIs.

### Transfer Generation Service

**Location:** `token/core/zkatdlog/nogh/v1/`

#### Tests
- `BenchmarkTransferServiceTransfer`
- `TestParallelBenchmarkTransferServiceTransfer`

#### Purpose
Complete end-to-end transfer operation generation through the high-level Transfer Service API.

#### Includes
- **Token Loading**: Loads input tokens from vault by their IDs, retrieves token data and metadata
- **Input Preparation**: Deserializes loaded tokens, extracts token commitments, metadata, owner information, and prepares upgrade witnesses if needed
- **Sender Initialization**: Creates a Sender instance with prepared inputs, sets up ZK proof generation context
- **Output Processing**: Extracts target values and owners from requested outputs, converts quantities to proper format, detects redeem operations (empty owner)
- **ZK Transfer Generation**: Generates ZK-SNARK transfer proof (range, sum, type proofs), creates output token commitments, produces output metadata with blinding factors
- **Metadata Enrichment**: Adds transfer action metadata attributes, attaches upgrade witnesses to inputs
- **Audit Information**: Collects audit info for all input token owners, prepares transfer input metadata
- **Output Metadata**: Collects audit info for all output recipients, handles redeem case (no recipient), serializes output metadata, creates transfer output metadata
- **Redeem Handling**: Selects authorized issuer for redeem operations, adds issuer to transfer action and metadata


#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1
go test -bench=BenchmarkTransferServiceTransfer -benchtime=10s
go test -run=TestParallelBenchmarkTransferServiceTransfer -v
```

### Issue Generation Service

**Location:** `token/core/zkatdlog/nogh/v1/`

#### Tests
- `BenchmarkIssueServiceIssue`
- `TestParallelBenchmarkIssueServiceIssue`

#### Purpose
End-to-end issue operation generation through the high-level Issue Service API.

#### Includes
- Wallet lookup and signer resolution
- ZK proof generation (range proofs, same-type proofs)
- Audit-info encoding for issuer and recipients
- Action and metadata serialization

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1
go test -bench=BenchmarkIssueServiceIssue -benchtime=10s
go test -run=TestParallelBenchmarkIssueServiceIssue -v
```

### Auditor Check Service

**Location:** `token/core/zkatdlog/nogh/v1/`

#### Tests
- `BenchmarkAuditorServiceCheck`
- `TestParallelBenchmarkAuditorServiceCheck`

#### Purpose
End-to-end audit verification through the high-level Auditor Service API.

#### Includes
- Action deserialization
- Pedersen commitment arithmetic (InspectOutput)
- Identity matching via the driver deserializer

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1
go test -bench=BenchmarkAuditorServiceCheck -benchtime=10s
go test -run=TestParallelBenchmarkAuditorServiceCheck -v
```

### Transfer Validator Service

**Location:** `token/core/zkatdlog/nogh/v1/validator/`

#### Tests
- `BenchmarkValidatorTransfer`
- `TestParallelBenchmarkValidatorTransfer`

#### Compatibility Tests
- `regression_test.go` (in `validator/regression/`)

#### Purpose
Complete validation pipeline performance, including all cryptographic and business logic checks.

#### Includes
- Action deserialization
- Token validation
- Signature verification (including auditors)
- Business logic checks (double-spend prevention, balance checks, etc.)
- ZKP format validation
- ZKP verification (range proofs, sum proofs, type proofs)

#### Example Commands
```bash
cd token/core/zkatdlog/nogh/v1/validator
go test -bench=BenchmarkValidatorTransfer -benchtime=10s
go test -run=TestParallelBenchmarkValidatorTransfer -v
```

---

## Testing Strategy

### Understanding the Layers

Each layer tests a **different code abstraction level independently**:

- **Layer 1** tests only the cryptographic proof generation code (`Prover.Prove()`)
- **Layer 2** tests the action creation/verification code (`Sender.GenerateZKTransfer()`, `Verifier.Verify()`)
- **Layer 3** tests the service layer code:
  - **Transfer Generation Service**: end-to-end transfer generation (`TransferService.Transfer()`)
  - **Issue Generation Service**: end-to-end issue generation (`IssueService.Issue()`)
  - **Auditor Check Service**: audit verification (`AuditorService.AuditorCheck()`)
  - **Transfer Validator Service**: validation logic (`Validator.VerifyTransfer()`)

**The layers do NOT build on each other** - they test different parts of the codebase at different abstraction levels.

---

## Parallel Testing Variants

Most layers include parallel test variants that run benchmarks concurrently:
- **`Benchmark*Parallel`**: Go's built-in parallel benchmarking (`*testing.B`)
- **`TestParallelBenchmark*`**: Custom parallel benchmarking framework (`*testing.T`)

These help identify concurrency issues and measure performance under load.

---

## Benchmark Parameters

All benchmarks support various configurations:
- **Bits**: 32, 64 (range proof bit sizes)
- **Curves**: BN254, BLS12_381_BBS_GURVY, BLS12_381_BBS_GURVY_FAST_RNG
- **Inputs**: 1, 2, 3 (number of input tokens for transfers)
- **Outputs**: 1, 2, 3 (number of output tokens)

Example with specific parameters:
```bash
go test -bench=BenchmarkSender/bits_32-curve_BN254-in_2-out_2 -benchtime=10s
