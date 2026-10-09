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

A prefix scan returns **every** entry below the prefix, at any depth, so a caller that wants one
of the four `walletDB` rows above has to say which. `GetConfID` and `GetWalletIDs` both filter by
attribute count — 5 for a `configid` entry, 3 for the wallet id — because the subtree they scan
also holds the `meta` blob and the other wallet reference, and neither is a wallet id or a
configuration id.

## Write Atomicity

The KVS abstraction has no multi-key transaction, so a store operation that needs several keys
writes them one at a time. `WalletStore.StoreIdentity` writes up to four:

* every write is **idempotent** — repeating the call rewrites the same values; and
* the entry `IdentityExists` reads (`tmsID, roleID, idHash, wID`) is written **last**.

A failure part-way therefore leaves the identity reported as *not* bound, so the caller's retry
converges on the complete binding and a partially applied sequence is never mistaken for a
finished one.

`IdentityExists` returns `(bool, error)`. An error means the lookup itself failed and the answer
is unknown — it must not be read as "the binding does not exist".

The entry is read with `KVS.Get`, not probed with `KVS.Exists`. FSC implements `Exists` as
`len(GetExisting(...)) > 0`, and `GetExisting` drops the underlying store error, so a transient
failure would masquerade as "not bound" — the very confusion the `error` return exists to remove.
`Get` is the only method on the KVS surface that propagates the store error, so absence is
classified from the error instead: a "not found" error is an authoritative miss, every other error
reaches the caller. `GetWalletID` resolves the same problem the same way.

FSC has no absence sentinel yet, so a backend signals "not found" only through its error message
and the classification has to match on it. The in-tree KVS (memory, sql) answers
`state [<ns>,<id>] does not exist`; the Vault backend's released module answers
`state of id [<id>] does not exist`. The match is **anchored at both ends** rather than searching
for the `does not exist` substring, because a store failure can carry that very phrase: a missing
or not-yet-migrated table makes Postgres answer `relation "kvs" does not exist`. A backend returns
its absence error unwrapped, so an authoritative miss *starts* with the sentinel, whereas a failure
always arrives wrapped in `failed retrieving state [...]` and can never match it, whatever its
cause says.

The anchoring ties the match to those two exact wordings, which makes it fragile in one direction
only, deliberately. A backend that reworded its absence error would have its misses propagated as
hard errors: `GetWalletID` would fail for an unbound identity and the role `Registry` would abort
the wallet creation — loudly, and recoverably. Loosening the match to avoid that trades it for the
opposite failure, a `("", nil)` answer for an unreadable store, which is the duplicate-wallet bug
of [#2063](https://github.com/LFDT-Panurus/panurus/issues/2063). So the match is never widened,
only replaced once FSC exposes a typed sentinel. `TestIsNotFoundErrMatchesLiveBackend` classifies
the error the in-tree KVS really returns, so a rewording upstream fails in CI rather than in a
deployment.

Absence has a second shape, and it is the only one the Vault backend now uses: a missing id is
reported as a `nil` error with the destination left untouched (see below). The entry holds the
wallet id itself, so an empty value also means "not bound"; an empty wallet id is not representable
here anyway, since `GetWalletID` already reserves `""` for "no binding". The Vault head stays in
the anchored list regardless, because that backend is a module of its own, versioned independently
of this one, so a deployment can still pair this store with a release that returns the message.

## HashiCorp Vault Backend

The Vault backend maps a composite key onto a Vault path under the configured mount point: one
path component per key component. All Vault I/O goes through the client's `*WithContext` calls
(`ReadWithContext`, `WriteWithContext`, `DeleteWithContext`, `ListWithContext`), so a cancelled
or expired caller context aborts the request in flight.

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
>
> No in-place migration tool ships with this change. The previous mapping replaced every `\x00`
> delimiter with `/` and escaped nothing, so it was not injective: a stored path such as
> `<mount>/a/b/c` does not say whether it was written under `["a","b","c"]` or `["a/b","c"]`.
> Recovering the original keys therefore means reconstructing them from the known key layout
> above rather than reading them back off the paths. The backend is a separate Go module with no
> in-tree consumer, so it is opt-in and versioned independently of the SDK, which is why the
> breakage is documented here rather than gated behind a format flag.

### Iteration

`GetByPartialCompositeID` lists the keys under a partial composite key and returns an iterator
that reads each key's value from Vault as it advances — Vault's KV v1 API has no multi-read
endpoint, so this is one round trip per key. Four properties matter to callers:

* The scan **walks the whole subtree**, not just the immediate children. Vault's list is
  single-level and reports a path that has children as an entry of its own with a trailing `/`,
  so listing one level returns directory markers — none of which holds a value. The entries a
  caller scans for usually sit deeper: `GetConfID` scans `walletDB/<tmsID>` for entries keyed by
  `tmsID, roleID, idHash, wID, configid`, four components below it. The walk is iterative, and a
  path that is both a leaf and a directory is returned once.
* The list is taken when the iterator is created. A key **deleted between the list and its read
  is skipped**, not yielded as a zero-valued entry; a caller reading records out of the iterator
  can therefore trust every entry it receives. A directory that disappears mid-walk is likewise
  skipped rather than reported as an error.
* A path that holds **no value of this backend's** is skipped on the same footing. Every shape
  Vault has for "nothing of ours is here" is one answer — no secret at the path, a secret with no
  data, and a secret whose `data` field is missing, empty or not a map — and `Get` reports all
  three as a miss (`nil` error, destination untouched), `Exists` as absent, and a scan by skipping
  them. KV v1 has a single mount per writer-set, so an entry written under it by an operator or
  another application is reachable by any scan over that mount; answering one of those shapes with
  an error instead aborted the iteration, and a single foreign entry under the prefix hid every
  real entry below it from `GetConfID` and `GetWalletIDs`. A secret that *does* carry a `data` map
  is this backend's own, so a `value` inside it that is missing, not a string or not decodable
  stays an error — that entry is corrupt, not absent.
* A prefix with nothing under it yields an **empty iterator**, never `nil`, so callers can
  iterate without a nil check.
* `HasNext` is **idempotent**: it memoizes its lookahead, so calling it twice before `Next`
  neither repeats the Vault read nor drops the key it advanced to.
* Entries are yielded in ascending order of their **composite key**, which is the order the SQL
  backends' prefix scan uses, so the two are interchangeable for a caller that depends on scan
  order. The sort is applied to the composite keys, not to the Vault paths they were listed from:
  a path joins the components with `/` where a composite key separates them with `\x00`, and
  components that need it are percent-escaped, so the two forms do not order alike. Under
  `order`, the attributes `["a","b"]`, `["a","z"]`, `["a-b"]` and `["a/b"]` scan in that order,
  where their paths (`order/a/b`, `order/a/z`, `order/a-b`, `order/a%2Fb`) sort as `a%2Fb`,
  `a-b`, `a/b`, `a/z`.
