# Ethereum / EVM Network Driver — Implementation Plan (production driver, ~8 weeks)

> Companion to `eth_network_driver_design.md`. Branch `feature/evm-network-driver`, module
> `github.com/LFDT-Panurus/panurus`. Ground zero → a **real, working** driver validated against a real EVM
> backend (**Besu**), not a demo. Target 6 weeks, hard ceiling ~8 weeks with buffer. Grounded in verified SDK
> extension points (§0).

> **Backend decision (Angelo, 2026-07-08):** the acceptance backend is **Besu** — "go ahead with it if it
> makes your life simpler." **fabric-x-evm is a stretch goal** ("if time remains we will check fabricx+EVM"),
> not the bar for done. This flips the finality plan: the **receipt-polling baseline (Wk5) is now the primary
> path** (Besu is a standard EVM node with no fabric-x gateway), and the fabric-x gateway `isPending` lifecycle
> (§7.1, superseded-tx handling) moves into the fabric-x-evm stretch. It also substantially lowers the Wk6 risk
> — Besu has mature Docker/dev-mode tooling, so the NWO bootstrap is no longer "from scratch." Other answers:
> NWO scaffolding stays Wk6 (not now); the admin runbook is a Wk6 deliverable; the EVM Ginkgo suite reuses the
> existing fungible `dlog` test bodies **verbatim**, retargeted at the EVM topology (like `dlogx`), so the
> `evm` topology package must mirror `fabricx`'s public interface exactly.

## 0. Foundation

### 0.1 SDK extension points (verified — the real seams)

| Seam | Interface / signature | Template (copy structure) |
|------|----------------------|-----------------------|
| Driver factory | `driver.Driver = New(network, channel) (Network, error)` | `network/driver/driver.go` |
| The network | `driver.Network` (16 methods) | `network/driver/network.go` |
| Envelope / Ledger | `Bytes/FromBytes/TxID/String` ; `Status/GetTransactionStatus/GetStates/TransferMetadataKey` | `network/driver/{envelope,ledger}.go` |
| Key derivation | `evm/keys` EVM-native `[32]byte` derivations (analog of, not `translator.KeyTranslator`) | `network/common/rws/keys/keys.go` (pattern) |
| Translator | `Write/AddPublicParamsDependency/CommitTokenRequest` (+`StateDelta()`) | `network/common/rws/translator/translator.go` |
| Endorse responder/initiator | FSC views | `network/fabric/endorsement/fsc/{responder,initiator}.go` |
| Endorse provider | lazy `Provider[TMSID, Service]` | `network/fabricx/endorsement/esp.go` |
| PP version | `VersionKeeper` (atomic uint64; first=init, then +1) | `network/fabricx/pp/versionkeeper.go` |
| Finality reuse | `OnlyOnceListener` + event `queue` | `network/fabricx/finality/{finality.go,queue}` |
| DI constructor | `NewDriver(...) *Driver` | `network/fabric/driver.go:119` |
| Registration | `Provide(evm.NewDriver, dig.Group("network-drivers"))` | `integration/.../sdk/fxdlog/sdk.go:55` |
| Driver routing | first driver whose `New` returns no error wins | `network/network.go:412-432` |
| Base SDK | `tokensdk.NewFrom(viewsdk.NewSDK(registry))` (pure view, **no Fabric**) | `viewsdk = platform/view/sdk/dig` |

### 0.2 What "real working driver" means here (no demo)

- **Acceptance backend = Besu**, wired through NWO. anvil/forge is used **only** for the fast inner loop
  (contract unit tests, Go unit tests) — never as the bar for "done." **fabric-x-evm is a stretch** validated
  only if time remains.
- **Full design implemented**: both shipped drivers (fabtoken + zkatdlog/nogh), one-list `spentRefs` +
  contract `graphHiding` flag,
  on-chain checks, EIP-712 endorsement with no blind-signing, on-chain PP versioning **and** the endorsed
  PP-update flow, recipient-side anchor→finality from chain data.
- **Finality, production-correct + robust**: receipt-based finality (polling the receipt + standard
  `eth_getTransactionByHash` block-number) is the **primary** path and works on Besu and any standard EVM node;
  the fabric-x gateway `TransactionByHash().isPending` lifecycle (superseded-tx handling, §7.1) is an
  efficiency layer added **only** for the fabric-x-evm stretch. Read at the `finalized` tag where the node
  exposes it (Besu dev-mode may only expose `latest`; the block-tag is configurable, §10).
