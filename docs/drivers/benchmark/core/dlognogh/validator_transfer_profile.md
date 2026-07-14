# BenchmarkValidatorTransfer - Performance Profile

This document provides a detailed performance analysis of the validator transfer benchmark, showing the complete call hierarchy and timing breakdown for token transfer verification.

> **Note:** This call tree represents a snapshot in time. After performance improvements or code changes, regenerate the tree using the BenchProf tool (see instructions at the end).

## Test Configuration

This profile was generated using `BenchmarkValidatorTransfer` with the following parameters:

| Parameter | Value | Description |
|-----------|-------|-------------|
| **Bits** | 32 | Range proof bit size |
| **Curve** | BN254 | Elliptic curve used for zero-knowledge proofs |
| **Inputs** | 2 | Number of input tokens in the transfer |
| **Outputs** | 2 | Number of output tokens in the transfer |
| **Workers** | 88 | Parallel workers (runtime.NumCPU()) |
| **Signature Protocol** | Idemix | Identity-based signatures for token owners |
| **Auditor Signatures** | ECDSA (P-256) | Auditor and issuer use ECDSA |

**Cryptographic Protocols Used:**
- **Owner Signatures:** Idemix (Identity Mixer) - Privacy-preserving credential system
- **Auditor/Issuer Signatures:** ECDSA (Elliptic Curve Digital Signature Algorithm) on P-256 curve
- **Zero-Knowledge Proofs:** Range proofs using Inner Product Arguments (IPA) on BN254 curve

**Important:** The timing values in this document are specific to this configuration. Different parameters (e.g., 4 inputs/outputs, different curve, different signature protocol) will produce different results.

## Overview

`VerifyTokenRequestFromRaw` is the entry point for validating token requests. It deserializes the raw request and delegates to the verification logic, which validates:
1. Auditing signatures
2. Transfer actions (signatures and zero-knowledge proofs)
3. HTLC (Hash Time-Locked Contract) constraints

## Complete Call Tree

