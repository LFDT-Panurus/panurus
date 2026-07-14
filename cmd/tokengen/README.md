# Tokengen

`tokengen` is a utility for generating Panurus material, such as public parameters, token chaincode packages, and other cryptographic artifacts. 

It is primarily used for pre-configuring development and testing environments.

## Build

To build `tokengen`, run the following command from the root of the repository:

```bash
make tokengen
```

The binary will be generated in the `$GOROOT/bin` directory.

## Usage

The `tokengen` tool uses a command-line interface with several subcommands. You can always use the `--help` flag to see available options for any command.

```bash
tokengen [command] --help
```

### Core Commands

- **`gen`**: Generates public parameters for specific drivers (e.g., `fabtoken.v1`, `zkatdlognogh.v1`).
- **`update`**: Updates certificates within existing public parameters.
- **`pp print`**: Inspects and prints human-readable details of a public parameters file.
- **`certifier-keygen`**: Generates key pairs for token certifiers.
- **`version`**: Displays the build version information.

> Topology-driven artifact generation previously offered as `tokengen artifacts` now lives in a separate binary, [`artifactgen`](../artifactgen/README.md). Splitting it keeps `tokengen`'s dependency surface small (it no longer links the `integration/nwo` test framework).

### Examples

#### Generate Public Parameters for FabToken
```bash
tokengen gen fabtoken.v1 --auditors ./msp/auditor --issuers ./msp/issuer --output ./params
```

#### Inspect Public Parameters
```bash
tokengen pp print --input ./params/fabtokenv1_pp.json
```

## Configuration

`tokengen` can also be configured via environment variables prefixed with `CORE_`. For example, `CORE_LOGGING_LEVEL=debug` will set the logging level to debug.