- **Genuinely deferred (future scope, not corners cut)**: EIP-1167 clones (deploy optimization), ERC-4337
  (v2 gas/batching), a graph-hiding token driver (none ships today). These are additive and do not change the
  MVP architecture; the ABI/StateDelta frozen in Week 1 already accommodate them.

### 0.2b Working rules (binding for every phase — each traces to a defect actually hit on this project)

| # | Rule | Incident it prevents from recurring |
|---|------|-------------------------------------|
| **R1** | **No undocumented decisions.** Any decision from a call/chat/review lands in the design doc + this plan + root `plan.md` in the *same working session*, dated, with the source named. A decision that isn't written down doesn't exist. | The `snMarker` content-binding decision (Angelo, 03/07) left Week 3 + §5.3 describing the superseded `ComputeTokenID` spend-ref for days — following the stale text would have produced spends the contract rejects. Same class: the Besu backend decision. |
| **R2** | **Freeze discipline.** Frozen artifacts (StateDelta shape, key derivations, EIP-712 encoding) change only via an explicit re-freeze: regenerate the Go golden values, re-validate against ethers, re-run the Solidity suite. **Never hand-edit an expected fixture value.** | Guards the three-way (Go/ethers/Solidity) agreement that everything downstream signs against. |
| **R3** | **Prove, don't assume.** Nothing is "done" until its tests were *executed* (go test / forge test / fmt / vet), and every digest-covered field has (a) a sensitivity mutation and (b) cross-impl coverage for every delta *shape* that will be signed in production. Coverage claims in docs are audited against the test code, not taken from memory. | The "sensitivity covers all 9 fields" claim was false — the struct has 10 and `setupParameters` had no mutation and no cross-impl vector; a dropped field in `HashStruct` would have passed every test and surfaced in Week 7 on-chain. Also: forge assertions (`expectRevert` semantics) were wrong on first write and only caught because the suite was actually run. |
| **R4** | **One source of truth for cross-impl values.** Tests parse expected values (and inputs where practical) from the committed fixture; hardcoding a second copy of an expected value in a test is a defect. | Duplicated domain constants in `EIP712.t.sol` would have silently diverged from the fixture. |
| **R5** | **Deviations are design-doc edits, same PR.** Any implementation deviation from the design (interface shape, semantics) is written into the design doc with date + rationale in the same change, never left as code-only knowledge. | `verify(digest)` vs §3.2's `verify(structHash)`, and strict all-valid vs "validCount ≥ threshold" — both now documented in §3.2. |
| **R6** | **Fail-fast validation on signed payloads.** Any field covered by the EIP-712 digest must be constrained by `StateDelta.Validate()` (or the contract) — no digest-covered field may be simultaneously ignored by consumers and unconstrained by validation. | A non-setup delta could smuggle `SetupParameters` bytes that endorsers sign but the contract ignores; unsorted/duplicate metadata keys would break byte-identical re-derivation across endorsers. Both now rejected by `Validate()`. |
| **R7** | **Module isolation (Angelo, Week-1 review).** Everything EVM stays under `x/token/services/network/evm` as its own Go module, and **the core token-sdk must never import it** (`go list -deps ./token/...` must not contain `network/evm`). The lean module also must not import `token/sdk/dig` / the fabric platform SDK — that drags core's whole fabric+idemix graph in and cannot be version-reconciled. Any composition of the driver **with** a core token driver (the `evmdlog` SDK) is an **integration-module** concern (Week 6), not a lean-module one. | Attempting to host the `evmdlog` SDK in the lean module broke `go mod tidy` (idemix skew) on 2026-07-09; core→evm dependence would defeat the entire isolation goal Angelo set. |

### 0.3 Critical path, parallelization, risk front-loading

```
Wk1 FREEZE (StateDelta+keys+EIP712) + registered skeleton + EVM-node reachability SPIKE (anvil; same probe → Besu)
        ├── Wk2 Contracts (Solidity/forge) ───────────────┐  (parallelizable: contract help welcome)
        └── Wk3 StateDelta translator + EIP-712 signer ────┤
Wk4 Endorsement (responder/initiator/provider/registry) ───┤
Wk5 Driver + 16 methods + JSON-RPC client + DI + receipt finality (primary path)
Wk6 Besu NWO bootstrap + forge-deploy into it + fabtoken END-TO-END
Wk7 endorsed PP-update + zkatdlog END-TO-END + recipient anchor→finality
Wk8 hardening + full integration matrix + metrics + buffer
    (stretch, if time: fabric-x-evm bootstrap + gateway isPending lifecycle)
```
The one remaining front-loaded risk is the **Go↔Solidity EIP-712 vector** (gated Wk1–2). The backend-bootstrap
risk is much reduced now that the acceptance target is Besu (mature tooling) rather than a from-scratch
fabric-x-evm bootstrap.