```
└── >>> (*Unknown).VerifyTokenRequestFromRaw [30.850136ms, 100.00%]
    ├── (*TokenRequest).FromBytes [59.246µs, 0.19%]
    │   ├── (*TokenRequest).ProtoReflect [380ns, 0.00%]
    │   ├── (*TokenRequest).Reset [6.831µs, 0.02%]
    │   └── (*TokenRequest).FromProtos [30.835µs, 0.10%]
    │       ├── GenericSliceOfPointers [472ns, 0.00%]
    │       └── FromProtosSlice [604ns, 0.00%]
    │           └── (*AuditorSignature).FromProtos [173ns, 0.00%]
    ├── (*TokenRequest).MarshalToMessageToSign [16.121µs, 0.05%]
    ├── NewBackend [5.258µs, 0.02%]
    └── >>> (*Unknown).VerifyTokenRequest [30.731184ms, 99.61%]
        ├── (*Unknown).VerifyAuditing [186.608µs, 0.60%]
        │   └── AuditingSignaturesValidate [185.102µs, 0.60%]
        │       ├── (*PublicParams).Auditors x2 [3.782µs, 0.01%]
        │       ├── (*Deserializer).GetAuditorVerifier [37.251µs, 0.12%]
        │       │   └── >>> (*TypedVerifierDeserializerMultiplex).DeserializeVerifier [36.81µs, 0.12%]
        │       │       ├── UnmarshalTypedIdentity [13.157µs, 0.04%]
        │       │       └── >>> (*TypedIdentityVerifierDeserializer).DeserializeVerifier [22.094µs, 0.07%]
        │       │           └── (*IdentityDeserializer).DeserializeVerifier [17.551µs, 0.06%]
        │       │               └── DeserializeVerifier [17.236µs, 0.06%]
        │       │                   ├── PemDecodeKey [16.258µs, 0.05%]
        │       │                   └── NewECDSAVerifier [416ns, 0.00%]
        │       └── (*Backend).HasBeenSignedBy [142.667µs, 0.46%]
        │           ├── Base64 [230ns, 0.00%]
        │           └── (*ecdsaVerifier).Verify [138.394µs, 0.45%]
        │               └── IsLowS [422ns, 0.00%]
        ├── (*ActionDeserializer).DeserializeActions [122.268µs, 0.40%]
        │   └── (*Action).Deserialize [121.309µs, 0.39%]
        │       ├── (*TransferAction).ProtoReflect [4.321µs, 0.01%]
        │       ├── (*TransferAction).Reset [175ns, 0.00%]
        │       ├── GenericSliceOfPointers [539ns, 0.00%]
        │       ├── FromProtosSlice [27.538µs, 0.09%]
        │       │   └── (*ActionInput).FromProtos x2 [20.819µs, 0.07%]
        │       │       └── FromG1Proto x2 [16.83µs, 0.05%]
        │       └── FromG1Proto x2 [16.643µs, 0.05%]
        ├── (*Unknown).verifyIssues [12.782µs, 0.04%]
        └── >>> (*Unknown).verifyTransfers [30.407175ms, 98.56%]
            └── >>> (*Unknown).VerifyTransfer [30.404821ms, 98.56%]
                ├── TransferActionValidate [33.504µs, 0.11%]
                │   └── (*Action).Validate [33.082µs, 0.11%]
                │       ├── (*Token).Validate x4 [21.93µs, 0.07%]
                │       └── (*Action).IsRedeem [10.309µs, 0.03%]
                │           └── (*Token).IsRedeem x2 [6.606µs, 0.02%]
                ├── >>> TransferSignatureValidate [6.216641ms, 20.15%]
                │   ├── >>> (*Deserializer).GetOwnerVerifier x2 [5.612908ms, 18.19%]
                │   │   └── >>> (*TypedVerifierDeserializerMultiplex).DeserializeVerifier x2 [5.612046ms, 18.19%]
                │   │       ├── UnmarshalTypedIdentity x2 [25.119µs, 0.08%]
                │   │       └── >>> (*TypedIdentityVerifierDeserializer).DeserializeVerifier x2 [5.584705ms, 18.10%]
                │   │           └── >>> (*Deserializer).DeserializeVerifier x2 [5.580346ms, 18.09%]
                │   │               └── >>> (*Deserializer).Deserialize x2 [5.579273ms, 18.09%]
                │   │                   └── >>> (*Deserializer).DeserializeAgainstNymEID x2 [5.578593ms, 18.08%]
                │   │                       ├── (*SerializedIdemixIdentity).ProtoReflect x2 [568ns, 0.00%]
                │   │                       ├── (*SerializedIdemixIdentity).Reset x2 [5.385µs, 0.02%]
                │   │                       ├── NewIdentity x2 [4.647µs, 0.02%]
                │   │                       └── >>> (*Identity).Validate x2 [5.542037ms, 17.96%]
                │   │                           └── >>> (*Identity).verifyProof x2 [5.537436ms, 17.95%]
                │   ├── (*Backend).HasBeenSignedBy x2 [573.318µs, 1.86%]
                │   │   ├── Base64 x2 [638ns, 0.00%]
                │   │   └── (*NymSignatureVerifier).Verify x2 [571.008µs, 1.85%]
                │   └── (*PublicParams).Issuers [230ns, 0.00%]
                ├── TransferUpgradeWitnessValidate [275ns, 0.00%]
                ├── >>> TransferZKProofValidate [24.073677ms, 78.03%]
                │   ├── (*Action).GetOutputCommitments [289ns, 0.00%]
                │   ├── NewVerifier [6.979µs, 0.02%]
                │   │   ├── NewRangeCorrectnessVerifier [6.061µs, 0.02%]
                │   │   └── NewTypeAndSumVerifier [433ns, 0.00%]
                │   ├── (*Action).GetProof [2.686µs, 0.01%]
                │   └── >>> (*Verifier).Verify [24.059524ms, 77.99%]
                │       ├── (*Proof).Deserialize [609.503µs, 1.98%]
                │       │   └── Unmarshal [605.941µs, 1.96%]
                │       │       ├── (*TypeAndSumProof).Deserialize [125.08µs, 0.41%]
                │       │       │   ├── NewUnmarshaller [11.102µs, 0.04%]
                │       │       │   ├── (*unmarshaller).NextG1 [14.153µs, 0.05%]
                │       │       │   │   └── (*unmarshaller).Next [9.67µs, 0.03%]
                │       │       │   ├── (*unmarshaller).NextZrArray x2 [42.821µs, 0.14%]
                │       │       │   │   └── (*unmarshaller).Next x2 [5.811µs, 0.02%]
                │       │       │   └── (*unmarshaller).NextZr x4 [55.937µs, 0.18%]
                │       │       │       └── (*unmarshaller).Next x4 [26.942µs, 0.09%]
                │       │       └── (*RangeCorrectness).Deserialize [447.484µs, 1.45%]
                │       │           ├── NewArrayWithNew [3.569µs, 0.01%]
                │       │           └── Unmarshal [439.994µs, 1.43%]
                │       │               └── (*Unknown).Deserialize [426.14µs, 1.38%]
                │       │                   └── UnmarshalTo [421.759µs, 1.37%]
                │       │                       └── (*RangeProof).Deserialize x2 [392.084µs, 1.27%]
                │       │                           └── Unmarshal x2 [391.429µs, 1.27%]
                │       │                               ├── (*RangeProofData).Deserialize x2 [113.418µs, 0.37%]
                │       │                               │   ├── NewUnmarshaller x2 [40.21µs, 0.13%]
                │       │                               │   ├── (*unmarshaller).NextG1 x8 [35.727µs, 0.12%]
                │       │                               │   │   └── (*unmarshaller).Next x8 [15.964µs, 0.05%]
                │       │                               │   └── (*unmarshaller).NextZr x6 [35.765µs, 0.12%]
                │       │                               │       └── (*unmarshaller).Next x6 [19.288µs, 0.06%]
                │       │                               └── (*IPA).Deserialize x2 [238.53µs, 0.77%]
                │       │                                   ├── NewUnmarshaller x2 [56.379µs, 0.18%]
                │       │                                   ├── (*unmarshaller).NextZr x4 [30.527µs, 0.10%]
                │       │                                   │   └── (*unmarshaller).Next x4 [12.233µs, 0.04%]
                │       │                                   └── (*unmarshaller).NextG1Array x4 [149.945µs, 0.49%]
                │       │                                       └── (*unmarshaller).Next x4 [36.534µs, 0.12%]
                │       ├── (*Proof).Validate [132.437µs, 0.43%]
                │       │   ├── (*TypeAndSumProof).Validate [33.52µs, 0.11%]
                │       │   │   ├── CheckElement [7.586µs, 0.02%]
                │       │   │   │   └── isNilInterface [3.525µs, 0.01%]
                │       │   │   ├── CheckZrElements x2 [12.717µs, 0.04%]
                │       │   │   │   └── CheckBaseElement x4 [11.594µs, 0.04%]
                │       │   │   │       └── isNilInterface x4 [3.906µs, 0.01%]
                │       │   │   └── CheckBaseElement x4 [9.303µs, 0.03%]
                │       │   │       └── isNilInterface x4 [3.663µs, 0.01%]
                │       │   └── (*RangeCorrectness).Validate [98.499µs, 0.32%]
                │       │       └── (*RangeProof).Validate x2 [98.038µs, 0.32%]
                │       │           ├── (*RangeProofData).Validate x2 [46.849µs, 0.15%]
                │       │           │   ├── CheckElement x8 [36.647µs, 0.12%]
                │       │           │   │   └── isNilInterface x8 [11.627µs, 0.04%]
                │       │           │   └── CheckBaseElement x6 [8.777µs, 0.03%]
                │       │           │       └── isNilInterface x6 [723ns, 0.00%]
                │       │           └── (*IPA).Validate x2 [47.213µs, 0.15%]
                │       │               └── CheckZrElements x4 [46.589µs, 0.15%]
                │       │                   └── CheckBaseElement x20 [36.425µs, 0.12%]
                │       │                       └── isNilInterface x20 [20.651µs, 0.07%]
                │       ├── (*TypeAndSumVerifier).Verify [808.481µs, 2.62%]
                │       │   ├── GetG1Array [415ns, 0.00%]
                │       │   └── (G1Array).Bytes [10.989µs, 0.04%]
                │       │       └── AppendFixed32 [4.683µs, 0.02%]
                │       └── >>> (*RangeCorrectnessVerifier).Verify [22.497704ms, 72.93%]
                │           ├── NewRangeVerifier x2 [9.811µs, 0.03%]
                │           └── >>> (*rangeVerifier).Verify x2 [22.486225ms, 72.89%]
                │               ├── GetG1Array x4 [13.943µs, 0.05%]
                │               ├── (G1Array).Bytes x4 [27.127µs, 0.09%]
                │               │   └── AppendFixed32 x4 [6.041µs, 0.02%]
                │               ├── Two x4 [11.136µs, 0.04%]
                │               │   └── NewCachedZrFromInt x4 [10.008µs, 0.03%]
                │               ├── Zero x2 [4.528µs, 0.01%]
                │               │   └── NewCachedZrFromInt x2 [397ns, 0.00%]
                │               ├── SumOfPowersOfTwo x2 [397ns, 0.00%]
                │               ├── One x2 [10.797µs, 0.03%]
                │               │   └── NewCachedZrFromInt x2 [5.56µs, 0.02%]
                │               └── >>> (*rangeVerifier).verifyIPA x2 [21.119434ms, 68.46%]
                │                   ├── PowerOfTwo x64 [84.271µs, 0.27%]
                │                   ├── NewIPAVerifier x2 [7.655µs, 0.02%]
                │                   └── >>> (*ipaVerifier).Verify x2 [5.786362ms, 18.76%]
                │                       ├── GetG1Array x12 [20.201µs, 0.07%]
                │                       ├── (G1Array).Bytes x12 [201.783µs, 0.65%]
                │                       │   └── AppendFixed32 x12 [57.165µs, 0.19%]
                │                       ├── MarshalStd x2 [40.911µs, 0.13%]
                │                       ├── cloneGenerators x2 [134.973µs, 0.44%]
                │                       └── >>> computeSVector x2 [2.222623ms, 7.20%]
                │                           └── BatchInverse x4 [1.004519ms, 3.26%]
                ├── TransferHTLCValidate [65.504µs, 0.21%]
                │   ├── UnmarshalTypedIdentity x4 [53.827µs, 0.17%]
                │   └── (*Token).IsRedeem x2 [6.699µs, 0.02%]
                ├── TransferApplicationDataValidate [5.866µs, 0.02%]
                │   └── (*Action).GetMetadata [5.139µs, 0.02%]
                └── (*Action).GetMetadata [120ns, 0.00%]
```

