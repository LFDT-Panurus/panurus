# KVS-Backed Identity and Wallet Stores

Besides the SQL implementation, the identity and wallet stores can be backed by a **key-value
store** (`token/services/storage/db/kvs`). This is the backend used by nodes that keep their
identity material outside a relational database — in memory, in the Fabric Smart Client KVS, or
in [HashiCorp Vault](#hashicorp-vault-backend) (`token/services/storage/db/kvs/hashicorp`).

Only the identity-side stores have a KVS implementation:

| Store | Type | Interface |
|---|---|---|
| Identity configurations, identity data, signer info | `kvs.IdentityStore` | `identity/driver.IdentityStore` |
| Wallet bindings | `kvs.WalletStore` | `identity/driver.WalletStore` |

## Key Layout

Every entry is stored under a **composite key** built with
`fabric-smart-client/.../storage/kvs.CreateCompositeKey(objectType, attributes)`, which joins
the object type and the attributes with a `\x00` delimiter and terminates each of them with one.
All keys are scoped by the TMS id, so several TMSs can share one backend.

### `WalletStore` (`walletDB`)

| Attributes | Value | Read by |
|---|---|---|
| `tmsID, roleID, idHash, wID, meta` | identity metadata | `LoadMeta` |
| `tmsID, roleID, idHash, wID, configid` | identity configuration id | `GetConfID` |
| `tmsID, roleID, idHash` | wallet id | `GetWalletID`, `GetWalletIDs` |
| `tmsID, roleID, idHash, wID` | wallet id | `IdentityExists` |

`idHash` is `Identity.UniqueID()`, i.e. a base64-encoded hash of the identity.

### `IdentityStore` (`idb`)

| Attributes | Value | Read by |
|---|---|---|
| `configuration, tmsID, type, base64(id‖url)` | `IdentityConfiguration` | `GetConfiguration`, `IteratorConfigurations`, `ConfigurationsByID` |
| `data, tmsID, identity` | `RecipientData` | `GetAuditInfo`, `GetTokenInfo` |
| `signer, tmsID, idHash` | signer info | `GetSignerInfo`, `GetExistingSignerInfo` |

Two lookups have no key to match on and therefore scan a prefix and filter client-side:
`WalletStore.GetConfID` (the `configid` entries are role- and wallet-scoped, not keyed by the
identity hash alone) and `IdentityStore.ConfigurationsByID` (the key encodes the id and the url
together). Both are documented as such in the code.

## Write Atomicity

The KVS abstraction has no multi-key transaction, so a store operation that needs several keys
writes them one at a time. `WalletStore.StoreIdentity` writes up to four:

* every write is **idempotent** — repeating the call rewrites the same values; and
* the entry `IdentityExists` reads (`tmsID, roleID, idHash, wID`) is written **last**.

A failure part-way therefore leaves the identity reported as *not* bound, so the caller's retry
converges on the complete binding and a partially applied sequence is never mistaken for a
finished one.

`IdentityExists` returns `(bool, error)`. An error means the lookup itself failed and the answer
is unknown — it must not be read as "the binding does not exist". Note that this backend can only
report a malformed key that way: `KVS.Exists` is implemented over FSC's `GetExisting`, which drops
the underlying store error, so a read failure is indistinguishable from a missing key here.

## HashiCorp Vault Backend

The Vault backend maps a composite key onto a Vault path under the configured mount point: one
path component per key component.

### Path Encoding

Vault paths are `/`-separated and the Vault client cleans them (`path.Join`), so a key component
is **percent-escaped** before it becomes a path component:

| Component | Path component |
|---|---|
| `walletDB`, `0`, `MHg=+abc` | unchanged |
| `` (empty) | `%` |
| `a/b` | `a%2Fb` |
| `100%` | `100%25` |
| `.`, `..` | `%2E`, `%2E%2E` |

This makes the mapping injective, which the backend relies on in three ways:

1. Distinct keys always address distinct secrets. Without escaping,
   `CreateCompositeKey("", []string{"1"})` and `CreateCompositeKey("1", nil)` both resolve to
   `<mount>/1`.
2. A key listed by `GetByPartialCompositeID` can be decoded back into exactly the composite key
   it was stored under, so callers such as `WalletStore.GetConfID` can split it into attributes
   again. Identity hashes are base64-encoded and routinely contain `/`.
3. A `.` or `..` component cannot walk out of the configured mount point.

> [!WARNING]
> **Storage-format note**: entries whose key components contain `/` or `%` are stored under a
> different Vault path than they were before the escaping was introduced, and are not found by
> the new code. Vault-backed deployments that hold identity or wallet data written by an earlier
> version have to re-register the affected identities (or migrate the paths) after upgrading.

### Iteration

`GetByPartialCompositeID` lists the keys under a partial composite key and returns an iterator
that reads each key's value from Vault as it advances — Vault's KV v1 API has no multi-read
endpoint, so this is one round trip per key. Two properties matter to callers:

* The list is taken when the iterator is created. A key **deleted between the list and its read
  is skipped**, not yielded as a zero-valued entry; a caller reading records out of the iterator
  can therefore trust every entry it receives.
* A prefix with nothing under it yields an **empty iterator**, never `nil`, so callers can
  iterate without a nil check.