---

## Week 1 — Freeze foundation + registered skeleton (split into 4 sub-phases)

Done in dependency order; each sub-phase must `go build ./...` green + its unit tests pass before the next.
No big-bang. Quality bar: godoc on every export, Apache header, table-driven tests, `make checks` clean at
each sub-phase.

### Phase 1.1 — Scaffolding, dependencies, crypto primitives (the ground)  ✅ DONE

- [x] Package tree created under `x/token/services/network/evm/{client,crypto,eip712,keys,statedelta,
      endorsement,finality,pp,contracts}` (co-located with the design docs under `x/`; promote to
      `token/services/network/evm/` when the feature graduates). Package-doc `doc.go` per package.
- [x] `golang.org/x/crypto/sha3` used for keccak (already a direct dep, no go.mod change). `secp256k1`
      deferred to Phase 1.4/3 where the signer first uses it (avoids an unused dep).
- [x] **go-ethereum guard**: `depguard_test.go` runs `go list -deps ./...` and fails if
      `github.com/ethereum/go-ethereum` is in the build graph. Passing (absent).
- [x] `client/types.go`: local `Address` (20B) + `Hash` (32B), hex/text/JSON encoding, strict `HexTo*`
      parsing, right-aligned `BytesTo*`. No go-ethereum. Tests: parsing table + round-trips.
- [x] `crypto/hash.go`: `Keccak256`/`Keccak256Hash` (via `sha3.NewLegacyKeccak256`) + `SHA256`. Tests:
      keccak/sha256 known-answer vectors, variadic concat, and **SHA-256 == `utils.Hashable.Raw()`** parity.

Gate 1.1 ✅: `go build` + `go test ./x/.../evm/...` green; gofmt + `go vet` clean; go-ethereum guard passes.

### Phase 1.2 — Wiring skeleton: EVMClient iface + registered no-op driver + SDK module  ✅ DONE

- [x] `client/evmclient.go`: the `EVMClient` interface (type-safe over `Address`/`Hash`; `IsPending` +
      receipt cover the finality resolver) with a `//go:generate counterfeiter` directive. Mock generation
      deferred to Phase 5 (first consumer); counterfeiter is a `make install-tools` tool, unused mock would
      only drift now.
- [x] `network.go`: `*Network` implements all **16** `driver.Network` methods returning `errNotImplemented`
      (with a `var _ driver.Network` assertion). `envelope.go`: real minimal `*Envelope` (Bytes/FromBytes/
      TxID/String). Ledger stub returns `errNotImplemented` (full adapter in Phase 5).
- [x] `driver.go`: `Driver` + `NewDriver(configService)` + `New(network, channel)` that **errors for non-EVM
      networks** so the provider falls through. Routing extracted behind a `networkResolver` seam
      (`configNetworkResolver` detects the `services.network.evm` config block) for unit testing.
- [x] Driver-construction/routing coverage: `driver_test.go` `TestDriverNewRouting` (`New` returns a Network
      for an evm-configured TMS, errors for non-evm / mismatched channel) — runs in the lean module.
- [x] Reachability spike `client/reachability_test.go`: starts anvil and probes `eth_chainId` via raw
      JSON-RPC; skips cleanly when anvil is absent (same probe works against Besu / fabric-x-evm).
