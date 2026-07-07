# Ethereum / EVM Network Driver — Implementation Plan (production driver, ~8 weeks)

> Companion to `eth_network_driver_design.md`. Branch `feature/evm-network-driver`, module
> `github.com/LFDT-Panurus/panurus`. Ground zero → a **real, working** driver validated against the actual
> backend (**fabric-x-evm**), not a demo. Target 6 weeks, hard ceiling ~8 weeks with buffer. Grounded in
> verified SDK extension points (§0).

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

- **Acceptance backend = fabric-x-evm** (the production target), wired through NWO. anvil/forge is used **only**
  for the fast inner loop (contract unit tests, Go unit tests) — never as the bar for "done."
- **Full design implemented**: both shipped drivers (fabtoken + zkatdlog/nogh), one-list `spentRefs` +
  contract `graphHiding` flag,
  on-chain checks, EIP-712 endorsement with no blind-signing, on-chain PP versioning **and** the endorsed
  PP-update flow, recipient-side anchor→finality from chain data.
- **Finality, production-correct + robust**: receipt-based finality at the `finalized` tag is the always-works
  baseline; the gateway `TransactionByHash().isPending` lifecycle is layered on as the efficiency signal where
  the gateway exposes it (Storm1289: this is in progress gateway-side, so the baseline keeps us unblocked).
- **Genuinely deferred (future scope, not corners cut)**: EIP-1167 clones (deploy optimization), ERC-4337
  (v2 gas/batching), a graph-hiding token driver (none ships today). These are additive and do not change the
  MVP architecture; the ABI/StateDelta frozen in Week 1 already accommodate them.

### 0.3 Critical path, parallelization, risk front-loading

```
Wk1 FREEZE (StateDelta+keys+EIP712) + registered skeleton + fabric-x-evm reachability SPIKE
        ├── Wk2 Contracts (Solidity/forge) ───────────────┐  (parallelizable: contract help welcome)
        └── Wk3 StateDelta translator + EIP-712 signer ────┤
Wk4 Endorsement (responder/initiator/provider/registry) ───┤
Wk5 Driver + 16 methods + JSON-RPC client + DI + receipt finality baseline
Wk6 fabric-x-evm NWO bootstrap + forge-deploy into it + fabtoken END-TO-END
Wk7 gateway isPending finality + endorsed PP-update + zkatdlog END-TO-END
Wk8 hardening + full integration matrix + metrics + buffer
```
The two real risks are pulled forward: the **fabric-x-evm integration** (reachability spiked Wk1, full
bootstrap given all of Wk6) and the **Go↔Solidity EIP-712 vector** (gated Wk1–2).

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
- [x] SDK module `integration/token/common/sdk/evmdlog/sdk.go` over `viewsdk` (no Fabric); provides dlog token
      driver + `evm.NewDriver` into `network-drivers`.
- [x] Reachability spike `client/reachability_test.go`: starts anvil and probes `eth_chainId` via raw
      JSON-RPC; skips cleanly when anvil is absent (same probe works against fabric-x-evm).
- [x] Tests: `evmdlog` `DryRunWiring` (driver registered) green; routing test (`New` returns stub for evm,
      errors for non-evm/mismatched channel) green; stub-not-implemented test green.

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
      covering all 9 fields** (mutating any changes the digest — the no-blind-sign guarantee at the bytes
      level); empty-array stability. Golden fixture committed at `contracts/test/statedelta_digest_fixture.json`
      for the Solidity cross-check.

Gate 1.4 ✅ (Week-1 FREEZE): golden digest locked; gofmt + `go vet` clean; full evm suite green.
**StateDelta + keys + EIP-712 are frozen.** Cross-impl vs Solidity is Week 2. (Angelo already steered the
one-list model; his one-line ack on the input-identity read is the only outstanding courtesy item.)

## Week 2 — Smart contracts (parallelizable)

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
- [ ] forge deploy script (verifier + TokenState; seed PP v0 + endorser set + graphHiding from PP).
- [ ] forge tests incl. **the fixture digest + a Go-signed delta verifying on-chain** (the cross-impl gate).
- [x] Independent EIP-712 validation of the Go side vs ethers v6 done in Week-1 review
      (`contracts/test/eip712_check.js`): type hashes, domain separator and digest all match, including after
      the `snMarker` addition.

Gate: forge suite green; Solidity reproduces the fixture digest (`0xc9326b72…`); a Go-signed delta verifies
on-chain; a forged-content spend (real `tokenID`, different `tokenData`) is rejected by the `snMarker` check.

## Week 3 — StateDelta translator + EIP-712 signer

- [ ] `statedelta/translator.go`: Setup/Issue/Transfer mapping (§5.2) producing a `statedelta.StateDelta`.
      Build `SpentRefs` off-chain via `keys`: graph-revealing → `keys.ComputeTokenID(anchor(input.TxId),
      input.Index)` from `GetInputs()`; graph-hiding → `keys.SpentRefForSerial(sn)` from `GetSerialNumbers()`
      (exactly one is non-empty per driver). Outputs' `TokenID` = `keys.ComputeTokenID(anchor, counter+i)`;
      metadata via `keys.TransferMetadataKey`/`IssueMetadataKey`. `TokenRequestHash` = `crypto.SHA256(request)`,
      `PublicParamsHash` = `crypto.SHA256(pp)`.
- [ ] Exact counter (issue `+= len(outputs)`, transfer `+= NumOutputs()`, redeem skipped) + canonical
      ordering (sort metadata by key) so all endorsers emit byte-identical deltas; call `delta.Validate()`.
- [ ] `eip712/signer.go`: secp256k1 sign/verify (`decred/secp256k1`), 65-byte `{r,s,v}`, low-s, address
      derivation `keccak256(pubkey)[12:]`; add `decred/dcrd/dcrec/secp256k1/v4` to `go.mod` here (first use).
