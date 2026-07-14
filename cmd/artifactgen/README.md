# Artifactgen

`artifactgen` is a development / testing utility that reads a topology description (YAML) and generates the corresponding Fabric Smart Client + Panurus artifacts (crypto material, configuration, etc.).

It was previously offered as the `artifacts` subcommand of [`tokengen`](../tokengen/README.md). It was split out so that the core `tokengen` binary no longer links the `integration/nwo` test framework, which trims `tokengen`'s transitive dependency surface substantially.

## Build

From the root of the repository:

```bash
make artifactgen
```

The binary is installed in `$GOPATH/bin`.

## Usage

```bash
artifactgen [command] --help
```

### Commands

- **`artifacts`**: Generates key material and configuration files from a topology description (YAML).
- (more subcommands may be added here over time)

### Example

```bash
artifactgen artifacts --topology ./topology.yaml --output ./artifacts
```

## Configuration

`artifactgen` honours the same environment variable prefix as `tokengen` (`CORE_`). For example, `CORE_LOGGING_LEVEL=debug` will set the logging level to debug.
