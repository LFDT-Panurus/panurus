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

`gen fabtoken.v1` flags:

| Flag | Description |
|------|-------------|
| `-o`, `--output` | Output folder. Default: `.`. |
| `--cc` | Also generate the chaincode package. |
| `-a`, `--auditors` | Auditor MSP directories. The certificate is read from `signcerts/`. |
| `-s`, `--issuers` | Issuer MSP directories. The certificate is read from `signcerts/`. |
| `-v`, `--version` | Override the protocol version in the public parameters. |
| `-x`, `--extra` | Extra data as `key=path`. The file content is stored under `key`. |
| `--x509-owner-roots` | Root CA certificates of x509 owners. Each value is a PEM file or a directory of single-certificate PEM files (for example `msp/cacerts`). Any other entry in the directory is an error. |
| `--x509-owner-intermediates` | Intermediate CA certificates of x509 owners. Same format (for example `msp/intermediatecerts`). Requires `--x509-owner-roots`. |

With `--x509-owner-roots`, an x509 owner identity is trusted only if its certificate chains to one of the roots.
Issuer and auditor identities in the public parameters are exempt from the chain check.
Without it, `tokengen` prints a warning: any x509 owner certificate is accepted, as before.
`tokengen` validates the certificates before it writes the public parameters.
See [Public Parameters](../../docs/public_parameters.md) for the full rules.

```bash
tokengen gen fabtoken.v1 --issuers ./msp/issuer --auditors ./msp/auditor \
  --x509-owner-roots ./owners/msp/cacerts \
  --x509-owner-intermediates ./owners/msp/intermediatecerts \
  --output ./params
```

#### Inspect Public Parameters
```bash
tokengen pp print --input ./params/fabtokenv1_pp.json
```

## Configuration

`tokengen` can also be configured via environment variables prefixed with `CORE_`. For example, `CORE_LOGGING_LEVEL=debug` will set the logging level to debug.
