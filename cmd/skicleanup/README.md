# skicleanup

`skicleanup` is a diagnostic command-line tool for inspecting orphaned signer entries in a Panurus token database.

An **orphaned signer** is an entry in the `Signers` identity table whose identity is not referenced by any token in the `Tokens` table (neither spent nor unspent). These entries may accumulate over time and represent identities for which the corresponding cryptographic keys can safely be removed from the keystore.

## Build

```bash
make skicleanup
```

The binary is installed to `$GOPATH/bin/skicleanup`.

## Commands

### `config example`

Prints a fully-annotated YAML configuration file to stdout. Use this to bootstrap a new configuration:

```bash
skicleanup config example > config.yaml
```

No flags required.

### `signers`

Iterates all entries in the `Signers` table in batches, unmarshals each identity, and checks whether it appears in the `Tokens` table. For every orphaned signer (not found in `Tokens`), the command prints the identity hash and the derived Subject Key Identifiers (SKIs) to stdout.

This is a **read-only** operation. No data is modified or deleted.

```bash
skicleanup signers --config <path-to-config.yaml> [--batch-size <n>]
```

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--config` | Yes | — | Path to the YAML configuration file |
| `--batch-size` | No | `1000` | Number of signer rows read per database query |

**Output format** (one line per orphaned signer):

```
<identity-hash>: [<ski1>, <ski2>, ...]
```

### `owned-skis`

Lists the SKIs derived from the identities that own tokens. Token-driven cleanup should not remove these keys while the tokens exist.

The command reads every token owned by this node (`owner = true`), spent and unspent, from the `Tokens` table.
It takes each distinct `(owner_identity, owner_type)` pair and derives its SKIs with the `signers` extractor, extended to unwrap composite owners (see [SKI Extraction](#ski-extraction)).
The Ownership table is not used: tokens stored through the standard wallet path have no Ownership row.

This is a **read-only** operation.

```bash
skicleanup owned-skis --config <path-to-config.yaml> [--output <file>]
```

**Flags:**

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--config` | Yes | — | Path to the YAML configuration file |
| `--output` | No | — | Also write the list to this file (mode `0600`). The file is replaced atomically by a rename. A symlink at this path is replaced by a regular file; its target is not changed. `-` is rejected. |

**Output format:** the sorted, de-duplicated SKIs on stdout, one per line, and nothing else.
This differs from `signers`. Statistics and errors go to stderr.

**Errors:** the command never produces a partial list.
- If SKI extraction fails for any owner, stderr names every failing owner as `owner type [<type>] identity sha256 [<hash>]`. Nothing goes to stdout, the output file is not touched, and the exit code is non-zero.
- The list goes to stdout first, then to the file. If the stdout write fails, the file is not touched.
- If the file write fails after stdout succeeded, stdout holds the complete list, and the exit code is non-zero.
- A stdout write can fail partway (for example, a closed pipe). Consumers must check the exit code.

**This is not a complete keep-list.** Never delete every key that is not in this list.
- The SKIs come from the runtime cleanup extractor, plus composite unwrapping (see [SKI Extraction](#ski-extraction)).
- `x509` owners yield no SKIs: cleanup never deletes the wallet's X.509 key. The same holds for `x509` components of a composite.
- Composite owners (`htlc`, `multisig`, policy) yield the SKIs of their components. Components of an unknown type yield the SHA-256 fallback, not a keystore key.
- `idemixnym` components with no signer info row belong to a counterparty and yield no SKIs.
- The list is a snapshot of the `Tokens` table. Keys for identities that own no token yet are not listed.

**Caveat on spent tokens:** spent tokens are listed too. The runtime cleanup service deletes the SKIs of spent tokens after its TTL. Tokens it has already cleaned may have no keys left in the keystore.

## Configuration

The tool reads a YAML file that describes how to connect to the target database.
Generate a starter file with:

```bash
skicleanup config example > config.yaml
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

If the Panurus node was started with `token.storage.skipPrefix: true`, set the same flag here so the tool resolves the same unprefixed table names:

```yaml
driver: postgres
dataSource: "postgres://user:pass@localhost:5432/panurus?sslmode=disable"
tablePrefix: ""
skipPrefix: true
tableNames: {}
```

With `skipPrefix: true` the tool looks for tables named `<params>_<short_code>` instead of `fsc_<short_code>_<params>`.

### Table name overrides

If the Panurus node was started with non-default table names (using the `token.storage.tableNames` config option), set the same overrides here so the tool connects to the correct tables:

```yaml
driver: postgres
dataSource: "postgres://user:pass@localhost:5432/panurus?sslmode=disable"
tablePrefix: ""
skipPrefix: false
tableNames:
  id_signers: identity_signers   # Signers table was renamed
  tokens: my_tokens              # Tokens table was renamed
```

`skipPrefix` and `tableNames` can be combined:

```yaml
skipPrefix: true
tableNames:
  tokens: my_tokens   # final name: <params>_my_tokens  (no prefix)
```

Each key is a canonical short code; the value is the replacement short code used when generating the SQL table name. The FSC-generated prefix still wraps it unless `skipPrefix` is true. Unknown keys produce a warning and are ignored.

Available short codes: `movements`, `txs`, `tx_ends`, `requests`, `req_vals`, `tokens`, `tkn_own`, `tkn_crts`, `tkn_locks`, `public_params`, `wallets`, `id_cfgs`, `id_info`, `id_signers`, `key_store`, `eid_leases`, `tkn_ski_cleanups`.

## SKI Extraction

SKIs are derived from each identity (orphaned signers for `signers`, token owners for `owned-skis`) using the same extraction logic as the runtime cleanup service:

| Identity type | Extraction method |
|---------------|-------------------|
| `idemix` | Extracts SKI from the Idemix NymPublicKey |
| `idemixnym` | Looks up signer info in the identity store, then extracts SKI from the embedded Idemix signature |
| `x509` | Returns no SKIs (X.509 key material is not stored in the Panurus keystore) |
| `htlc`, `multisig`, policy | `owned-skis` only: unwraps the components (htlc sender and recipient, all multisig and policy components) and extracts each by its own type. An `idemixnym` component with no signer info row is a counterparty's and yields no SKIs; a row with empty or invalid info is an error. Malformed bytes or exceeded limits are an error. `signers` and the runtime use the fallback. |
| Any other type | SHA-256 hash of the raw identity bytes (fallback) |

The Tokens table stores policy owners with type `policy`. Rows written before `identity.TypeToString` gained the policy case carry `Type (6)`. `owned-skis` accepts both.

Composite unwrapping notes:
- The tool always uses the default composite-identity limits: depth 5, 16 components. It does not read the node configuration. If the node is configured with larger limits, an owner beyond the defaults fails the command, and no list is produced.
- Composites are decoded like the runtime deserializer. htlc scripts reject unknown JSON fields but accept trailing bytes. Duplicate components and policy expressions are not checked. This is harmless for a keep-list.

## Environment variables

Configuration values can be overridden with environment variables prefixed `CORE_`, using `_` in place of `.`:

```bash
CORE_DATASOURCE="postgres://..." skicleanup signers --config config.yaml
```