- [ ] Unit tests: translator determinism (shuffled metadata → identical bytes), key parity with `keys`, and
      signer round-trip/recovery vectors (sign digest → recover expected address).

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
      `envelope.go`.
- [ ] `finality/manager.go` **baseline**: receipt polling at `finalized`; reuse `OnlyOnceListener` + event
      queue; `StateCommitted` indexed-log resolution (recipient-side); wire `AddFinalityListener`/
      `GetTransactionStatus` + `getTokenRequestHash`.
- [ ] `driver.go` `NewDriver(...)` finalized DI (model `fabric/driver.go:119`); SDK module provides EVM services.

Gate: with the real client against anvil, issue→transfer round-trips RequestApproval→Broadcast→finality; the
container resolves the real driver.

## Week 6 — fabric-x-evm NWO bootstrap + fabtoken END-TO-END (the integration milestone)

- [ ] `integration/nwo/token/evm/`: an NWO platform/topology that **boots fabric-x-evm**, forge-deploys
      verifier + TokenState into it, provisions endorser identities (address↔FSC), wires FSC nodes with
      addresses + endpoints. (This is the from-scratch bootstrap Storm1289 flagged; budget the full week.)
- [ ] `Makefile` target `integration-tests-evm`.
- [ ] `integration/token/evm/evm_test.go` (Ginkgo) — **fabtoken on fabric-x-evm**: issue, transfer,
      double-spend reject, sub-threshold reject, finality, recipient anchor→finality.

Gate: fabtoken Ginkgo suite green **end-to-end on fabric-x-evm** (not anvil).

## Week 7 — gateway isPending finality + endorsed PP-update + zkatdlog END-TO-END

- [ ] Layer gateway `TransactionByHash().isPending` onto the finality manager (design §7.1: pending→receipt;
      no-blockNumber→dropped; superseded→synthetic status-0), keeping the receipt baseline as fallback.
      Coordinate with Storm1289 on gateway readiness.
- [ ] Endorsed **PP-update flow**: setup token request → setup delta → contract stores PP, bumps version,
      emits `PublicParametersUpdated`; driver `VersionKeeper` resyncs; stale-PP delta rejected.
- [ ] zkatdlog/nogh end-to-end on fabric-x-evm (same path; opaque token bytes) added to the Ginkgo suite.

Gate: isPending path verified on fabric-x-evm; endorsed PP update + version bump tested; zkatdlog suite green.

## Week 8 — Hardening, full matrix, metrics, buffer

- [ ] Full integration matrix: stale-PP reject, superseded tx, concurrent transfers / nonce recovery,
      recipient-only finality, restart/recovery.
- [ ] Error taxonomy (§13), metrics (§12, `disabled.Provider` in tests), structured logging.
- [ ] `make checks`/lint clean; godoc on exports; `go generate` mocks; DCO sign-off.
- [ ] **Buffer (~1 wk absorbed across Wk6–8)** for the integration/EIP-712/gateway surprises.

Gate (DONE): fabtoken + zkatdlog Ginkgo suites green **on fabric-x-evm**; isPending + receipt-baseline
finality both exercised; endorsed PP update works; driver registered via the `evmdlog` SDK module; `make
checks` clean.

---

## Build / verify loop

```
go build ./... ; make checks ; make lint-auto-fix ; go generate ./...
go test ./token/services/network/evm/...                 # unit (mock client / anvil)
(cd x/.../evm/contracts && forge test)                   # contracts
make integration-tests-evm                               # Wk6+ : fabric-x-evm acceptance
```

## Risk register (front-loaded)

| Risk | Impact | Mitigation (when) |
|------|--------|-------------------|
| fabric-x-evm NWO bootstrap from scratch (no tooling exists) | **High** | reachability spike **Wk1**; full week budgeted **Wk6**; coordinate with Storm1289 |
| gateway `isPending` not ready when needed | High | **receipt-finality baseline (Wk5)** keeps driver working; isPending layered **Wk7** |
| Go↔Solidity EIP-712 disagreement | High | shared digest **vector**, gated **Wk1–2** |
| secp256k1 ↔ FSC identity integration | Med | spike signer + registry **Wk3–4**; address↔identity in config |
| Solo bandwidth | High | parallelize contracts **Wk2**; pull integration help for **Wk6** |
| `eth_getLogs` topic filter for recipient finality | Med | exercise the `StateCommitted` filter shape against fabric-x-evm **Wk6** |

## Honest assessment

Building the **real** driver against fabric-x-evm (not an anvil demo) is ~7 weeks of work + ~1 week buffer =
**8 weeks**, and only holds if: (1) Week 1–2 freeze + EIP-712 vector land on time, and (2) the fabric-x-evm
NWO bootstrap in Week 6 goes smoothly — it's from scratch and is the most likely slip. The receipt-finality
baseline deliberately decouples us from the gateway team's `isPending` timeline so the driver is "working"
even if that lands late. If contracts can't be parallelized in Week 2, or the fabric-x-evm bootstrap proves
harder than a week, expect to use the full 8 and possibly trim Week 8's matrix.

## Notes & Decisions

- Design decisions settled in design §15; §16 are non-blocking confirmations — get Angelo's nod on input
  identity + PP bootstrap before Week 2 (only those force a Week-1 re-freeze).
- anvil/forge = inner loop only; **fabric-x-evm = acceptance**.
- Deferred (additive, not demo cuts): EIP-1167 clones, ERC-4337, graph-hiding driver.
- Status legend: `[ ] Pending`, `[x] Done`, `[~] In progress`, `[!] Blocked`.

✅ COMPLETE when the Week-8 gate is met.
