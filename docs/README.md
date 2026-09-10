# Fabric Token SDK Documentation

Welcome to the Fabric Token SDK documentation.

## Core Concepts

*   [**Token SDK Overview**](tokensdk.md): Start here to understand the architecture, key concepts (Tokens, Wallets, Privacy), and layers of the SDK.
*   [**Token API**](tokenapi.md): The high-level API for interacting with tokens.
*   [**Token API Usage**](token_sdk_usage.md): API Usage Guide.
*   [**Driver API**](driverapi.md): The interface for building token drivers.
*   [**Configuration**](configuration.md): Token-SDK configuration.
*   [**Services**](services.md): Additional services like transaction assembly.
*   [**Upgradability**](upgradability.md): How to upgrade tokens, drivers, and storage.
*   [**Public Parameters Lifecycle**](public_parameters.md): How public parameters are generated, published, and updated across the network.
*   [**Token Transaction Finality on Fabric**](services/finality.md): Step-by-step walkthrough of how finality is detected and reflected in local storage.

## Development

If you are developing *using* the SDK or contributing *to* the SDK, check out the [Development](development/development.md) section.

*   [General Guidelines](development/general.md)
*   [Idiomatic Go](development/idiomatic.md)
*   [Testing](development/testing.md)
*   [Test & Benchmark Profiler](../cmd/profiler/README.md)

## Evolution

*   [**Evolution Summary (v0.4.0 -> Present)**](evolution_summary.md): A summary of how the functionalities provided by the token-sdk have evolved since tag v0.4.0.

## Guides & Tutorials

*   [Fabric Smart Client](https://github.com/hyperledger-labs/fabric-smart-client): The underlying platform.
*   [Fabric Samples](https://github.com/hyperledger/fabric-samples/tree/main/token-sdk): Runnable examples.