- **SDK composition (`evmdlog`) — relocated per Angelo's Week-1 requirement + module isolation (R7):** the
  original `integration/token/common/sdk/evmdlog/sdk.go` (evm network driver + dlog token driver over
  `viewsdk`) **cannot live in the lean evm module** — it imports `token/sdk/dig`, which transitively pulls the
  core token-sdk's entire fabric+idemix graph and cannot be version-reconciled inside the isolated module
  (verified 2026-07-09: `idemix` skew, tidy fails). Since Angelo requires the lean module to stay free of
  core imports, the evm+dlog **composition is an integration-module concern, reintroduced in Week 6** (the
  integration module already aligns those deps). The lean module keeps only the driver + its
  construction/routing test; **full DryRunWiring registration moves to Week 6**. (This is why local HEAD had
  no evmdlog — the deletion was correct, the relocation just wasn't finished.)

Gate 1.2 ✅: `go build`/`go test` green in both the main and integration modules; gofmt + `go vet` clean;
go-ethereum guard still passes. (Also fixed a pre-existing integration `go.sum` gap for badger so the
integration module builds again.)

### Phase 1.3 — Frozen data model: StateDelta types + evm key derivations  ✅ DONE

- [x] `statedelta/types.go`: `StateDelta` (single `bytes32[] SpentRefs`, `OutputToken[] Outputs`,
      `MetadataKeys/Vals`, `TokenRequestHash`, `PublicParamsHash`, `PublicParamsVersion`, `IsSetup`,
      `SetupParameters`) + `OutputToken`. No `Input` struct. Added `Validate()` (metadata alignment + setup
      invariants) as a fail-fast guard for the translator/endorsers.
- [x] `keys/keys.go`: EVM key derivations as **`[32]byte`** — `ComputeTokenID(anchor,index) =
      keccak256(abi.encode(anchor,index))`, `SpentRefForSerial`, `IssueMetadataKey`, `TransferMetadataKey`
      (1-byte domain prefixes), `AnchorFromTxID`.
  - **Deviation from the plan's "implement `translator.KeyTranslator`"**: that interface returns string keys
    for Fabric's RWSet Translator, which the EVM driver does not reuse (it builds its own StateDelta
    translator, Phase 3) and needs `bytes32`, not strings. So the `keys` package exposes EVM-native `[32]byte`
    derivations instead. The validator's `getState` (Phase 4) will call `ComputeTokenID` directly.
- [x] Tests: abi.encode self-consistency (independently reproduces `keccak256(abi.encode(...))`); **golden
      vectors locked** (reproduced by Solidity in Phase 2); distinctness/length-safety; domain separation
      (metadata vs serial never collide); determinism; `AnchorFromTxID` parsing; `StateDelta.Validate`.

Gate 1.3 ✅: builds; golden vectors locked; gofmt + `go vet` clean; determinism/distinctness green.

### Phase 1.4 — EIP-712 (Go side) + Week-1 freeze (freeze artifact 2)  ✅ DONE

- [x] `eip712/domain.go`: `Domain{ChainID, VerifyingContract}` + `Separator` (name "Panurus", version "1").
- [x] `eip712/encode.go`: 32-byte word encoders (uint64/big/bool/address/left-pad).
- [x] `eip712/hashstruct.go`: EIP-712 type strings + type hashes for `StateDelta`/`OutputToken`; `HashStruct`
      with correct dynamic-member hashing — `bytes32[]` as keccak of concatenated words, `bytes[]` as keccak
      of concatenated element hashes, `OutputToken[]` as keccak of concatenated element hashStructs, `bytes`
      as keccak(value). `tokenRequestHash`/`publicParamsHash` carried as the SHA-256 `bytes32` fields.
- [x] `eip712/digest.go`: `Digest = keccak256(0x1901 || DomainSeparator || HashStruct(delta))`.
- [x] Tests: **golden type hashes, domain separator, and digest locked**; determinism; a **sensitivity test
      covering all 10 digest-covered fields** (mutating any changes the digest — the no-blind-sign guarantee
      at the bytes level; the original claim of "9" missed `setupParameters`, whose mutation was absent —
      found and fixed in the 2026-07-08 review, see R3); empty-array stability. Golden fixture committed at
      `contracts/test/statedelta_digest_fixture.json` for the Solidity cross-check; a **second golden vector**
      (setup/PP-update delta: empty arrays + `setupParameters`, digest `0xdca9a011…`, ethers-validated) added
      in the same review so both delta shapes endorsers ever sign are cross-impl gated.

Gate 1.4 ✅ (Week-1 FREEZE): golden digest locked; gofmt + `go vet` clean; full evm suite green.
**StateDelta + keys + EIP-712 are frozen.** Cross-impl vs Solidity is Week 2. (Angelo already steered the
one-list model; his one-line ack on the input-identity read is the only outstanding courtesy item.)

## Week 2 — Smart contracts (parallelizable)

**Delivered as two PRs, each built and reviewed in phases (quality gate per phase, no big-bang):**

