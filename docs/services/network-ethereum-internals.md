# Network Service - Ethereum Driver Internals

This page is for someone extending or debugging the Approach-2 Ethereum driver
([`x/token/services/network/evm`](../../x/token/services/network/evm)) rather than operating it. It covers
the derivations, invariants and traps that the code depends on but that don't show up when reading any
single file. For the architecture and the contract walkthrough, start with the
[Ethereum implementation guide](./network-ethereum.md); for standing up a network, see the
[deployment runbook](./network-ethereum-deployment.md).

## Scope

Both shipped token drivers, `fabtoken` and `zkatdlog/nogh`, are supported. Both are **graph-revealing**
today (`IsGraphHiding() == false`, `GetSerialNumbers() == nil`); the graph-hiding path described throughout
this page is fully specified in the contracts and the translator, but dormant until a graph-hiding driver
ships. Each TMS gets exactly one `TokenState` clone, and one Ethereum transaction carries one `StateDelta`
(the translation of a whole token request, which can itself carry many operations).

Out of scope for this driver: cross-chain interop, contract upgradeability (the minimal-clone deploy path
gives cheap per-TMS deployment without introducing an upgrade path), ERC-4337 batching or gas sponsoring,
state-delta compression, and a graph-hiding token driver (the contract and translator support the mode; no
such driver exists yet).

## Trust model

