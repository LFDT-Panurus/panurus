# Store Integrity Verification

The store services persist the payloads that Panurus later treats as evidence: the token request a
transaction is re-validated against, the public parameters it is validated *under*, the identity a
signer is bound to, the acknowledgement that a party endorsed a transaction. This page states, per
asset class, **what a store verifies, what it requires of its caller, and what it deliberately does
not check** — so that a reader of a store method knows which of the three applies without reading
its implementation.

The checks themselves live in one backend-agnostic package,
[`token/services/storage/integrity`](../../token/services/storage/integrity), and each store method
that applies one carries a `Verification:` clause in its Godoc naming it. Both are greppable on
purpose: `grep -rn "Verification:"` enumerates the contract, and `grep -rn integrity.Check`
enumerates the enforcement.

## Scope

This is a **structural** integrity boundary, not a second validator. The checks need only the bytes
themselves plus the key those bytes are stored under; none of them performs I/O, consults a ledger,
or evaluates a zero-knowledge proof. A token request is fully verified only by a `token.Validator`
against a ledger, and that happens where it already happened — see
[Endorsement responder security](../services/ttx/ttx_responder_security.md).

What the boundary buys is that a payload which **could not have been produced by a correct caller**
is refused rather than stored, and a payload which **could not honestly be attributed to the row it
was found in** is reported rather than returned. The failures it catches are systemic ones: a
truncated or garbled blob, a row swap, a wrong-row read on a hash-addressed table, a write that
skipped the validating path, an empty value standing in for a real one.

Out of scope: trust-chain and revocation validation, semantic validation of actions, and anything
requiring a network round-trip from inside a store call.

## Asset classes

| Asset | At insert | On retrieval | Caller must have done |
|---|---|---|---|
| Token request (`ttxdb`, `auditdb` — `TokenRequestWithMetadata`) | Non-empty `tx_id`, non-empty request, non-empty `pp_hash` | Deserializes, declares a supported protocol version, and its **anchor equals the `tx_id` it is filed under** | Serialized it from a live `token.Request` (the marshal round-trip is what makes the insert-side re-parse redundant) |
| Token request (`endorserdb` — bare actions and signatures) | Non-empty `tx_id`/request/`pp_hash`, deserializes at a supported version, **carries at least one action** | — (this format has no anchor; see below) | Validated it — `validator.UnmarshallAndVerifyWithMetadata`, and taken `pp_hash` from the *local* TMS, not from the peer |
| Public parameters (`tokendb`) | Hash computed by the store from the bytes it is storing | **Recomputed and compared against the hash the row is filed under** | — |
| Identity (`identitydb`, both backends) | Non-empty | Non-empty, and the stored identity **compared against the requested one** (`GetAuditInfo`, `GetTokenInfo`, and `GetSignerInfo` on SQL) | Matched it against its audit info, and — at `wallet.Service` — established that an owner verifier can be derived from it |
| Signer registration (`token.SignatureService`) | Identity non-empty **and** a verifier derivable from it | — | — |
| Endorsement acknowledgement (`tx_ends`) | Non-empty endorser, non-empty signature | — (the signed message is not persisted; see below) | **Verified `sigma` against the exact payload it sent to that party** — `CollectEndorsementsView.distributeTxToParty` does |
| Movements, transaction records, token locks, application/public metadata, audit info blobs, `IdentityConfiguration` raw | — | — | Everything: these are stored as given |

The last row is a deliberate posture, not an omission. These classes are high-volume, individually
low-value, and — decisively — have **no cheap self-consistency predicate**: there is no field in a
movement row that a structural check could disagree with. Adding a check with no predicate to
evaluate would cost write throughput and catch nothing.

## Requirements

1. **Callers of `endorserdb.AppendValidationRecord` MUST validate the token request first**, and
   MUST take `pp_hash` from their own TMS rather than from the peer that sent the request. The store
   checks that the payload is a deserializable request with at least one action; it cannot check
   that the actions are legal.
2. **Callers of `AddTransactionEndorsementAck` MUST verify the signature** against the payload they
   sent to that endorser, before storing. The store checks only that an endorser and a signature are
   present.
3. **No caller may pass an empty identity** to the identity store. Every write and read path
   refuses one with an error, with one deliberate exception: the existence queries
   `GetExistingSignerInfo` and `SignerInfoExists` exclude an empty identity from the lookup and
   report it as not existing, so that the shared `<empty>` key is still never consulted while a
   batch containing one empty entry still answers for the caller's other identities. See the next
   section for why this is a correctness requirement and not a tidiness one.
4. **The checks MUST remain unconditional.** No functional option, no setter, no configuration key,
   no build tag may turn one off. This is enforced by
   [`nobypass_test.go`](../../token/services/storage/integrity/nobypass_test.go), which reads the
   source of every package applying a check and fails on an identifier named like a bypass, on a
   variadic parameter added to a check, on mutable package-level state in the `integrity` package,
   on a verification-related configuration key, and on a check whose error is discarded.

## What is deliberately not checked, and why

Three of these are structural limits rather than choices, and each one is a place where the obvious
check does not exist to be added.

