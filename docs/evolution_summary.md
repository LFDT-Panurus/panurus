# Evolution of Panurus (v0.4.0 -> Present)

Since tag `v0.4.0` (January 2025), Panurus has undergone a significant architectural and functional evolution, comprising over 850 commits. The evolution can be summarized in several key areas:

## Architectural Refinement: Common Driver Framework
Panurus has transitioned toward a more modular and robust internal driver architecture. A major shift involved moving core driver interfaces and shared logic into the `token/core/common/driver` package. This decoupling allows for more uniform driver implementations and simplifies the introduction of new token technologies.
*   *See also:* [**Driver API Overview**](driverapi.md)

## Privacy-Preserving Driver Evolution: `zkatdlog/nogh`
One of the most substantial additions is the `nogh` (**No Graph Hiding**) variant of the `zkatdlog` driver. This variant represents an optimization and refinement of the zero-knowledge token logic, providing a more efficient way to handle private transactions by omitting graph-hiding properties. 
*   *See also:* [**DLog w/o Graph Hiding (NOGH) Driver**](drivers/dlogwogh.md)

- **Upgradability Support:** The `nogh` driver explicitly supports token upgradability. 
    - **In-place Upgrades:** Compatible legacy tokens (like Fabtoken) can be spent directly by the `nogh` driver. The driver automatically generates Pedersen commitments and an "Upgrade Witness" on the fly, allowing for transparent migration during standard transfer operations. Criteria for compatibility include format inclusion in the driver's supported list and precision alignment (e.g., legacy precision $\le$ current max precision).
    - **Burn and Re-issue:** For major migrations, it implements a "Burn and Re-issue" mechanism. This is facilitated by a dedicated `upgrade` service that handles challenge-response protocols and proof verification to ensure supply consistency during migration.
    *   *See also:* [**Upgradability Guide**](upgradability.md)
