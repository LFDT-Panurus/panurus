# Owner Wallet Key Store Growth

This page explains how the key store of an Idemix owner wallet grows (for example, with the
`zkatdlog` driver).

## What the Key Store Holds

For each Idemix owner wallet, the key store holds:

*   **One user secret key.** The key manager imports it when it loads the wallet
    (`token/services/identity/idemix/km.go`).
*   **One nym secret key per generated pseudonym.** `KeyManager.Identity` derives it with
    `Temporary: false`, so the key store persists it at generation time, not at first use
    (`token/services/identity/idemix/km.go`).

## The Pseudonym Cache

One lazy, in-memory cache pre-generates pseudonyms for an anonymous owner wallet:

| Cache | Backend | Writes a `Signers` row |
|:------|:--------|:-----------------------|
| `role.RecipientDataCache` (`token/services/identity/role/cache.go`) | `AnonymousOwnerWallet.getRecipientIdentity`, which calls `KeyManager.Identity` and registers the identity descriptor | Yes |

It takes its size from `wallets.owners[].cacheSize`, falling back to
`wallets.defaultCacheSize`. Call this size `N`.

The cache holds `N + 1` entries:

*   The buffered channel holds `N` entries.
*   The provisioning goroutine generates one more entry and blocks while it sends it.

Provisioning is lazy. It starts on the first request for recipient data. With `N = 0`, no
goroutine starts and every request goes to the backend.

## Growth Formula

For `N > 0`:

```
keys = usk + used pseudonyms + (N+1) per process run that uses the wallet
```

*   `usk` is the user secret key (one per wallet).
*   Each used pseudonym is replaced in the cache by a new one, so it adds one key.
*   Cached entries live only in memory. A restart loses them, but their keys stay in the key
    store. The next run that uses the wallet fills the cache again.
*   A restart that does not use the wallet adds no keys.

Every pseudonym key has a `Signers` row, so `cmd/skicleanup` lists all of them as orphans.

> **Warning:** do not delete the keys of "orphan" SKIs while the node runs. Some of these
> SKIs belong to pseudonyms that still wait in the live `RecipientDataCache`. The wallet can
> later hand out such a pseudonym, and signing for it then fails. Delete keys found with
> `skicleanup` only while the node is stopped.

## Measuring It

The dlog integration test labeled `T18` (`integration/token/fungible/dlog`) measures this
growth. It issues to a dedicated owner wallet, transfers, restarts the node, and transfers
again. After each step it compares the key store and `Signers` statistics with the formula
above. Use it as a benchmark for changes that reduce key consumption.

Run it with:

```bash
make integration-tests-dlog-fabric-t18
```

## See Also

*   [Identity Service: Wallet Lifecycle and Recipient Data Caching](identity.md#wallet-lifecycle-and-recipient-data-caching)
*   [Keystore Cleanup Service](storage/keystore_cleanup.md)
*   [skicleanup](../../cmd/skicleanup/README.md)
