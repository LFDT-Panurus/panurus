# Plan: #1958 — request approval: check if the request was already approved

## Goal

Before an endorser evaluates an incoming approval/setup proposal (`fsc.ResponderView.Call`),
check whether an equivalent proposal has already been processed, and reject it immediately
instead of re-running expensive cryptographic verification and risking two concurrent
approvals of the same request. The replay-detection key is derived from the content of the
proposal itself: **txID, creator, nonce, and timestamp** — all available before any expensive
work starts. The check must be a **pluggable** component (interface + swappable backend), with
an in-memory implementation as the default, and must live in a **reusable, driver-agnostic**
location so the (currently WIP) Ethereum endorser-based approval flow (#1669) can adopt it
without duplicating logic.

## Background / current state

- The only concrete "request approval" responder today is the Fabric FSC endorsement path:
  `token/services/network/fabric/endorsement/fsc/responder.go`. `ResponderView.Call` (line 132)
  runs: `receive` → `validateProposal` (creator/MSP/ACL/signature checks) → `behaviour.validate`
  (crypto verification of the token request + `AppendValidationRecord`) → `endorse`.
- There is **no explicit "already processed" guard** anywhere on this path today. The closest
  incidental protection is that `AppendValidationRecord` → `AddValidationRecord`
  (`token/services/storage/db/sql/common/endorser.go:178`) inserts into a table whose `tx_id` is
  the `PRIMARY KEY` (schema at `endorser.go:64`), so a true duplicate would eventually fail on a
  raw SQL PK-violation — but only *after* the full `UnmarshallAndVerifyWithMetadata` crypto
  verification has already run, and it's an accidental side effect of storage, not a designed
  guard. There is also no protection at all against two concurrent identical requests racing
  each other through `validate` before either has written its record.
- `endorserdb.StoreService.GetStatus(ctx, txID)` already exists and returns `Unknown` for an
  unseen txID or the stored `TxStatus` otherwise, but it is not exposed on the `fsc.Storage`
  interface (`token/services/network/fabric/endorsement/fsc/storage.go:19`) and not called
  anywhere in the responder path.
- Precedent for a pluggable, config-selected component with a memory-first backend already
  exists in this codebase: `token/services/storage/auditdb/locker/{config.go,factory.go,memory/}`
  (from PR #1729). This plan follows the same shape: an interface, a `Config`/`Backend` enum, a
  `NewFromConfig` factory, and a `memory` package as the default implementation.
- Proposal content (creator, nonce, timestamp) is available via:
  - `tx.Transaction.Creator()` → `view.Identity` (already used in `validateProposal`,
    `responder.go:250`)
  - `tx.Transaction.Nonce()` → `[]byte` (`fabric.Transaction.Nonce`,
    `fabric-smart-client/platform/fabric/transaction.go:204`)
  - `tx.ID()` → the ledger tx ID (`responder.go:193`)
  - Timestamp is **not** currently exposed by the `fabric.Transaction`/`endorser.Transaction`
    wrappers. It lives in the proposal's `ChannelHeader.Timestamp`
    (`google.protobuf.Timestamp`), reachable by unmarshalling
    `tx.Transaction.SignedProposal().ProposalBytes()` with
    `fabric-smart-client/platform/fabric/core/protoutil` (`UnmarshalProposal` →
    `UnmarshalHeader` → `UnmarshalChannelHeader`). This `protoutil` package is already imported
    elsewhere in this repo (`token/services/network/fabricx/tms/submitter.go` imports the
    sibling `core/generic/transaction` package), so this is a precedented, public import path,
    not a reach into FSC internals.

## Design

### 1. New reusable package: `token/services/network/common/replay`

Driver-agnostic replay-detection component, colocated with other network-driver-shared
infrastructure (`token/services/network/common` already hosts `finalitymanager.go`,
`fetcher.go`, `normalizer.go`, `rws/`).

```go
// Key identifies a single proposal for replay-detection purposes.
type Key struct {
    TxID      string
    Creator   []byte
    Nonce     []byte
    Timestamp time.Time
}

// Guard detects whether a request has already been seen.
//
//go:generate counterfeiter -o mock/guard.go -fake-name Guard . Guard
type Guard interface {
    // Check atomically records key as seen and returns ErrAlreadyProcessed if an
    // equivalent key was already recorded. Implementations must guarantee that of
    // two concurrent Check calls with the same key, at most one returns nil.
    Check(ctx context.Context, key Key) error
}

var ErrAlreadyProcessed = errors.New("request already processed")
```

`Key` fields are intentionally not assumed to be redundant with each other (e.g. we do not rely
on `TxID` always being a deterministic function of `Creator`+`Nonce`, since that's a Fabric-ism
that a future driver, such as the Ethereum one, may not preserve) — the guard hashes over all
four fields to build its internal lookup key, so it stays correct regardless of how a given
driver computes its `TxID`.

### 2. Default implementation: `token/services/network/common/replay/memory`

- In-memory only, as the issue asks for. Backed by a `sync.Map` (or `sync.Mutex` + `map`,
  matching the style of `auditdb/locker/memory`) from a SHA-256 digest of the four `Key` fields
  to an insertion timestamp.
- Bounded via a TTL (default a few minutes — long enough to cover one
  `CollectEndorsements` round; see the existing `2*time.Minute` timeout in
  `fsc/initiator.go:99`) and a background sweep (or lazy expiry on `Check`) so memory doesn't
  grow unbounded on a long-running endorser. No new external dependency required.
- `Check` uses `LoadOrStore` (or an equivalent single-step check-and-set under a mutex) so the
  "already seen" test and the "mark as seen" write are atomic — closing the race between two
  concurrent identical proposals.
- Package name mirrors the locker precedent: `memory.New(ttl time.Duration) *Guard`.

### 3. Config + factory: `token/services/network/common/replay/{config.go,factory.go}`

Mirrors `auditdb/locker` exactly, for the "make it pluggable" requirement:

```go
type Backend string
const BackendMemory Backend = "memory"

type Config struct {
    Backend Backend       `yaml:"backend"`
    TTL     time.Duration `yaml:"ttl"`
}

func DefaultConfig() Config { return Config{Backend: BackendMemory, TTL: 5 * time.Minute} }

func NewFromConfig(cfg Config) (Guard, error) {
    switch cfg.Backend {
    case BackendMemory, "":
        return memory.New(cfg.TTL), nil
    default:
        return nil, errors.Errorf("unknown replay guard backend: %s", cfg.Backend)
    }
}
```

Swapping in a distributed backend later (e.g. a Postgres- or Redis-backed guard for multi-replica
endorsers, matching how `auditdb/locker` grew a `postgres` backend after `memory`) only means
adding a new case here and a new sub-package — no change to callers.

### 4. Wiring into the Fabric FSC responder

- `fsc.ResponderView` (`responder.go:79`) gets a new field `replayGuard replayguard.Guard`,
  injected through `newResponderView`/`NewResponderView` (`responder.go:85`, `106`).
- `ResponderView.Call` (`responder.go:132`) checks the guard **immediately after `receive`**,
  before `validateProposal`:

  ```go
  request, behaviour, err := r.receive(context)
  ...
  key, err := proposalKey(request.Tx)   // new helper: TxID/Creator/Nonce/Timestamp
  if err != nil { return nil, errors.Join(ErrInvalidProposal, err) }
  if err := r.replayGuard.Check(context.Context(), key); err != nil {
      return nil, errors.Join(ErrAlreadyProcessed, err) // new sentinel in errors.go
  }
  ```

  Doing the check at the `ResponderView.Call` level (rather than inside
  `approvalBehaviour.validate`) means it protects **both** registered behaviours —
  token-request approval (`InvokeFunction`) and public-params setup (`SetupFunction`) — for
  free, matching the issue's generic wording ("before starting evaluating *a request*").
- New helper `proposalKey(tx *endorser.Transaction) (replay.Key, error)` (in `responder.go` or a
  new `proposal.go` in the same package) extracts `TxID := tx.ID()`, `Creator := tx.Creator()`
  (bytes), `Nonce := tx.Transaction.Nonce()`, and unmarshals the channel header timestamp out of
  `tx.Transaction.SignedProposal().ProposalBytes()` via `protoutil`.
- New sentinel `ErrAlreadyProcessed` (or reuse `replay.ErrAlreadyProcessed` directly) added to
  `token/services/network/fabric/endorsement/fsc/errors.go`.
- Construction: one `Guard` instance per node (not per TMS), built once from config and passed
  down through `endorsement.ServiceProvider` → `loader.load` → `fsc.NewEndorsementService` →
  `NewResponderView`, exactly like `storageProvider`/`channelProvider` are threaded today
  (`provider.go:104-125`). One shared instance avoids N duplicate caches when a node serves
  multiple TMSs/namespaces.
- Config key: `services.network.fabric.fsc_endorsement.replay_guard` (backend/ttl), read in
  `endorsement.loader.load` alongside the existing `FSCEndorsementKey`-scoped config, with
  `replay.DefaultConfig()` as the fallback — so this is opt-in-compatible and requires no
  configuration change for existing deployments.

### 5. Why not the DB-backed (`GetStatus`) path for this issue

Kept out of scope, per your confirmation that in-memory-only is acceptable for now:
- `endorserdb` dedup would catch restarts and multi-replica endorser deployments that the
  in-memory guard structurally cannot, but it costs a DB round-trip on every request and is a
  separate concern from "fast in-memory replay protection."
- The existing PK-violation backstop in `AddValidationRecord` remains as defense-in-depth and
  needs no change.
- If/when multi-replica endorsers become a real deployment target, a `postgres`
  (or similar) `replay.Guard` backend can be added under `NewFromConfig` without touching the
  responder — this is exactly what the pluggable design in step 3 is for.

## Files touched

- **New**: `token/services/network/common/replay/guard.go` (interface, `Key`, sentinel error)
- **New**: `token/services/network/common/replay/config.go`
- **New**: `token/services/network/common/replay/memory/memory.go` (+ `memory_test.go`)
- **New**: `token/services/network/common/replay/factory/factory.go` — separate leaf package so
  `replay` itself doesn't need to import `memory` (would create an import cycle since `memory`
  imports `replay.Key`)
- **New**: `token/services/network/common/replay/mock/guard.go` (counterfeiter-generated)
- **Modify**: `token/services/network/fabric/endorsement/fsc/responder.go` — inject `Guard`,
  add `proposalKey` helper, call `Check` in `Call`
- **Modify**: `token/services/network/fabric/endorsement/fsc/errors.go` — add
  `ErrAlreadyProcessed`
- **Modify**: `token/services/network/fabric/endorsement/fsc/service.go` — thread `Guard` through
  `NewEndorsementService`
- **Modify**: `token/services/network/fabric/endorsement/provider.go` — build the `Guard` from
  config in `loader.load` (or `NewServiceProvider`) and pass it down
- **Docs**: add/update `docs/services/network-fabric.md` (or nearest existing endorsement doc) to
  describe the replay-guard config keys and behavior; link from the endorsement design doc if one
  exists.
- **Tests**: `responder_test.go` — a duplicate proposal (same `Key`) is rejected before
  `validate`/`translate`/`endorse` are ever invoked (assert on the existing mocks' call counts);
  a second, distinct proposal is unaffected; `memory_test.go` — concurrent `Check` calls with the
  same key: exactly one succeeds; TTL expiry allows the same key again after expiry.

## Implementation Progress

- [x] Done — `token/services/network/common/replay` package: `Guard` interface, `Key`,
      `ErrAlreadyProcessed`
- [x] Done — `replay/memory` in-memory implementation (TTL + max-entries LRU, atomic
      check-and-set)
- [x] Done — `replay/config.go` + `replay/factory.go` (`factory.New`)
- [x] Done — counterfeiter mock for `Guard` (`replay/mock/guard.go`), wired into `go generate`
- [x] Done — `proposalKey` helper in the `fsc` package (txID/creator/nonce/timestamp
      extraction via `protoutil`/`transaction.UnpackProposal`)
- [x] Done — wire `Guard` into `ResponderView`/`NewResponderView`/`newResponderView`, checked
      once per `Call` for both approval and setup behaviours
- [x] Done — thread `Guard` construction through `fsc/service.go` and
      `endorsement/provider.go` (Fabric) and `fabricx/endorsement/esp.go` (FabricX), one
      `factory.New(replay.DefaultConfig())` instance per node
- [x] Done — unit tests: `replay/memory/memory_test.go`, `replay/factory/factory_test.go`,
      updated `responder_test.go` and `setup_responder_test.go` (valid proposal fixtures +
      "already processed" cases)
- [x] Done — docs update (`docs/services/network-fabric.md#replay-detection`, cross-linked
      from `network-fabricx.md` and `network-ethereum.md`)
- [x] Done — `make lint-auto-fix && make checks && make unit-tests-race`

## Notes & Decisions

- Dedup key = `(TxID, Creator, Nonce, Timestamp)`, all sourced from the proposal — per explicit
  user direction, not just the derived `Anchor`/`TxID`.
- On detection: **reject with a typed error** (`ErrAlreadyProcessed`), no idempotent replay of a
  cached prior result — per explicit user direction.
- Guard is **pluggable**: interface + config-selected backend (`memory` today), so a future
  distributed backend is a drop-in addition — per explicit user direction.
- Scope: implemented as a **reusable component** under `token/services/network/common/replay`,
  not inlined into the Fabric-specific responder, so the future Ethereum endorser-based approval
  flow (#1669) can reuse it — per explicit user direction.
- Guard is checked once per `ResponderView.Call`, covering both `approvalBehaviour` and
  `setupBehaviour`, rather than only inside `approvalBehaviour.validate` — broader and cheaper
  than issue's literal wording suggests, and no downside identified.

## Addendum: freshness window (2026-07-21)

### Goal

Extend the in-memory `replay.Guard` so a key's claimed `Timestamp` must also lie within a
configurable window of the guard's current wall-clock time, in either direction, before it is
even considered for the dedup cache. The window moves continuously with the node's clock.

### Design

- New sentinel `replay.ErrOutOfWindow` (`replay/guard.go`), distinct from
  `replay.ErrAlreadyProcessed`, so callers/logs can tell a stale/skewed request apart from a
  true duplicate.
- `replay/memory.Guard` gains a `window time.Duration` and an injectable `now func() time.Time`
  clock (`memory.WithClock`, defaulting to `time.Now`). `New` signature becomes
  `New(window, ttl time.Duration, maxEntries int, opts ...Option)`. `Check` rejects with
  `ErrOutOfWindow` when `Timestamp < now-window` or `Timestamp > now+window`, before touching
  the cache; `window <= 0` disables the check (pure dedup, unchanged from before).
- `replay.Config` gains a `Window` field. `DefaultConfig()`: `Window: 5m`, `TTL: 10m` (was
  `5m`), `MaxEntries: 100_000`.
- `replay/factory.New` derives `ttl := max(cfg.TTL, 2*cfg.Window)` before constructing the
  memory guard, so a dedup entry always survives the window during which its key could still
  be replayed.
- `fsc/responder.go`'s guard-error handling changed from unconditionally wrapping
  `ErrAlreadyProcessed` to `errors.WithMessagef(err, ...)`, preserving whichever sentinel
  `Check` actually returned. `fsc.ErrOutOfWindow` alias added to `fsc/errors.go`.

### Implementation Progress

- [x] Done — `replay.ErrOutOfWindow` sentinel
- [x] Done — `replay/memory`: window + injectable clock + freshness check in `Check`
- [x] Done — `replay.Config.Window` + updated `DefaultConfig()`
- [x] Done — `replay/factory.New`: TTL floor (`>= 2*Window`)
- [x] Done — `fsc/responder.go` error surfacing fix + `fsc.ErrOutOfWindow` alias
- [x] Done — `replay/memory/memory_test.go`: stale/future/in-window/clock-moves/window-disabled
      cases; existing tests updated to new `New` signature
- [x] Done — `replay/factory/factory_test.go`: TTL-floor test
- [x] Done — `fsc/responder_test.go` and `fsc/setup_responder_test.go`: "out of window" cases
- [x] Done — `docs/services/network-fabric.md#replay-detection` updated
- [ ] Pending — `make lint-auto-fix && make checks && make unit-tests-race`

### Notes & Decisions

- Symmetric window (`now-window <= ts <= now+window`) — tolerates clock skew in both
  directions, matches typical anti-replay designs — per explicit user direction.
- `Window` kept as a separate config field from `TTL` rather than repurposing `TTL` — per
  explicit user direction.
- Distinct `ErrOutOfWindow` sentinel rather than reusing `ErrAlreadyProcessed` — per explicit
  user direction.

## Addendum: hash-collision fix + configurable guard (2026-07-21)

### Part A: `replay/memory` cache-key hash collision

`memory.hash()` concatenated `TxID`/`Creator`/`Nonce` into SHA-256 with no delimiters, so two
distinct `(TxID, Creator)` pairs could collide if their byte concatenation matched (e.g.
`TxID="a",Creator="bc"` vs. `TxID="ab",Creator="c"`). Fixed by replacing the digest with a
length-prefixed byte-string `cacheKey` (each variable-length field is prefixed with its
8-byte big-endian length before concatenation) and changing the LRU key type from
`digest [32]byte` to `string`. No config/behavior change; `crypto/sha256` import removed.

### Part B: make the guard configurable per-TMS

Previously both the Fabric and FabricX drivers built one `replay.Guard` per **node** via
`factory.New(replay.DefaultConfig())`, hardcoded, shared by every TMS the node serves. Per
explicit user direction, this becomes a **per-TMS** guard, sourced from
`services.network.fabric.fsc_endorsement.replay` in each TMS's own YAML config (same block
that already holds `endorser`/`policy`/`endorsers`/`id`), falling back to
`replay.DefaultConfig()` when the block is absent.

- New `endorsement.NewReplayGuard(configuration, tmsID) (replay.Guard, error)` helper
  (`token/services/network/fabric/endorsement/provider.go`) — reads `ReplayKey` via
  `UnmarshalKey` into a `replay.DefaultConfig()`-seeded var, then `factory.New`. Shared by
  both the Fabric loader (`provider.go`'s `loader.load`) and the FabricX loader
  (`fabricx/endorsement/esp.go`'s `loader.load`), since FabricX reuses the Fabric
  `fsc_endorsement` config namespace.
- Removed the per-node `factory.New(replay.DefaultConfig())` construction and the
  `replayGuard` parameter from `endorsement.NewServiceProvider`/`fabricx/endorsement.NewServiceProvider`
  and from `fabric/driver.go` / `fabricx/driver.go` (including now-unused `replay`/`factory`
  imports there).
- `fsc.NewEndorsementService`/`NewResponderView` signatures unchanged — only the *source* of
  the guard moved from the driver to the per-TMS loader.
- Docs: `docs/configuration.md` gained a `replay:` sub-block in the `fsc_endorsement` example
  plus a new "Optional: token.tms.<name>.services.network.fabric.fsc_endorsement.replay"
  section; `docs/services/network-fabric.md`'s Replay Detection section corrected to describe
  per-TMS construction (was: "one instance per node ... not read from YAML").
- Tests: new `token/services/network/fabric/endorsement/replay_guard_test.go` covers absent
  config (defaults), a configured block being read, and an unknown backend surfacing an
  error from `load`. Existing mock-injected tests (`responder_test.go`,
  `setup_responder_test.go`, `service_test.go`) unaffected — they construct
  `EndorsementService` directly with a `*replaymock.Guard`.

### Implementation Progress

- [x] Done — `replay/memory` cache-key hash-collision fix + tests pass
- [x] Done — `endorsement.NewReplayGuard` helper + `ReplayKey` const
- [x] Done — Fabric loader (`provider.go`) reads per-TMS `replay` config
- [x] Done — FabricX loader (`esp.go`) reads per-TMS `replay` config (via the shared helper)
- [x] Done — `fabric/driver.go` / `fabricx/driver.go`: removed node-level guard construction
- [x] Done — `replay_guard_test.go`: defaults / configured / unknown-backend cases
- [x] Done — `docs/configuration.md` + `docs/services/network-fabric.md` updated
- [x] Done — `go build ./...`, targeted `go test`, `make lint-auto-fix && make checks`

✅ COMPLETE

### Notes & Decisions

- Config lives under per-TMS `fsc_endorsement.replay`, not a node-level key — per explicit
  user direction (overrides the node-level `token.network.replay` option considered earlier).
- Consequence accepted: the guard is now per-TMS rather than one shared node-wide instance;
  memory cost scales with (number of endorsed TMSs) × `maxEntries`. Considered acceptable and
  arguably more correct (per-namespace tuning).
- Backwards compatible: an absent `replay` block behaves identically to the previous hardcoded
  default.