> **Note:** Functions marked with `>>>` are the top 20 functions by time (see table below).

## Execution Flow Description

### 1. Entry Point (100%)
**`VerifyTokenRequestFromRaw`** - Deserializes and validates a raw token request
- **Time:** 30.850136ms (100%)
- **Purpose:** Main entry point for token request validation

### 2. Deserialization (0.19%)
**`(*TokenRequest).FromBytes`** - Converts raw bytes to TokenRequest structure
- **Time:** 59.246µs (0.19%)
- **Purpose:** Parse the binary token request format

### 3. Main Verification (99.61%)
**`(*Unknown).VerifyTokenRequest`** - Orchestrates all validation checks
- **Time:** 30.731184ms (99.61%)
- **Delegates to three main validation paths:**

#### 3.1 Auditing Validation (0.60%)
**`(*Unknown).VerifyAuditing`** → **`AuditingSignaturesValidate`**
- **Time:** 186.608µs (0.60%)
- **Purpose:** Verify auditor signatures on the request
- **Key Operations:**
  - Deserialize auditor verifier (37.251µs)
  - Verify signature (142.667µs)

#### 3.2 Action Deserialization (0.40%)
**`(*ActionDeserializer).DeserializeActions`** → **`(*Action).Deserialize`**
- **Time:** 122.268µs (0.40%)
- **Purpose:** Parse transfer actions from the request

