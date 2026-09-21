# tokendiag

`tokendiag` is a diagnostic command-line tool for inspecting token-selector state in a
Panurus token database. It was added for
[#2395](https://github.com/LFDT-Panurus/panurus/issues/2395), a CERT load test that
showed severe lock contention on a small number of hot tokens.

## Build

```bash
make tokendiag
```

The binary is installed to `$GOPATH/bin/tokendiag`.

## Commands

### `config example`

Prints a fully-annotated YAML configuration file to stdout. Use this to bootstrap a new
configuration:

```bash
tokendiag config example > config.yaml
```

No flags required.

### `locks`

Reads every currently held row in the `token_locks` table, joined with the status of
its consuming transaction, and reports:

- every held lock, with its age and the consumer's status, oldest first;
- locks whose consumer has already reached a **terminal** status (`Confirmed`,
  `Deleted`, or `Orphan`) — these are **leaked** locks: nothing on the success path
  released them, so they sit until the next lease-age sweep (see mechanism 4 in #2395);
- a summary line (total locks, leaked count, oldest age) suitable for scripting.

This is a **read-only** operation. No data is modified or deleted.

```bash
tokendiag locks --config <path-to-config.yaml>
```

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--config` | Yes | — | Path to the YAML configuration file |

**Output format:**

```
--- Held locks (oldest first) ---
  token=<tx_id>:<idx> consumer_tx_id=<tx_id> age=<duration> status=<status>[  [LEAKED: ...]]

--- Summary ---
  Total locks held         : <n>
  Leaked (terminal consumer): <n>
  Oldest lock age           : <duration>
```

A single snapshot cannot distinguish "one token repeatedly re-contended" from "one
token held a long time" — that comparison requires running `locks` more than once and
diffing. The ranking here is by lock age only.

## Configuration

The tool reads a YAML file that describes how to connect to the target database.
Generate a starter file with:

```bash
tokendiag config example > config.yaml
```

### SQLite example

```yaml
driver: sqlite
dataSource: /var/lib/panurus/node/data.db
tablePrefix: ""
skipPrefix: false
tableNames: {}
```

### PostgreSQL example

```yaml
driver: postgres
dataSource: "host=db.example.com port=5432 user=panurus password=secret dbname=panurus sslmode=require"
tablePrefix: "prod_"
skipPrefix: false
tableNames: {}
```

### Skipping the prefix

If the Panurus node was started with `token.storage.skipPrefix: true`, set the same
flag here so the tool resolves the same unprefixed table names:

```yaml
driver: postgres
dataSource: "postgres://user:pass@localhost:5432/panurus?sslmode=disable"
tablePrefix: ""
skipPrefix: true
tableNames: {}
```

### Table name overrides

If the Panurus node was started with non-default table names (using the
`token.storage.tableNames` config option), set the same overrides here so the tool
connects to the correct tables. The `locks` command reads `tkn_locks` and `requests`:

```yaml
driver: postgres
dataSource: "postgres://user:pass@localhost:5432/panurus?sslmode=disable"
tablePrefix: ""
skipPrefix: false
tableNames:
  tkn_locks: my_token_locks
```

## Environment variables

Configuration values can be overridden with environment variables prefixed `CORE_`,
using `_` in place of `.`:

```bash
CORE_DATASOURCE="postgres://..." tokendiag locks --config config.yaml
```
