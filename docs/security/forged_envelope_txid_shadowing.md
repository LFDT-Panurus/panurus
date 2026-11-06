# Forged-Envelope txID Shadowing

This page describes why a point lookup of a transaction's ledger status can return a forged
envelope's verdict instead of the honest one's, the single rule Panurus applies to resolve it,
and where that rule is enforced.

## The problem

A Fabric block can carry several envelopes with the **same transaction ID**. Within a block,
Fabric records a txID for duplicate detection only when its envelope validates as `VALID`
(`markTXIdDuplicates` in Fabric's `core/committer/txvalidator`), so a block holds **at most one
`VALID` envelope per txID**.

Any party that learns a txID before it is ordered — for instance a counterparty in the ttx
protocol — can sign an envelope carrying that txID with its own identity and have it ordered ahead
of the honest one. The peer flags the forgery `BAD_PROPOSAL_TXID`, and the honest envelope later in
the same block is still `VALID`. The attacker spends only one correctly signed envelope; no orderer
or peer compromise is needed (issue #2647).

The hazard is that several Panurus paths picked the *first* envelope carrying a txID:

- **Point lookups.** `ledger.Status` and `ledger.GetTransactionStatus`
  ([`token/services/network/fabric/network.go`](../../token/services/network/fabric/network.go))
  call `Ledger.GetTransactionByID`, which returns the lowest `(block, txNum)` entry for a txID —
  the forgery — and maps its non-`VALID` code to `Invalid`.
- **TTX recovery.** `TTXRecoveryHandler.Recover`
  ([`token/services/ttx/finality/recovery.go`](../../token/services/ttx/finality/recovery.go))
  consumes `GetTransactionStatus`, so the forgery's `Invalid` made it set the transaction to
  `storage.Deleted` — removing from the wallet tokens that exist on the ledger, a deletion that
  rerunning recovery could not fix because the point lookup kept returning the forgery.
- **Delivery finality & lookup.** The per-envelope mappers in
  [`finality/deliveryflm.go`](../../token/services/network/fabric/finality/deliveryflm.go) and
  [`lookup/deliveryllm.go`](../../token/services/network/fabric/lookup/deliveryllm.go) mapped each
  envelope with its own flag; because the listener manager invokes a txID's listeners per event in
  block order, a listener received the forgery's `Invalid` event first.
- **Evicted-listener query.** `DeliveryScanQueryByID.queryByID`
  ([`finality/deliveryqs.go`](../../token/services/network/fabric/finality/deliveryqs.go)) resolves
  evicted listeners with `GetTransactionByID`, inheriting the point-lookup problem.

A forgery ordered in an **earlier** block is out of scope: Fabric's ledger index records every txID
whatever its flag, so Fabric flags the honest envelope `DUPLICATE_TXID` on every peer and the
transaction is invalid everywhere. Only a forgery in the **same block** as the honest envelope
shadows a committed transaction.

## The rule

Everywhere a txID's verdict is derived from a block, Panurus applies one rule, matching
fabric-smart-client's committer:

> The verdict of a txID in a block is that of its `VALID` envelope if the block has one, and
> otherwise that of its first envelope.

## Where it is enforced

The rule lives in
[`token/services/network/fabric/finality/shadow.go`](../../token/services/network/fabric/finality/shadow.go),
in two forms for the two ways Panurus consumes a block:

| Helper | Used by | What it does |
| --- | --- | --- |
| `SkipShadowedEnvelopes(block)` | The delivery scan (`finality.Delivery.ScanBlock`) | Returns the block with every endorser envelope Fabric found invalid whose txID a `VALID` endorser envelope in the same block also carries removed, with its aligned `TRANSACTIONS_FILTER` byte. |
| `ValidEnvelope(block, txID)` / `HonestEnvelope(ctx, ledger, delivery, txID)` | Point-lookup callers (`ledger.Status`, `ledger.GetTransactionStatus`, `DeliveryScanQueryByID`) | Resolve the one envelope whose verdict is the real outcome of a txID. |

Concretely:

1. **Delivery path (both mappers).** `finality.Delivery.ScanBlock` wraps every delivered block with
   `SkipShadowedEnvelopes` before the per-envelope mappers see it. Because both the finality
   listener manager and the lookup listener manager scan through this one `Delivery`, each txID
   yields a single, correct event per block from one place.
2. **Point lookups.** When `GetTransactionByID` returns a non-`VALID` envelope, `ledger.Status` and
   `ledger.GetTransactionStatus` resolve the honest envelope with `HonestEnvelope`: it reads the
   transaction's block number (`GetBlockNumberByTxID`) and scans **exactly that one block** for a
   `VALID` envelope with the same txID, using its verdict (and token request hash) when one exists.
   Only that block is scanned, because an honest envelope in any later block is always
   `DUPLICATE_TXID`.
3. **TTX recovery** needs no change of its own: it is fixed because `GetTransactionStatus` now
   reports the honest verdict.
4. **Evicted-listener query** (`DeliveryScanQueryByID`) applies the same `HonestEnvelope`
   resolution when a point lookup maps to `Invalid` and its block-resolution dependencies are
   wired.

## A note on the FSC dependency

fabric-smart-client fixes its own committer (`hyperledger-labs/fabric-smart-client#2480`) and
delivery-based finality (`#2482`). Panurus's pinned FSC release does not yet export a shared rule,
so `shadow.go` is a local helper that mirrors FSC's `skipShadowedEnvelopes`. Once Panurus depends
on an FSC release that exports the rule, these helpers can defer to it.

## Tests

- `shadow_test.go` — `SkipShadowedEnvelopes` / `ValidEnvelope` / `HonestEnvelope` across the
  forged-then-honest, honest-then-forged, no-valid-envelope, distinct-txID, config-never-shadows,
  and malformed-block cases.
- `delivery_test.go` — the delivery scan strips shadowed envelopes before the callback.
- `deliveryqs_test.go` — an evicted listener's forged txID resolves to the honest verdict.
- `recovery_test.go` — a shadowed-forgery transaction stays `Confirmed`, not `Deleted`.
- `shadow_fuzz_test.go` (`FuzzSkipShadowedEnvelopesNoPanic`, wired into
  `.github/workflows/nightly-fuzz.yml`) — no block bytes panic the filter.