#### 3.3 Transfer Verification (98.56%) - **CRITICAL PATH**
**`(*Unknown).verifyTransfers`** → **`(*Unknown).VerifyTransfer`**
- **Time:** 30.407175ms (98.56%)
- **Purpose:** Validate each transfer action
- **Three main sub-validations:**

##### 3.3.1 Signature Validation (20.15%)
**`TransferSignatureValidate`**
- **Time:** 6.216641ms (20.15%)
- **Purpose:** Verify sender signatures using Idemix credentials
- **Key Operations:**
  - **Owner Verifier Deserialization (18.19%):** Called twice (once per input)
    - Deserialize Idemix identity (5.612908ms)
    - Validate identity proof (5.537436ms)
  - **Signature Verification (1.86%):** Called twice
    - Verify pseudonym signature (573.318µs)

##### 3.3.2 Zero-Knowledge Proof Validation (78.03%) - **BOTTLENECK**
**`TransferZKProofValidate`** → **`(*Verifier).Verify`**
- **Time:** 24.073677ms (78.03%)
- **Purpose:** Verify zero-knowledge proofs for transfer correctness
- **Key Operations:**

**a) Proof Deserialization (1.98%)**
- Deserialize type and sum proof (125.08µs)
- Deserialize range correctness proof (447.484µs)
  - Includes IPA (Inner Product Argument) data (238.53µs)