- **PR 2a — EIP-712 core + EndorsementVerifier** (the crypto half + the cross-impl gate) — ✅ DONE, 21/21 forge tests green:
  - [x] Phase A: Foundry scaffold (`foundry.toml`, remappings, `.gitignore`, forge-std submodule + `foundry.lock`)
    + `StateDelta.sol` (frozen structs) + `EIP712.sol` library (type hashes, `hashStruct`, domain separator,
    digest) + `EIP712.t.sol` reproducing **both** fixture vectors — transfer-shaped (`digest 0xc9326b72…`)
    and setup/PP-update-shaped (`digest 0xdca9a011…`, added 2026-07-08 review) — with domain inputs and
    expected values parsed from the committed fixture (R4). **The Go↔Solidity gate is GREEN** — Go, ethers
    v6, and Solidity all agree on both shapes. The plan's #1 risk (EIP-712 disagreement) is closed.
  - [x] Phase B: `EndorsementVerifier.sol` (`verify(bytes32 digest, bytes[])` — pure signature checker:
    ecrecover, low-s, `v∈{27,28}`, 65-byte, signer uniqueness, threshold; deployer-seeded immutable set) +
    `EndorsementVerifier.t.sol` (15 tests: happy 2-of-3/3-of-3, below-threshold, duplicate-signer,
    non-endorser, wrong-digest, high-s, bad-v, bad-length, 4 constructor invariants, getters). **Design
    deviation from §3.2 (production-correctness):** `verify` takes the final **digest**, not `structHash` —
    TokenState (a per-TMS clone) owns the domain separator (binds `verifyingContract=address(this)`), so it
    computes the digest; a shared/decoupled verifier cannot. Avoids a verifier↔TokenState address
    chicken-and-egg. Design §3.2 updated to match.
- **PR 2b — TokenState + deploy + integration** (the state machine on the proven crypto):
  - Phase A: `TokenState.sol` storage §3.1 + `applyStateDelta` §3.4 (computes `hashStruct`+digest via the
    PR-2a library, calls `verifier.verify(digest, sigs)`) + core tests (Go-signed delta verifies on-chain;
    double-spend; forged-content spend rejected by `snMarker`).
  - Phase B: PP/setup lifecycle (SHA-256 precompile `0x02`, version bump first=0/then+1), queries (with the
    §5.3 `isSpent` query-surface decision — recommend option (a): `tokenID → spent` map), events, deploy
    script + `stale-PP`/`PP-update` integration tests.

Frozen contract from Week 1 (do not deviate): the Solidity `StateDelta`/`OutputToken` structs must use the
**exact field names, types and order** of the EIP-712 type in `eip712/hashstruct.go` — note
`uint64 publicParamsVersion` (not uint256), `bytes32[] spentRefs` (single list), `bytes setupParameters`, and
`OutputToken{tokenID, snMarker, tokenData}` (content-bound marker added post-review, see below). The
Go↔ethers-validated fixture `contracts/test/statedelta_digest_fixture.json` (digest
`0xc9326b72636896424aabe0039efef420df6cd18811b82db3237260110f39b64d`) is the cross-impl oracle; the Solidity
`hashStruct`/digest must reproduce it exactly.