**`pp_hash` is not a digest of the request.** It is the hash of the *public parameters* the request
was created under. It therefore provides no integrity for the `request` column, and no
retrieval-time "does the stored hash match the stored bytes" check is possible for token requests.
What *is* available, and is what the retrieval-side check uses, is the request's own **anchor**: the
transaction id the request commits to, covered by the signatures inside it. Comparing the anchor
against the `tx_id` the row is keyed by is what ties the bytes to the row they were found in, and it
catches truncation, encoding drift, and row swaps. The bare actions-and-signatures format held by
`endorserdb` carries no anchor at all, so records in that format cannot be bound to their
transaction id by a structural check — only by the caller's validation.

**The message an endorsement acknowledgement signs is not persisted.** `tx_ends` holds
`(endorser, sigma)`. The signed message is the *per-party filtered* transaction payload, different
for each endorser, and it is not stored anywhere. Retrieval-time re-verification of an ack therefore
has nothing to verify against, and adding it requires a schema change — a persisted digest of the
endorsed message — which is tracked separately. Until then, the posture is: verified at insert by
the only producer, contract-documented as not re-verifiable on read. The store does refuse an empty
endorser or an empty signature, which matters more than it looks:
`ttx.TransactionInfo` presents acks as a map keyed by endorser and its consumers do not inspect the
values, so a row with an empty signature reads as "this party signed". On retrieval such a row is
logged at ERROR and **dropped from the returned map** rather than failing the call: the same
`ttx.TransactionInfo` read carries the transaction's valid acks and its token request, there is no
API to delete the offending row, and failing would make the whole transaction permanently
unreadable over one bad row.

**A supplied verifier is not compared against the identity it is registered for.** `driver.Verifier`
exposes only `Verify(message, sigma)` — there is no canonical public key to compare, so establishing
agreement would require a new accessor implemented across every identity type (x509, idemix,
htlc, multisig, boolpolicy). What `token.SignatureService.RegisterSigner` enforces instead is the
substantive, non-tautological part: the identity is non-empty, and a verifier can be derived from it
by this driver. That rules out binding a signer to bytes no verifier can be built from —
an identity that can sign but whose signatures nothing can check. The owner deserializer is the one
asked, and asking it is enough: every driver in the tree builds `common.NewDeserializer` from a
single `TypedVerifierDeserializerMultiplex`, so the owner, issuer and auditor deserializers are the
same object and route by identity type rather than by role. The comparison itself would be a
tautology for the in-tree callers that pass a verifier at all: the x509 and idemix key managers
derive it from the identity they are registering, and the `ttx` callers pass `nil`.

**The KVS identity backend cannot compare signer identities.** It does not store the identity
alongside the signer info, so `GetSignerInfo` there refuses an empty identity but cannot verify that
the record found under an identity hash belongs to the requested identity. The SQL backend does both.
This asymmetry is recorded on the KVS method itself; the shared spec in
`token/services/storage/db/dbtest` holds both backends to everything they *can* both do.

## Why this is sufficient

The empty-identity guards are the least obvious and the most load-bearing, so they are worth stating
plainly. Identity rows and identity caches are keyed by `Identity.UniqueID()`, and for an empty
identity that function returns the literal string `<empty>` — **not** a hash. Every empty identity
therefore collapses onto one row and one cache entry. Without the guard, one caller's audit info,
token metadata, or signer info is readable by any other empty-identity lookup, and a signer
registered for one is returned for another. This is why the guard sits on ephemeral registrations
too, which write nothing to storage but populate the same caches.

The hash-addressed read paths are the second load-bearing case. `GetAuditInfo`, `GetTokenInfo`, and
`GetSignerInfo` locate a row by `identity_hash`, and `PublicParamsByHash` by `raw_hash` — in each
case the caller names a value, the store looks up a digest of it, and before these checks nothing
compared the row it found against what the caller asked for. Comparing them turns a hash-addressed
read into an authenticated one at the cost of a byte comparison, or one SHA-256 for public
parameters.

For the remaining classes, the argument is that the check is applied where the information exists.
Every insert path either just serialized the payload from an in-memory object or just validated it
in the caller's own scope; re-parsing there would cost time proportional to the payload and learn
nothing. Every retrieval path hands bytes to a caller that is about to treat them as authentic
evidence about a specific transaction, and pays one unmarshal to establish that they are.

## If the requirements are not met

The checks are **fail-closed**: a payload that fails one is not stored, and a row that fails one is
not returned. Retrieval-side failures are logged at ERROR before the error is returned, because they
indicate storage corruption or out-of-band modification rather than a caller mistake, and nothing
downstream will surface them a second time.

A caller that skips its own obligations from the Requirements section above is *not* caught by these
checks — that is what makes them obligations. An unvalidated token request that deserializes and
carries an action passes the `endorserdb` check and is filed as validated; an acknowledgement with a
signature that verifies against nothing passes the `tx_ends` check and is filed as an endorsement.
Both would then be believed by anything reading those stores. The store-level checks narrow what a
skipped verification can look like; they do not substitute for it.

## Related

* [Endorsement responder security](../services/ttx/ttx_responder_security.md) — where token requests
  arriving over the wire are actually validated.
* [Storage Service](../services/storage.md) — the schema these checks apply to.
* [Public Parameters Lifecycle](../public_parameters.md) — why parameters are addressed by hash.
* [Identity Service](../services/identity.md) — identity registration and the deserializer.
* [Storage DB Schema Upgradability](../upgradability.md#storage-db-schema-upgradability) — why a
  persisted digest column is a migration question and not a code change.