**b) Type and Sum Verification (2.62%)**
- Verify token types and amounts match (808.481µs)

**c) Range Correctness Verification (72.93%) - MAIN BOTTLENECK**
- **`(*RangeCorrectnessVerifier).Verify`** → **`(*rangeVerifier).Verify`** (called twice)
  - **Time:** 22.497704ms (72.93%)
  - **Purpose:** Prove values are in valid range without revealing them
  - **Main Operation:** IPA Verification (68.46%)
    - **`(*rangeVerifier).verifyIPA`** (called twice, 21.119434ms)
      - **`(*ipaVerifier).Verify`** (called twice, 5.786362ms)
        - Serialize generators (201.783µs)
        - Clone generators (134.973µs)
        - **Compute S-vector (7.20%)** - Most expensive operation
          - Batch inverse computation (1.004519ms)

##### 3.3.3 HTLC Validation (0.21%)
**`TransferHTLCValidate`**
- **Time:** 65.504µs (0.21%)
- **Purpose:** Validate Hash Time-Locked Contract constraints (if present)

## Performance Summary

### Top 20 Functions by Time

*Note: These functions are marked with `>>>` in the call tree above*

| Function | Total Time | % of Root |
|----------|------------|-----------|
| `(*Unknown).VerifyTokenRequestFromRaw` | 30.850136ms | 100.00% |
| `(*Unknown).VerifyTokenRequest` | 30.731184ms | 99.61% |
| `(*Unknown).verifyTransfers` | 30.407175ms | 98.56% |
| `(*Unknown).VerifyTransfer` | 30.404821ms | 98.56% |
| `TransferZKProofValidate` | 24.073677ms | 78.03% |
| `(*Verifier).Verify` | 24.059524ms | 77.99% |
| `(*RangeCorrectnessVerifier).Verify` | 22.497704ms | 72.93% |
| `(*rangeVerifier).Verify` | 22.486225ms | 72.89% |
| `(*rangeVerifier).verifyIPA` | 21.119434ms | 68.46% |
| `TransferSignatureValidate` | 6.216641ms | 20.15% |
| `(*ipaVerifier).Verify` | 5.786362ms | 18.76% |
| `(*TypedVerifierDeserializerMultiplex).DeserializeVerifier` | 5.648856ms | 18.31% |
| `(*Deserializer).GetOwnerVerifier` | 5.612908ms | 18.19% |
| `(*TypedIdentityVerifierDeserializer).DeserializeVerifier` | 5.606799ms | 18.17% |
| `(*Deserializer).DeserializeVerifier` | 5.580346ms | 18.09% |
| `(*Deserializer).Deserialize` | 5.579273ms | 18.09% |
| `(*Deserializer).DeserializeAgainstNymEID` | 5.578593ms | 18.08% |
| `(*Identity).Validate` | 5.542037ms | 17.96% |
| `(*Identity).verifyProof` | 5.537436ms | 17.95% |
| `computeSVector` | 2.222623ms | 7.20% |

