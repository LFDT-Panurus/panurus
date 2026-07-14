# Panurus Documentation

Welcome to Panurus documentation.

## Core Concepts

*   [**Panurus Overview**](tokensdk.md): Start here to understand the architecture, key concepts (Tokens, Wallets, Privacy), and layers of Panurus.
*   [**Token API**](tokenapi.md): The high-level API for interacting with tokens.
*   [**Token API Usage**](token_sdk_usage.md): API Usage Guide.
*   [**Driver API**](driverapi.md): The interface for building token drivers.
*   [**Configuration**](configuration.md): Panurus configuration.
*   [**Services**](services.md): Additional services like transaction assembly.
*   [**Upgradability**](upgradability.md): How to upgrade tokens, drivers, and storage.
*   [**Public Parameters Lifecycle**](public_parameters.md): How public parameters are generated, published, and updated across the network.

## Command-Line Tools

Panurus ships several standalone CLI tools, each living in its own Go module under `cmd/`.

| Tool | Description |
|------|-------------|
| [**tokengen**](../cmd/tokengen/README.md) | Generates public parameters, token chaincode packages, and other cryptographic artifacts. Used to pre-configure development and test environments. |
| [**artifactgen**](../cmd/artifactgen/README.md) | Topology-driven artifact generation (previously part of `tokengen`). Kept separate to avoid pulling in the `integration/nwo` test framework. |
| [**skicleanup**](../cmd/skicleanup/README.md) | Diagnostic tool that lists orphaned signer entries and their derived SKIs. Connects directly to an existing Panurus database (SQLite or PostgreSQL). |

## Development

If you are developing *using* Panurus or contributing *to* Panurus, check out the [Development](development/development.md) section.

*   [General Guidelines](development/general.md)
*   [Idiomatic Go](development/idiomatic.md)
*   [Testing](development/testing.md)
*   [Test & Benchmark Profiler](../cmd/profiler/README.md)

## Evolution

*   [**Evolution Summary (v0.4.0 -> Present)**](evolution_summary.md): A summary of how the functionalities provided by Panurus have evolved since tag v0.4.0 when it was still called `token-sdk`.

## Guides & Tutorials

*   [Fabric Smart Client](https://github.com/hyperledger-labs/fabric-smart-client): The underlying platform.
*   [Fabric Samples](https://github.com/hyperledger/fabric-samples/tree/main/token-sdk): Runnable examples.
