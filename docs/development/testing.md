# Testing Guide

This document outlines how to run tests for Panurus.

## Getting Started

To work with Panurus and run tests, you first need to clone the repository and set up your environment.

### Clone the Repository

Clone the code and make sure it is on your `$GOPATH`.
(Important: we assume in this documentation and default configuration that your `$GOPATH` has a single root-directory!).
Sometimes, we use `$PANURUS_PATH` to refer to Panurus repository in your filesystem.

```bash
export PANURUS_PATH=$GOPATH/src/github.com/LFDT-Panurus/panurus
git clone https://github.com/LFDT-Panurus/panurus.git $PANURUS_PATH
```

## Prerequisites

Before running tests, ensure you have the necessary tools and environment set up.

Panurus uses a system called `NWO` from Fabric Smart Client for its integration tests and samples to programmatically create a fabric network along with the fabric-smart-client nodes.

1.  **Install Tools**:
    ```bash
    make install-tools
    ```

    After installing the tools, run the checks to verify that everything is in order:
    ```bash
    make checks
    ```

2.  **Download Fabric Binaries**:
    The integration tests require Hyperledger Fabric binaries.
    
    In order for a fabric network to be able to be created you need to ensure you have downloaded the appropriate version of the hyperledger fabric binaries from [Fabric Releases](https://github.com/hyperledger/fabric/releases) and unpack the compressed file onto your file system. This will create a directory structure of /bin and /config. You will then need to set the environment variable `FAB_BINS` to the `bin` directory.
    
    **Do not store the fabric binaries within your panurus cloned repo as this will cause problems running the samples and integration tests as they will not be able to install chaincode.**

    Almost all the samples and integration tests require the fabric binaries to be downloaded and the environment variable `FAB_BINS` set to point to the directory where these binaries are stored. One way to ensure this is to execute the following in the root of the panurus project:

    ```bash
    make download-fabric
    export FAB_BINS=$PWD/../fabric/bin
    ```
    
    You can also use this to download a different version of the fabric binaries, for example:
    ```shell
    FABRIC_VERSION=2.5 make download-fabric
    ```

3.  **Docker Images**:
    Build the necessary Docker images for testing.
    ```bash
    make testing-docker-images
    make docker-images
    ```

## Unit Tests

You can run unit tests using the following make targets:

-   **Standard Unit Tests**:
    ```bash
    make unit-tests
    ```
-   **Unit Tests with Race Detection**:
    ```bash
    make unit-tests-race
    ```
-   **Regression Unit Tests**:
    These tests ensure that recent changes have not reintroduced previously fixed bugs or broken existing functionality.
    ```bash
    make unit-tests-regression
    ```

## Integration Tests

Integration tests are crucial for verifying the interaction between different components.

Run specific integration tests using the `integration-tests-<target>` pattern.

### Common Test Targets

Here are some common integration test targets (refer to `.github/workflows/tests.yml` or `integration/` folder for a full list):

-   `dlog-fabric-t1`
-   `fabtoken-fabric-t1`
-   `nft-dlog`
-   `nft-fabtoken`
-   `dvp-fabtoken`
-   `interop-fabtoken-t1`

### Example Usage

To run the `dlog-fabric-t1` test:

```bash
make integration-tests-dlog-fabric-t1
```

## Fabric-X Tests

For tests involving Fabric-X (starts with `fabricx`), you need additional setup:

```bash
make fxconfig configtxgen fabricx-docker-images
make integration-tests-fabricx-dlog-t1
```

## Cleaning Up

After running tests, especially integration tests that spin up Docker containers, you might want to clean up your environment.

```bash
# Clean up Docker artifacts (containers, volumes, networks) and generated files
make clean

# Remove all Docker containers (running and stopped)
make clean-all-containers

# Remove Fabric peer images
make clean-fabric-peer-images
```