### Time Distribution

1. **Zero-Knowledge Proof Validation:** 78.03% (24.073677ms)
   - Range correctness: 72.93% (22.497704ms)
   - IPA verification: 68.46% (21.119434ms)
2. **Signature Validation:** 20.15% (6.216641ms)
   - Identity deserialization: 18.19% (5.612908ms)
   - Signature verification: 1.86% (573.318µs)
3. **Auditing:** 0.60% (186.608µs)
4. **Other:** 0.82% (253.071µs)

### Understanding the Timing Numbers

**What the numbers mean:**

Each function shows its **cumulative time** - the total time spent in that function INCLUDING all the work it does and all functions it calls.

**Format:** `FunctionName x[calls] [total_time, percentage]`

### Why Children Don't Always Sum to Parent Time

**Important:** When you add up all the children's times, they often DON'T equal the parent's time. This is NORMAL and EXPECTED.

**Example from this profile:**
```
(*rangeVerifier).verifyIPA x2 [21.119434ms, 68.46%]
  ├── PowerOfTwo x64 [84.271µs, 0.27%]
  ├── NewIPAVerifier x2 [7.655µs, 0.02%]
  └── (*ipaVerifier).Verify x2 [5.786362ms, 18.76%]
      ├── (G1Array).Bytes x12 [201.783µs, 0.65%]
      ├── MarshalStd x2 [40.911µs, 0.13%]
      ├── cloneGenerators x2 [134.973µs, 0.44%]
      └── computeSVector x2 [2.222623ms, 7.20%]
```

**Children sum:** 0.084 + 0.008 + 5.786 + 0.202 + 0.041 + 0.135 + 2.223 = **8.479ms**
**Parent time:** 21.119ms
**Difference:** 21.119 - 8.479 = **12.64ms** (60% of parent's time)

**Where's the missing 12.64ms?**

It's spent in `verifyIPA` doing work that's NOT in instrumented child functions:
- **Cryptographic operations:** Elliptic curve math, point multiplications
- **Vector operations:** Computing inner products, reducing vectors
- **Hash computations:** Fiat-Shamir challenges
- **Memory operations:** Allocating/copying large arrays
- **Loop overhead:** Multiple verification rounds
- **Inline code:** Assignments, conditionals, simple math

## How to Regenerate This Tree

This call tree was generated using the Profiler tool. To get an updated version after code changes:

```bash
cd /path/to/panurus/tools/profiler
./profile.sh BenchmarkValidatorTransfer -f VerifyTokenRequestFromRaw
```

For more profiler options and usage examples, see the [Profiler Tool README](../../../../../cmd/profiler/README.md).

## References

- [Profiler Tool](../../../../../cmd/profiler/README.md) - Automatic profiling tool for tests and benchmarks
- [Validator Implementation](../../../../../token/core/zkatdlog/nogh/v1/validator/)
- [Idemix Identity](../../../../../token/services/identity/idemix/)