- **Data Model & Protocol Buffers:** The `nogh` driver introduces a refined data model for tokens and metadata. Tokens are represented as Pedersen commitments to the token type, value, and blinding factor. Newly defined protocol buffers support this optimized model.
    *   *See also:* [**DLog Protobuf Messages**](drivers/dlogwogh.md#protobuf-messages)
- **Driver Versioning:** The implementation follows a clear versioning strategy, with the current `nogh` driver logic residing in the `v1` sub-package (`token/core/zkatdlog/nogh/v1`). This versioning ensures that future protocol changes can be introduced without breaking backward compatibility for existing ledger data.
- **Crypto & Math:** New `bls12_381_bbs` curve support and improved cryptographic primitives are integrated into the `v1` implementation.
- **Auditing:** A dedicated auditing layer (`v1/audit`) provides necessary compliance tools for private token systems.

## Enhanced Security: Protocol V2
A major security enhancement introduced **Protocol V2** for token request signatures, addressing critical vulnerabilities in the original implementation:

- **Structured Signature Format:** Protocol V2 uses ASN.1-structured messages with explicit field boundaries, eliminating boundary ambiguity attacks present in Protocol V1's simple concatenation approach.
- **Input Validation:** Comprehensive validation with typed errors prevents DoS attacks through anchor size limits (128 bytes max) and ensures non-empty anchors.
- **Secure Error Handling:** Binary data is hex-encoded in error messages, preventing sensitive data exposure in logs and log aggregation systems.
- **Collision Resistance:** The structured format ensures different (Request, Anchor) pairs always produce different signature messages, providing tamper-evident properties.
- **Backward Compatibility:** Protocol V1 remains supported for existing deployments while Protocol V2 is recommended for all new implementations.

*   *See also:* [**Protocol Versions and Signature Security**](driverapi.md)

## Cryptographic Optimization: Compressed Sigma Protocol Range Proofs
Panurus now implements **Compressed Sigma Protocol-Based Range Proofs**, providing significant performance improvements for zero-knowledge range validation:

- **Reduced Proof Size:** Compressed proofs are substantially smaller than traditional Bulletproofs, reducing on-chain storage and network bandwidth requirements.
- **Faster Verification:** Optimized verification algorithms improve transaction validation throughput.
- **Configurable Executor Patterns:** Introduction of `ExecutorProvider`, `WorkerPoolExecutor`, and `UnboundedExecutor` allows fine-tuned control over proof generation and verification parallelism.
- **Constructor Injection:** Executor patterns are threaded through cryptographic components (`RangeCorrectnessProver`, `RangeCorrectnessVerifier`) enabling flexible performance tuning.

## Token Transaction (TTX) Service Modernization
The Token Transaction service, a core component for orchestrating token lifecycles, has been significantly refactored:

- **Dependency Injection Pattern:** The introduction of the `dep/` sub-package in the TTX service enables better dependency management, reducing circular dependencies and drastically improving unit testability.
- **Multisig Support:** New capabilities for multi-signature transactions have been introduced in `token/services/ttx/multisig`.
- **Finality Tracking:** Improved transaction finality handling via the new `token/services/ttx/finality` package, including a new `TTXRecoveryHandler` for synchronous transaction recovery.
- **Transaction Recovery:** A new recovery mechanism has been introduced to automatically re-register finality listeners for pending transactions that may have lost their listeners due to node restarts or network issues. The recovery service includes:
    - **Automatic Re-registration:** Scans for pending transactions and re-establishes finality listeners on startup
    - **Configurable Retry Logic:** Supports exponential backoff with configurable retry limits
    - **Backend-Agnostic:** Works with both PostgreSQL and SQLite storage backends
    - **Network Integration:** Instantiated by both Fabric and FabricX network services
    - **Dual Database Support:** Operates on TTXDB for regular transactions and AuditDB for auditor nodes

*   *See also:* [**TTX Service Documentation**](services/ttx.md), [**Transaction Recovery Service**](services/storage/recovery.md)

## Core Service Enhancements
Panurus's foundational services have been matured:

- **Identity Service:** Reorganized to better handle various identity types, with improved support for Idemix (privacy-preserving) and X.509 (standard) identities.
    *   *See also:* [**Identity Service**](services/identity.md)
- **Storage Service:** Enhanced with more efficient and reliable ways to manage token and transaction databases, including:
    - **Database Indexing:** Added missing indexes for token queries, significantly improving query performance
    - **Query Enhancements:** New `SearchDirection` support in `QueryTransactionsParams` for flexible result ordering
    - **Recovery Service Integration:** Built-in transaction recovery capabilities for handling finality listener failures
    - **Keystore Cleanup Service:** Automatic deletion of cryptographic keys for deleted tokens, improving security and reducing storage overhead
    *   *See also:* [**Storage Service**](services/storage.md), [**Keystore Cleanup Service**](services/storage/keystore_cleanup.md)
- **Network Service:** Expanded to handle more complex Fabric network interactions and better integration with the Fabric Smart Client. A major change was the **removal of the Orion-based implementation**, which has been replaced by the introduction of **FabricX support**, providing a more modern and integrated approach for advanced ledger interactions.
    *   *See also:* [**Network Service**](services/network.md)

## Performance & Benchmarking Infrastructure
Significant investments in performance measurement and optimization:

- **Automated Benchmarking Pipeline:** New Python-based tools (`cmd/benchmarking/`) for running comprehensive performance tests and generating detailed reports.
- **Service-Layer Benchmarks:** Added benchmarks for `IssueService` and `AuditorService` to measure end-to-end performance.
- **Executor-Aware Metrics:** Performance metrics now track executor strategy impact, enabling data-driven optimization decisions.
- **Profiling Integration:** Enhanced integration with Go's profiling tools (pprof, trace) for deep performance analysis.
- **Benchmark Service:** New dedicated service (`token/services/benchmark`) providing a flexible framework for performance testing with configurable workloads and detailed reporting.

*   *See also:* [**Benchmark Service**](services/benchmark.md), [**Benchmarking Documentation**](drivers/benchmark/benchmark.md)

## Developer Experience: Testing & Documentation
Panurus has seen a major push in both testing infrastructure and documentation:

- **Comprehensive Test Coverage:** Massive improvements across the codebase:
    - `token/services/tokens`: 80%+ coverage
    - `token/services/auditor`: 0% → 97.7% coverage
    - `token/services/certifier`: 41% → 62% coverage
    - `token/services/network`: 87% coverage
    - `token/services/ttx`: Comprehensive coverage with new tests
    - `token/services/nfttx`: 80% coverage
    - Integration tests expanded for fungible, NFT, DVP, and interop scenarios
- **Comprehensive Mocks:** A massive addition of mocks (`token/core/common/driver/mock`, etc.) simplifies application testing and improves SDK maintainability.
- **Expanded Documentation:** The `docs/` directory has been substantially populated with detailed guides on driver APIs, Panurus usage, architectural diagrams, and development best practices.
- **Integration Tests:** The `integration/` tests have been expanded to cover new use cases, ensuring the stability of the evolved features.
    *   *See also:* [**Testing Guide**](development/testing.md)

## Bug Fixes & Security Hardening
Numerous critical bug fixes and security improvements:

- **Asset Safety Fixes:** Plugged five silent asset-safety bugs across selector, storage, and HTLC components
- **Selector Improvements:** Fixed token lock leaks in sherdlock selector on bad quantity errors
- **Certifier Hardening:** Enhanced request validation and prevented information leakage in certification protocol
- **Transfer Action Security:** Fixed issuer identity check vulnerabilities
- **Error Handling:** Replaced panics with proper error returns across multiple components, improving system stability
- **Immutability:** Made operations immutable to prevent unintended state modifications
- **Hash Validation:** Re-enabled `IsValid` checks with debug-level gating for better integrity verification

## High-Level Token API Evolution
The developer-facing Token API has been refined for better usability, consistency, and cross-version stability:

-   **Ubiquitous Context Support:** Almost all API methods (in `WalletManager`, `Vault`, `TMS`, etc.) now accept a `context.Context`. This enables standard Go practices for cancellation, timeouts, and request-scoped tracing.
-   **Unified Option Pattern:** The introduction of a centralized `opts.go` and the functional options pattern (e.g., `WithTMSID`, `WithDuration`) provides a consistent and extensible way to configure API calls.
-   **New `TokensService`:** A high-level service for advanced token operations, including de-obfuscation and support for the new in-place upgrade challenge/proof protocol.
-   **Robust TMS Management:** The `ManagementService` now performs more thorough eager initialization of its sub-services and supports dynamic cache clearing via the `Update` mechanism, facilitating seamless public parameter rotations.
-   **Enhanced Querying:** The `Vault` and `QueryEngine` have been expanded with new methods like `GetTokenOutputs` and `WhoDeletedTokens`, providing deeper insights into the token lifecycle on the ledger.
-   **Issuer Wallet Balances:** `IssuerWallet` now exposes `IssuedBalance`, `RedeemedBalance`, and net `Balance`, filterable by token type and time range. Redeem outputs (empty owner) are attributed to the signing issuer and persisted with a `Redeemed` flag in the `TokenDB`, so the issuer node can track its outstanding issued supply.
-   **Protobuf Backed Requests:** The high-level `Request` structures now leverage versioned Protobuf messages internally, ensuring that transaction blueprints created by one node version can be understood by others.
-   **Improved Error Handling:** Wallet methods (`IssuerWallet`, `AuditorWallet`, `CertifierWallet`, `OwnerWallet`) now return errors instead of panicking, enabling better error handling and recovery.
-   **Clone Support:** Added `Clone()` methods for safe copying of critical data structures.

*   *See also:* [**Token API Documentation**](tokenapi.md)

## Dependency Management & Tooling
- **Updated Dependencies:** Regular updates to OpenTelemetry, go-jose, and other critical dependencies for security and performance
- **Enhanced Linting:** Upgraded to golangci-lint v2.11.4 with expanded rule sets
- **Issue Templates:** New GitHub issue templates for bug reports, feature requests, and good first issues
- **Statistics Tools:** New tools for generating repository statistics and analyzing contribution patterns

## Conclusion
The evolution since `v0.4.0` marks Panurus's transition into a mature, production-ready, and feature-rich framework. The focus on security (Protocol V2), reliability (Recovery Service), performance (Compressed Range Proofs, Benchmarking), and quality (80%+ test coverage) has solidified its role as a robust tool for building tokenized applications on Hyperledger Fabric. The architectural clarity, optimized privacy-preserving drivers, and comprehensive developer experience through documentation and testability position Panurus for continued growth and adoption.