**Post-review addition (content binding):** the SDK validator is stateless — it validates the input tokens
carried in the request, not the real on-chain ones. A bare `tokenID` spend reference would let a spender
present forged bytes at a real `(anchor, index)`. `OutputToken.snMarker = keys.OutputSNMarker(anchor, index,
tokenData)` binds the content (mirrors Fabric's `CreateOutputSNKey`); graph-revealing spends reference the
`snMarker`, not the `tokenID`. `tokenID` remains the addressable storage key for queries only.

**Module note:** `x/token/services/network/evm` is now its own Go module (`go.mod`), isolated so the rest of
the token-sdk does not depend on it. Contract/forge tooling here stays separate from the module's Go deps.

- [ ] `EndorsementVerifier.sol` (ecrecover, low-s `s ≤ N/2`, `v∈{27,28}`, signer uniqueness, threshold).
- [ ] `TokenState.sol`: storage §3.1 (`tokens`, `snExists`/`snSpent`, `serialUsed`, `graphHiding` flag);
      typed `applyStateDelta` enforcement §3.4; queries/events; on-chain `computeTokenID` =
      `keccak256(abi.encode(anchor, index))` and `hashStruct` identical to `keys`/`eip712`; setup/PP-update
      path (SHA-256 precompile 0x02, version bump first=0/then+1).
  - `spentRefs` are **opaque `bytes32`**: for graph-revealing, each ref is an `snMarker` — the contract checks
    `snExists[ref] && !snSpent[ref]`, else `InputMissingOrSpent`; for graph-hiding, checks `!serialUsed[ref]`.
    The contract does NOT recompute token-id/marker/serial hashes — that hashing happens off-chain in `keys`
    (`ComputeTokenID`, `OutputSNMarker`, `SpentRefForSerial`). It only branches on `graphHiding`.
  - On output creation: store `tokens[tokenID] = tokenData` and set `snExists[snMarker] = true`.
  - **DECIDE (query surface, consequence of content-binding — settle before the ABI freezes):** the spent
    flag lives under the content-bound `snMarker`, but `AreTokensSpent`/`QueryTokens` receive only a
    `token.ID` (anchor, index) with no token content, so `isSpent(tokenID)` cannot resolve in one lookup.
    Pick one and reflect it in both the contract and the driver's §5.3 method:
    (a) contract also records a `tokenID → snMarker` (or `tokenID → spent`) mapping at output creation, so
        `isSpent(bytes32 tokenID)`/`areTokensSpent(bytes32[])` answer directly on-chain; **or**
    (b) contract exposes only the marker-keyed check and the **driver** does a 2-call resolve
        (`getToken(ComputeTokenID)` → recompute `OutputSNMarker` → `isSpent(marker)`) off-chain.
    (a) costs one extra SSTORE per output but keeps the query path a single call and the driver simple;
    (b) is cheaper on-chain but adds a round-trip and off-chain hashing to every spent-check. Recommend (a).
    This is a direct consequence of Angelo's stateless-validator / content-binding requirement (2026-07-03);
    give him a one-line heads-up when reviewing the contract, but it does not block starting it.
- [ ] forge deploy script (verifier + TokenState; seed PP v0 + endorser set + graphHiding from PP).
- [ ] forge tests incl. **the fixture digest + a Go-signed delta verifying on-chain** (the cross-impl gate).
- [x] Independent EIP-712 validation of the Go side vs ethers v6 done in Week-1 review
      (`contracts/test/eip712_check.js`): type hashes, domain separator and digest all match, including after
      the `snMarker` addition.

Gate: forge suite green; Solidity reproduces the fixture digest (`0xc9326b72…`); a Go-signed delta verifies
on-chain; a forged-content spend (real `tokenID`, different `tokenData`) is rejected by the `snMarker` check.

## Week 3 — StateDelta translator + EIP-712 signer

- [ ] `statedelta/translator.go`: Setup/Issue/Transfer mapping (§5.2) producing a `statedelta.StateDelta`.
      Build `SpentRefs` off-chain via `keys`, **content-bound** (the snMarker decision, confirmed by Angelo
      2026-07-03: the SDK validator is stateless on token content, so a bare `(anchor, index)` ref would let a
      spender present forged bytes at a real position — see design §5.1):
      - graph-revealing → `keys.OutputSNMarker(keys.AnchorFromTxID(input.TxId), input.Index,
        serializedInputs[i])`, pairing `GetInputs()` with `GetSerializedInputs()` (index-aligned; assert equal
        length). **Not** `ComputeTokenID` — that omits the content and would never match the on-chain
        `snExists`/`snSpent` markers. This mirrors Fabric's `checkInputs`/`spendInputs`
        (`translator.go:444/467`), which key spends by `CreateOutputSNKey(input.TxId, input.Index,
        serializedInputs[i])`.
      - graph-hiding → `keys.SpentRefForSerial(sn)` from `GetSerialNumbers()` (exactly one path is non-empty
        per driver).
      Outputs: `TokenID = keys.ComputeTokenID(anchor, counter+i)` (addressable storage/query key) **and**
      `SNMarker = keys.OutputSNMarker(anchor, counter+i, outputBytes)` (recorded at creation; the value a
      later graph-revealing spend must reproduce). Metadata via `keys.TransferMetadataKey`/`IssueMetadataKey`.
      `TokenRequestHash` = `crypto.SHA256(request)`, `PublicParamsHash` = `crypto.SHA256(pp)`.
- [ ] Exact counter (issue `+= len(outputs)`, transfer `+= NumOutputs()`, redeem skipped) + canonical
      ordering (sort metadata by key) so all endorsers emit byte-identical deltas; call `delta.Validate()`.
- [ ] `eip712/signer.go`: secp256k1 sign/verify (`decred/secp256k1`), 65-byte `{r,s,v}`, low-s, address
      derivation `keccak256(pubkey)[12:]`; add `decred/dcrd/dcrec/secp256k1/v4` to `go.mod` here (first use).
- [ ] Unit tests: translator determinism (shuffled metadata → identical bytes), key parity with `keys`, signer
      round-trip/recovery vectors (sign digest → recover expected address), and the **content-binding
      round-trip** — a token created as an output at `(anchor, index)` yields, when later spent as an input, a
      spend marker byte-identical to the `SNMarker` recorded at creation; exercised with **real fabtoken and
      zkatdlog/nogh actions** (relies on `GetSerializedInputs()[i]` == the output bytes at creation, the
      invariant Fabric already depends on). A forged-content input must produce a non-matching marker.

Gate: deterministic delta bytes; a Go-signed delta verifies on the Week-2 contract.

## Week 4 — Endorsement (responder, initiator, provider, registry)

- [ ] `endorsement/registry.go`: address ↔ `view.Identity`.
- [ ] `endorsement/responder.go` (template `fabric/.../responder.go`): authorize (allowlist) → validate
      (`UnmarshallAndVerifyWithMetadata` + `eth_call` `getState` ledger) → persist validation record →
      translate → assert pp version → sign. **No precomputed digest.**
- [ ] `endorsement/initiator.go` + `esp.go` (lazy `Provider[TMSID,Service]` + view registration).
- [ ] Tests: tampered-delta refusal (no blind-sign), 2-of-N assembly, authorization reject.

Gate: 2-of-N endorsement (mocked FSC sessions) assembles a tx whose sigs verify on the contract.

## Week 5 — Driver, 16 methods, JSON-RPC client, DI, receipt-finality baseline

- [ ] `client/jsonrpc.go`: real `EVMClient` (the frozen interface: `IsPending`, receipt, call+blockTag,
      getLogs, estimateGas, fees, pendingNonce, chainId) + **generate the counterfeiter mock** deferred from
      1.2. **Raw-tx (RLP) encoding + EIP-1559 tx signing must be permissive, not go-ethereum** (see design §9);
      the depguard test will catch a regression.
- [ ] `config.go` (+validation) + a **real-YAML routing test** for `Driver.New`/`IsEVMNetwork` (Week-1 review:
      `config.IsSet("services.network.evm")` on a parent key is unproven; confirm with an evm + a non-evm TMS,
      or probe a leaf like `...evm.endpoint`). `pp/versionkeeper.go` (+provider) synced from
      `getPublicParamsVersion`.
- [ ] `network.go`: all 16 methods (§5.3); `ComputeTxID` = `hex(crypto.SHA256(lenPrefix(nonce)‖creator))`,
      decodable by `keys.AnchorFromTxID` (round-trip test); `NonceManager` (init flag+recovery); `ledger.go`,
      `envelope.go`. `AreTokensSpent` (graph-revealing) resolves through the **content-bound marker** per the
      Week-2 query-surface decision (§5.3), not `isSpent(ComputeTokenID)` — see the Week-2 note.
- [ ] `finality/manager.go` **baseline**: receipt polling at `finalized`; reuse `OnlyOnceListener` + event
      queue; `StateCommitted` indexed-log resolution (recipient-side); wire `AddFinalityListener`/
      `GetTransactionStatus` + `getTokenRequestHash`.
- [ ] `driver.go` `NewDriver(...)` finalized DI (model `fabric/driver.go:119`); SDK module provides EVM services.

Gate: with the real client against anvil, issue→transfer round-trips RequestApproval→Broadcast→finality; the
container resolves the real driver.

## Week 6 — Besu NWO bootstrap + fabtoken END-TO-END (the integration milestone)

- [ ] **Reintroduce the `evmdlog` SDK composition in the integration module** (R7): `integration/token/common/
      sdk/evmdlog/` wiring `evm.NewDriver` + the token driver over `viewsdk`, with a `DryRunWiring`
      registration test. It lives here (not in the lean evm module) because it imports `token/sdk/dig`; the
      integration module already aligns core's fabric+idemix deps. Add the `require`+`replace` for the evm
      module in `integration/go.mod`.
- [ ] `integration/nwo/token/evm/`: an NWO platform/topology that **boots Besu** (dev-mode / Docker),
      forge-deploys verifier + TokenState into it, provisions endorser identities (address↔FSC), wires FSC
      nodes with addresses + endpoints. Mirror `integration/nwo/token/fabricx/` (`Backend` with
      `PrepareNamespace`/`UpdatePublicParams`, `BackedTopology`) so the topology's public interface matches
      `fabricx`'s — the Ginkgo suite (below) reuses the fungible `dlog` bodies verbatim, `dlogx`-style.
- [ ] **Admin deployment runbook** (Angelo, Wk6 deliverable): enumerated bootstrap steps — deploy verifier +
      TokenState clone, seed PP v0, register endorser set + threshold + `graphHiding`. This doc becomes the
      spec the forge/NWO deploy scripts automate.
- [ ] `Makefile` target `integration-tests-evm`.
- [ ] `integration/token/evm/evm_test.go` (Ginkgo) — **fabtoken on Besu**, reusing the existing fungible
      `dlog` test bodies retargeted at the EVM topology: issue, transfer, double-spend reject, sub-threshold
      reject, finality, recipient anchor→finality.

Gate: fabtoken Ginkgo suite green **end-to-end on Besu** (not anvil).

## Week 7 — endorsed PP-update + zkatdlog END-TO-END + recipient finality

- [ ] Endorsed **PP-update flow**: setup token request → setup delta → contract stores PP, bumps version,
      emits `PublicParametersUpdated`; driver `VersionKeeper` resyncs; stale-PP delta rejected.
- [ ] zkatdlog/nogh end-to-end on Besu (same path; opaque token bytes) added to the Ginkgo suite.
- [ ] Recipient-side anchor→finality from chain data (`StateCommitted` indexed-log resolution) exercised
      against Besu's `eth_getLogs`.

Gate: endorsed PP update + version bump tested; zkatdlog suite green on Besu; recipient anchor→finality works.

**Stretch (only if time remains — Angelo: "if time remains we will check fabricx+EVM"):** boot fabric-x-evm
through NWO and layer the gateway `TransactionByHash().isPending` lifecycle (design §7.1: pending→receipt;
no-blockNumber→dropped; superseded→synthetic status-0) onto the finality manager, keeping the receipt path as
the fallback. Coordinate with Storm1289 on gateway readiness. Not required for "done."

## Week 8 — Hardening, full matrix, metrics, buffer

- [ ] Full integration matrix: stale-PP reject, superseded tx, concurrent transfers / nonce recovery,
      recipient-only finality, restart/recovery.
- [ ] Error taxonomy (§13), metrics (§12, `disabled.Provider` in tests), structured logging.
- [ ] `make checks`/lint clean; godoc on exports; `go generate` mocks; DCO sign-off.
- [ ] **Buffer (~1 wk absorbed across Wk6–8)** for the integration/EIP-712/gateway surprises.

Gate (DONE): fabtoken + zkatdlog Ginkgo suites green **on Besu**; receipt-based finality exercised; endorsed
PP update works; driver registered via the `evmdlog` SDK module; `make checks` clean. (Stretch, not required:
fabric-x-evm + gateway isPending.)

---

## Build / verify loop

```
go build ./... ; make checks ; make lint-auto-fix ; go generate ./...
go test ./token/services/network/evm/...                 # unit (mock client / anvil)
(cd x/.../evm/contracts && forge test)                   # contracts
make integration-tests-evm                               # Wk6+ : Besu acceptance
```

## Risk register (front-loaded)

| Risk | Impact | Mitigation (when) |
|------|--------|-------------------|
| Go↔Solidity EIP-712 disagreement | **High** | shared digest **vector**, gated **Wk1–2** (the #1 remaining risk) |
| Besu NWO bootstrap | Med | mature Docker/dev-mode tooling; reachability spiked **Wk1**; full week budgeted **Wk6** |
| secp256k1 ↔ FSC identity integration | Med | spike signer + registry **Wk3–4**; address↔identity in config |
| Solo bandwidth | High | parallelize contracts **Wk2**; pull integration help for **Wk6** |
| `eth_getLogs` topic filter for recipient finality | Med | exercise the `StateCommitted` filter shape against Besu **Wk6/7** |
| fabric-x-evm bootstrap + gateway `isPending` (stretch only) | Low | receipt-finality is the primary path; this is additive, not required for done |

## Honest assessment

Building the **real** driver against Besu (not an anvil demo) is ~7 weeks of work + ~1 week buffer =
**8 weeks**, and holds mainly on (1) Week 1–2 freeze + EIP-712 vector landing on time. The Besu acceptance
target (Angelo, 2026-07-08) removes the largest schedule risk that the earlier fabric-x-evm-from-scratch
bootstrap carried, and the receipt-finality primary path means no dependency on the fabric-x gateway timeline.
fabric-x-evm + gateway `isPending` is now a stretch, attempted only if time remains after the Besu suite is
green. If contracts can't be parallelized in Week 2, expect to use the full 8 and possibly trim Week 8's matrix.

## Notes & Decisions

- Design decisions settled in design §15; §16 are non-blocking confirmations. Input-identity + PP bootstrap
  resolved (content-binding `snMarker`; deployer seeds, quorum owns) — no open pre-Week-2 blockers.
- **Backend (Angelo, 2026-07-08): acceptance = Besu; fabric-x-evm = stretch.** anvil/forge = inner loop only.
  NWO scaffolding + admin runbook = Week 6. EVM Ginkgo suite reuses the fungible `dlog` bodies verbatim
  (`dlogx`-style), so the `evm` topology mirrors `fabricx`'s public interface.
- Deferred (additive, not demo cuts): EIP-1167 clones, ERC-4337, graph-hiding driver.
- Status legend: `[ ] Pending`, `[x] Done`, `[~] In progress`, `[!] Blocked`.

✅ COMPLETE when the Week-8 gate is met.