The chain performs no token validation. `TokenState.applyStateDelta` checks only that a threshold of
authorized endorsers signed the exact delta being applied, that the public-parameters version is current,
and that the declared spends are legal (exist-and-unspent or not-yet-used, depending on mode). It does not
verify zero-knowledge proofs, value conservation, or issuer authorization — that happens entirely off chain,
in the endorser quorum. Security therefore reduces to endorser honesty and key custody; see
[Where the work happens](./network-ethereum.md#where-the-work-happens) for the walkthrough and
[Security considerations](#security-considerations) below for what follows from it.

## Roles

- **Initiator** — the FSC node assembling the transaction: builds the token request, drives endorsement
  collection over FSC sessions, assembles and signs the Ethereum transaction, tracks finality.
- **Endorser** — an FSC node that validates the request in Go, translates the validated actions into a
  `StateDelta`, and signs it with EIP-712. Identified by both an FSC `view.Identity` (routing) and an
  Ethereum address (on-chain recovery).
- **Submitter** — the account that signs and pays gas for the Ethereum transaction. May reuse an endorser
  key or be a separate, dedicated account.
- **Contracts** — `EndorsementVerifier` (endorser set, threshold, signature verification) and `TokenState`
  (token storage, spent/existence, public-parameters versioning, state application).

## How it compares to the other drivers

| Concern | Fabric | FabricX | EVM (this driver) |
|---|---|---|---|
| Validation | chaincode on-chain | FSC off-chain + endorse | FSC off-chain + endorse |
| Backend artifact | RWSet | RWSet (+ read deps) | typed `StateDelta` |
| Endorsement transport | Fabric endorser protocol | FSC views | FSC views |
| Spent semantics | delete/SN keys | delete/SN keys + MVCC read-set | spentRefs list + explicit on-chain checks |
| Re-validation at commit | MVCC | MVCC | the contract does it explicitly — there is no read-set |
| Finality signal | push committer | notification queue | receipt + `eth_getTransactionByHash` polling, primarily; |

The EVM driver follows FabricX structurally — off-chain validation, on-chain endorsement check — but emits a
`StateDelta` instead of an RWSet and replaces MVCC re-validation with the explicit checks in
`applyStateDelta` (see [The checks, in order](./network-ethereum.md#the-checks-in-order)).

## Package layout

```
x/token/services/network/evm/
├── driver.go                # DI constructor + registration
├── network.go                # driver.Network implementation
├── ledger.go                 # driver.Ledger adapter (read-only, eth_call at the configured block tag)
├── envelope.go                # driver.Envelope (Bytes/FromBytes/TxID/String)
├── config.go                 # configuration + validation + defaults
├── errors.go                  # sentinel errors and their permanent/transient classification
├── txid.go                    # ComputeTxID and the anchor derivation
├── submitter.go               # signs and submits the Ethereum transaction
├── recovery.go                # the SDK recovery manager wiring, settledNetwork, and its conflictWatch
                              #   tracker (evidence-based invalidation ahead of the age gate)
├── nonce.go, keystore.go, membership.go, policy.go   # signing identity, nonce tracking, local membership
├── client/                    # EVMClient interface, JSON-RPC implementation, local Address/Hash types
├── crypto/                    # keccak256 and SHA-256 primitives
├── eip712/                    # domain separator, type hashes, digest, secp256k1 signer/verifier
├── keys/                      # the KeyTranslator that derives on-chain object keys
├── statedelta/                # the StateDelta type and the translator from validated actions to it
├── endorsement/                # ServiceProvider (lazy per TMSID), initiator + responder views, identity registry
├── finality/                  # the finality manager and log-scanning resolver
├── pp/                        # the public-parameters version keeper and provider
├── nwo/                       # integration-test topology helpers (setup, config rendering)
├── abi/                       # ABI encoding for applyStateDelta calls
└── contracts/                 # Solidity sources, ABI, deploy scripts
```

## Key derivations

The [`keys`](../../x/token/services/network/evm/keys/keys.go) package is the single source of truth for
on-chain object keys, shared by the initiator, the endorsers, and reproduced independently by the contract.

| Function | Derivation | Used for |
|---|---|---|
| `ComputeTokenID(anchor, index)` | `keccak256(abi.encode(anchor, index))` | the addressable token storage key, used by queries |
| `OutputSNMarker(anchor, index, tokenData)` | `keccak256(abi.encode(anchor, index, keccak256(tokenData)))` | the content-bound graph-revealing spend reference |
| `SpentRefForSerial(serial)` | `keccak256(0x03 ‖ serial)` | the graph-hiding spend reference |
| `IssueMetadataKey(subkey)` | `keccak256(0x01 ‖ subkey)` | an issue metadata key |
| `TransferMetadataKey(subkey)` | `keccak256(0x02 ‖ subkey)` | a transfer metadata key |
| `AnchorFromTxID(txID)` | decode hex-32 | txID string to `bytes32` anchor |

Class separation uses fixed one-byte prefixes rather than ad-hoc strings, so the derivations stay collision-free
by construction.

**Why `OutputSNMarker`, not `ComputeTokenID`, is the graph-revealing spend reference.** The SDK validator is
stateless: the fabtoken transfer validator, for example, sets its input tokens from the action's own inputs
(`validator_transfer.go`) and checks balance against those — it never loads the real on-chain token to
compare. A spend reference keyed only on `(anchor, index)` would therefore let a spender present forged
token bytes at a real position and have them accepted, because nothing on the validation path re-reads the
real content. `OutputSNMarker` binds the token's content at creation time, mirroring how the Fabric
translator commits an output's serial-number key: the marker is recorded when the output is written, and a
spend must reference that exact marker, so forged content simply never matches a recorded one.
`ComputeTokenID` stays the addressable storage key, independent of content, so `QueryTokens` can still
resolve a `token.ID` to its bytes.

## Hashing: keccak256 vs SHA-256

| Value | Algorithm | Why |
|---|---|---|
| token ID, spend reference, metadata key, EIP-712 digest and structHash | keccak256 | EVM-native, cheap on chain |
| `tokenRequestHash` | SHA-256 | matches what the rest of the Token SDK stores and compares |
| `publicParamsHash` | SHA-256 | matches the SDK's `Hashable` public-parameters hash |

If the contract ever needs to reproduce one of the SHA-256 values itself, it reaches for the SHA-256
precompile at address `0x02` rather than a Solidity implementation.

## The anchor, and `ComputeTxID`'s mutating contract

The anchor — `StateDelta.anchor` — is `SHA-256(len(nonce) ‖ nonce ‖ creator)`, hex-encoded. It is
deliberately **not** the Ethereum transaction hash: a contract has no way to read its own transaction hash
(there is no opcode for it), so identifying transactions by anchor is the only option that works from inside
`applyStateDelta`.

`ComputeTxID` has a contract that is easy to miss reading the signature alone: it is called with a `TxID`
whose nonce field is expected to be **empty**, and it fills the nonce **in place** on the caller's struct
before hashing, generating a fresh cryptographically random nonce if none is set. A pure, non-mutating
implementation would derive the same anchor for every transaction a given creator submits, and the second
transaction ever sent by that creator would revert with `AnchorAlreadyProcessed` — a bug invisible in a
single-transaction demo and fatal in production. `txid.go` is the only place this contract has to be
honoured; every caller elsewhere relies on it already having been.

## StateDelta determinism

Every endorser must independently derive byte-identical `StateDelta` bytes for the same token request, or
their signatures verify against different digests and the initiator sees a shortfall it cannot explain.
Two sources of nondeterminism have to be canonicalized:

- **Metadata.** `metadataKeys`/`metadataVals` come from a Go map with random iteration order at the source.
  The translator sorts by key ascending and keeps `metadataVals` aligned to the sorted order.
- **Outputs and spend references.** These are emitted in deterministic action/counter order, reproducing the
  Fabric translator's own counter exactly: an issue action's counter advances by `len(outputs)`; a transfer
  action's advances by the action's total output count **including redeem slots**; redeem outputs themselves
  are skipped when building the delta's `outputs` list (a redeem has no new output token to write), but they
  still consume a counter position.

This is the most-cited rule in the codebase — the translator, its tests, and every endorsement round-trip
test depend on the two sides landing on the same bytes.

## From validated actions to a StateDelta

The translator ([`evm/statedelta`](../../x/token/services/network/evm/statedelta)) consumes already-validated
actions, not the raw token request — the same action objects the Fabric RWSet translator consumes.

| Action | Effect on the delta |
|---|---|
| `SetupAction` | `isSetup = true`, `setupParameters` set from the action; public-parameters hash/version set; spend references and outputs stay empty |
| `IssueAction` | appends the issued outputs and their metadata; appends to spend references only if the action itself declares inputs |
| `TransferAction` | appends non-redeem outputs (redeem outputs are skipped, per the counter rule above); appends spend references from the action's inputs (graph-revealing) or serial numbers (graph-hiding) — exactly one of the two is ever populated for a given driver; appends metadata |

## Spent and existence, and why the modes differ

Fabric's own translator uses opposite polarities for the two modes it supports, and the EVM contract
inherits both:

- **Graph-revealing** (`fabtoken`, `zkatdlog/nogh`): an output is recorded at creation under both its token
  ID and its content-bound marker (`OutputSNMarker`). A spend must reference a marker that **exists** and is
  **unspent** (`snExists[ref] && !snSpent[ref]`); spending sets `snSpent[ref] = true`. Spent is presence-then-flag.
- **Graph-hiding**: a spend reference is a serial number that must **not** already exist
  (`!serialUsed[ref]`); spending writes it (`serialUsed[ref] = true`). Spent is presence, full stop — there is
  no separate existence check because a graph-hiding driver never records an output's serial number at
  creation.

Because each `TokenState` clone serves exactly one token driver, the mode is fixed for the life of the
contract (the `graphHiding` flag, set at `initialize` from the public parameters) and `StateDelta` carries a
single `spentRefs` list whose interpretation the contract branches on — there is no need for two parallel
lists.

One consequence worth knowing before debugging a graph-hiding integration: the token-ID query surface
(`isSpent`, `areTokensSpent`) is meaningless for a graph-hiding clone, since spend state there is keyed by
serial number, not token ID — both calls **revert** with `UnsupportedForGraphHiding` rather than returning a
value that would be wrong. Use `isSerialUsed` instead.

## Public parameters: binding and block tag

An endorser's `DeltaFactory` (`evm/endorsement/delta.go`) touches public parameters from two distinct
sources, and both distinctions matter enough to have caused real bugs:

- **What it validates with vs. what it signs.** `Build` validates the request with the TMS's own
  `token.PublicParametersManager` (`f.validator`, resolved by `esp.go`'s `TMSResolver`), but the
  `StateDelta` it produces is stamped with parameters read fresh from the chain (`f.pp`, a
  `pp.ChainProvider`). Those two can disagree: an endorsed setup delta updates the contract
  immediately, but this node's `pp.Watcher` only applies the new parameters locally on its next poll
  (see `driver.go`'s documented "keep serving the old TMS" tradeoff). Signing regardless would produce
  a delta whose `PublicParamsHash` asserts this endorser validated under parameters it never actually
  used. `Build` therefore cross-checks `SHA-256(chain bytes)` against
  `f.localPP.PublicParamsHash()` before doing anything else, and refuses with `ErrStalePublicParams`
  on a mismatch rather than signing a false statement. This costs the requester a retry; it never signs
  a lie.
- **Which block tag the chain read uses.** `TokenState.applyStateDelta` enforces
  `publicParamsVersion`/`publicParamsHash` against its **current (head)** storage, not against what is
  finalized. `driver.go` therefore constructs the endorsement path's `pp.ChainProvider` at
  `client.BlockTagLatest`, explicitly, regardless of `Finality.BlockTag` — reading it at `finalized`
  instead would mean every endorsement signed in the finalization lag after a setup update reverts
  `StalePublicParams` on chain, since the endorser would keep seeing the pre-update pair the whole time.
  `TMSConfig.BlockTag` (which feeds `endorsement/ledger.go`'s token-existence reads, and `pp.Watcher`'s
  own polling) is a separate value and correctly stays at `Finality.BlockTag`: a reorg there risks a
  double spend, which the pp read does not, so the two uses are allowed to differ and are kept
  independent by construction (each is a distinct constructor argument, not a shared default).

## Signing: byte formats that bite

- **Address derivation.** An endorser or submitter's Ethereum address is
  `keccak256(uncompressed pubkey with the leading 0x04 byte stripped)[12:]`. The uncompressed
  serialization is 65 bytes starting with `0x04`; hashing all 65 bytes produces a wrong but entirely
  plausible-looking address, so the leading byte must be stripped first.
- **Signature wire order.** `decred/secp256k1`'s `ecdsa.SignCompact` returns a 65-byte signature with the
  recovery byte **first** — `{v, r, s}`, always signing with `compressed=false` so `v` lands in `{27, 28}`.
  The Ethereum/contract wire format is `{r, s, v}` with `v` last, so the signer reorders before returning.
  dcrd signatures are canonical (low-s) by construction, but the signer asserts low-s anyway, so a future
  library change can't silently reintroduce malleable signatures.
- **The nonce manager** tracks each submitter's next nonce explicitly, with an `initialized` flag rather than
  treating `nonce == 0` as "uninitialized" — zero is a legitimate first nonce. `RecoverNonce` re-syncs from
  the node's pending-nonce view and is wired into the retry path. A submitter address shared across
  processes needs external coordination: the node's own pending-nonce view does not see nonces another
  process has reserved locally but not yet broadcast.

## No go-ethereum, even transitively

The build must never link `go-ethereum`, even as a transitive dependency — its license is a hard blocker.
The driver uses `x/crypto/sha3` and `decred/secp256k1` plus local `Address`/`Hash` value types and a small
hand-rolled RLP encoder for the EIP-1559 transaction envelope, instead of `go-ethereum/core/types`.
`depguard_test.go` enforces this in CI. The operator-facing side of the same constraint — that the driver
therefore speaks plain JSON-RPC and works against any EVM node — is covered in
[Node compatibility](./network-ethereum.md#node-compatibility).

## Security considerations

- **Endorser key custody is the primary control.** Since the chain performs no token validation, everything
  in [Trust model](#trust-model) above ultimately rests on endorser keys being held and rotated safely.
- **Replay** is blocked two ways: the EIP-712 domain separator binds the chain id and the specific
  `TokenState` clone address, so a signature gathered for one TMS cannot be replayed against another or on a
  different chain; and `processedAnchor` prevents the same anchor from being applied twice.
- **Front-running is accepted as benign.** Signatures aren't bound to a specific submitter address, so
  anyone holding a valid, threshold-signed delta can broadcast it. The effect is harmless: it applies the
  same valid transition the original submitter intended, and the original submitter's own transaction then
  reverts on `AnchorAlreadyProcessed` rather than double-applying anything.
- **Metadata size** is bounded at the approver, since driver-specific metadata (zkatdlog's, in particular)
  is untrusted and can be large.
- **Reorgs** are avoided by reading state at the `finalized` block tag rather than handling reorg recovery
  in v1.
- **The audit surface** is `EndorsementVerifier` (`ecrecover`, low-s enforcement, signer uniqueness) and
  `TokenState` (atomicity of `applyStateDelta`, and that only a fresh clone can ever be initialized).

## See Also

- [Ethereum implementation guide](./network-ethereum.md) - the two approaches, the contract walkthrough, and operational behaviour
- [Ethereum Deployment Runbook](./network-ethereum-deployment.md) - standing up a TMS with this driver
- [Network Service](./network.md) - the driver interface being implemented
