✅ COMPLETE (Analysis Phase) — ✅ COMPLETE (Fix Phase, see bottom of file)

# Plan: Security attack analysis of token/services/identity

## Goal
Analyze `token/services/identity/` (the real identity stack — x509, idemix,
idemixnym, multisig, boolpolicy, interop/htlc, membership, role, wallet,
deserializer, plus top-level glue) for inputs that cause a panic, leak
information that should stay private, or let an attacker bypass
authentication/authorization logic. Every confirmed attack gets a
reproducing unit test; byte-parsing entry points get fuzz tests. No
production code is modified — this pass is analysis + tests only (decided
with the user); fixes are a follow-up decision after reviewing the report.

Scope decided with the user: `token/sdk/identity/` was ruled out — it is a
3-method pass-through (`DBStorageProvider`) with existing full test
coverage and no untrusted-input handling.

## Implementation Steps
1. [x] Confirm scope with user (sdk/identity vs services/identity) and fix
   scope (analysis + tests only, no prod-code fixes).
2. [x] Run a multi-agent workflow (Workflow tool) that, per subsystem:
   - Finds candidate attacks across three lenses (panic / info-leak /
     logic-bypass), each finder attempting real reproduction via scratch
     Go programs.
   - Deduplicates findings.
   - Adversarially verifies each finding (3 independent votes, each
     re-attempting reproduction blind to the finder's sketch).
   - For confirmed findings, writes a regression test that reproduces the
     bad behavior today (`require.Panics`, or asserting the bypass/leak
     occurs).
   - Adds/extends fuzz tests (`FuzzXxx`) around exported byte-parsing
     entry points per subsystem, seeded with attack inputs that are *not*
     known-panicking (those get their own explicit regression test
     instead, to keep fuzz corpora green).
3. [x] Run `go test ./token/services/identity/...` to confirm everything
   added compiles and behaves as intended (regression tests pass, proving
   the bug currently exists; fuzz tests pass for the short seeded corpus).
   Full suite: all packages `ok`.
4. [x] Run `make lint` / `gofmt` on touched test files. `gofmt -l` clean,
   `go vet ./token/services/identity/...` clean, `golangci-lint run
   ./token/services/identity/...` → "0 issues" (fixed 2 pre-existing
   `testifylint`/`gosec` findings surfaced in `membership/lm_security_test.go`
   while linting — `strings.Contains`→`require.NotContains`,
   `//nolint:gosec` on the two `exec.Command(os.Args[0], ...)` test-binary
   self-re-exec calls, which gosec's taint analysis flags as command
   injection but are not attacker-influenced).
5. [x] Present the full analysis to the user: confirmed findings by
   severity, refuted candidates (with reason), and pointers to the new
   tests. See findings below.

## Notes & Decisions
- No production code changes in this pass — user chose "analysis + tests
  only". Fix prioritization happens after the report is reviewed.
- No `docs/` update required — no public API/protocol/user-facing
  behavior changes.
- Subsystems audited: x509, x509/crypto, x509/crypto/csp, idemix,
  idemix/crypto, idemixnym, multisig, boolpolicy, interop/htlc,
  membership, role, wallet, deserializer, top-level identity package.
- **CONFIRMED (empirically reproduced) — HIGH severity panic**:
  `idemix/crypto/audit.go` `AuditInfo.EnrollmentID()`/`RevocationHandle()`
  index `Attributes[2]`/`Attributes[3]` with no bounds check;
  `DeserializeAuditInfo` only rejects `len(Attributes) == 0`, so JSON
  audit-info with 1-3 attributes deserializes fine and panics on first
  `EnrollmentID()`/`RevocationHandle()` call. Reachable from untrusted
  bytes via `Provider.GetEnrollmentID`/`GetEIDAndRH` →
  `EIDRHDeserializer.DeserializeAuditInfo` →
  `idemix.AuditInfoDeserializer.DeserializeAuditInfo`. Same gap exists
  independently in `idemixnym/nym/audit.go`'s `DeserializeAuditInfo`
  (embeds `*crypto.AuditInfo`, identical insufficient check) — a second
  reachability path to the same root cause. Regression tests added:
  `idemix/crypto/audit_test.go` (`TestDeserializeAuditInfo` new subtests)
  and `idemixnym/nym/audit_test.go`
  (`TestDeserializeAuditInfoAttributesTooFewPanics`), both passing today
  (i.e. proving the panic fires).
- Workflow-tool fan-out (13 subsystem finders → adversarial 3-vote
  verification per finding) covering marshal, top-level, x509, idemix,
  idemixnym, multisig, boolpolicy, interop/htlc, membership, role/wallets,
  wallet+deserializer, driver/config completed. All `zz*_test.go` scratch
  files created by finder subagents (and by my own follow-up verification
  of the boolpolicy/multisig/marshal leads) have been deleted — confirmed
  none remain on disk (`find ... -name 'zz*_test.go'` → empty). Their
  content was fully promoted into the permanent regression tests listed
  above before deletion.
- **CONFIRMED (prior session, re-verified present/passing this session)
  — HIGH severity RWMutex corruption → process-wide fatal error, and
  enrollment-ID info leak**: `membership/lm.go`'s `getLocalIdentity` does
  an imperative (non-deferred) `RUnlock()`/`RLock()` pair around
  `refreshAndGet`; if `refreshAndGet` (or anything it transitively calls,
  e.g. the identity store) panics, the `RUnlock` already ran but the
  matching `RLock` never does, so the caller's own deferred `RUnlock`
  (e.g. `GetIdentityInfo`'s) fires one time too many during stack unwind.
  The Go runtime treats this as `fatal error: sync: RUnlock of unlocked
  RWMutex` — a fatal error, not a panic, so no `recover()` anywhere in the
  call stack can intercept it; it aborts the whole process, including
  killing every other in-flight request in a server using panic-recovery
  middleware. Separately, `GetIdentityInfo`'s "not found" error embeds the
  full `localIdentitiesByName` map via `%v`, and non-anonymous
  `LocalIdentity.String()` formats each identity's resolved enrollment ID
  into that string — so any caller triggering a "not found" lookup (e.g.
  an attacker-supplied wallet label) receives every other loaded
  identity's enrollment ID in the error. Regression tests (all passing
  today, confirmed again this session):
  `membership/lm_security_test.go::TestGetLocalIdentity_PanicDuringRefreshCorruptsRWMutex`,
  `::TestGetLocalIdentity_LockCorruptionDefeatsRecover` (proves the fatal
  error bypasses a top-level `recover()` used as request-handler
  middleware), `::TestGetIdentityInfo_NotFoundLeaksOtherIdentities`.
- **CONFIRMED (prior session, re-verified present/passing this session)
  — HIGH severity panic on attacker-supplied audit info (idemix +
  idemixnym)**: `idemix/crypto/audit.go`'s `AuditInfo.EnrollmentID()`/
  `RevocationHandle()` index `Attributes[2]`/`Attributes[3]` with no
  bounds check, and `DeserializeAuditInfo` only rejects
  `len(Attributes) == 0` — so attacker JSON with 1-3 attributes
  deserializes cleanly and panics on the first `EnrollmentID()`/
  `RevocationHandle()` call (reachable via
  `Provider.GetEnrollmentID`/`GetEIDAndRH`). A second, independent gap:
  `DeserializeAuditInfo` never validates that `EidNymAuditData`/
  `RhNymAuditData` are present; attacker JSON that omits them leaves those
  pointer fields nil even with a full 4-element `Attributes` slice, and
  `Match()` nil-pointer-dereferences on `EidNymAuditData.Rand`. Both gaps
  exist independently, a second time, in `idemixnym/nym/audit.go`'s
  `AuditInfo` (embeds `*crypto.AuditInfo`, identical insufficient checks,
  reached via `nym.AuditInfo.Match`'s delegation to the embedded type).
  Regression tests (all passing today, confirmed again this session):
  `idemix/crypto/audit_test.go` (`TestDeserializeAuditInfo` subtests
  "Attributes present but too few", "Three attributes"; `TestAuditInfo_Match`
  subtests "EidNymAuditData is nil - panic", "Match panics via the real
  DefaultManager on short Attributes", "Match nil-pointer-dereferences
  when EidNymAuditData is omitted from JSON") and
  `idemixnym/nym/audit_test.go`
  (`TestDeserializeAuditInfoAttributesTooFewPanics`,
  `TestAuditInfoMatchNilEidNymAuditDataFromJSONPanics`).
- **CONFIRMED (empirically reproduced) — MEDIUM/HIGH severity
  authorization bypass**: `multisig/sig.go` `JoinSignatures` keys required
  signatures by `identity.UniqueID()`, and `Verifier.Verify` has no
  duplicate-member detection. A `MultiIdentity.Identities` list that
  repeats the same identity across multiple "signer" slots (e.g. simulating
  a 3-of-3 policy where all 3 slots are actually one person) is satisfied
  by that one identity's single real signature — one signer masquerades as
  N independent signers. Regression test added:
  `multisig/sig_test.go::TestVerifier_Verify_DuplicateMemberSingleSignerBypass`,
  passing today (proving the bypass). Added
  `multisig/multisig_fuzz_test.go` (`FuzzMultiIdentityDeserializeNoPanic`,
  `FuzzMultiSignatureFromBytesNoPanic`) — two 15s campaigns
  (~466k/~481k execs), no panics found.
- **CONFIRMED (empirically reproduced) — MEDIUM severity silent data
  corruption**: `marshal/marshal.go` `appendTLV`'s long-form length
  encoding only emits a 2-byte (`0x82`) form, which can only represent
  lengths up to `0xFFFF` (65535) — the `//nolint:gosec` comment explicitly
  (and here, incorrectly) assumes the value "fits in 16 bits". For a
  payload of exactly `0x10000` (65536) bytes, `byte(l>>8)`/`byte(l)`
  truncate silently: the declared length wraps to 0 while the full payload
  is still appended after it (the outer SEQUENCE length wraps too, since
  the body also exceeds `0xFFFF`). `DecodeIdentity` trusts the corrupted
  (too-small) declared length and returns an empty `Data` instead of an
  error — any identity/audit payload that happens to hit or exceed 64 KiB
  is silently truncated to nothing rather than rejected. Reachable via
  `typed.go`'s `DecodeIdentity` call, i.e. every deserialization entry
  point that decodes a wire identity. Regression test added:
  `marshal/marshal_test.go::TestEncodeIdentity_PayloadAtTwoByteLengthBoundary_SilentlyTruncates`,
  passing today (proving the corruption/silent-truncation). Added
  `marshal/marshal_fuzz_test.go` (`FuzzDecodeIdentityNoPanic`) — 20s
  campaign, ~369k execs on re-verification run, no panics found (exec
  count varies run-to-run with machine load; re-run to reproduce rather
  than treating any single number as exact).
- **CONFIRMED (empirically reproduced on a genuine 32-bit platform) —
  LOW/MEDIUM severity panic, architecture-conditional**:
  `marshal/marshal.go` `readLen`'s big-endian length accumulator
  (`l = l<<8 | int(b[pos+i])`, up to 4 length-bytes) is declared as plain
  `int`. On the 64-bit platforms this repo's CI runs on, `int` is 64 bits
  and a 4-byte length (max ~4.3 billion) fits exactly, so `DecodeIdentity`'s
  `np+l > len(b)` bounds check correctly rejects any length exceeding the
  input. On a `GOARCH=386`/arm/mips-class platform, `int` is 32 bits, and
  the identical 4-byte high-bit-set length (e.g. `0xFFFFFFFF`) wraps around
  to `-1`, which defeats that same bounds check (`np + -1 > len(b)` is
  false for any small `np`), and `DecodeIdentity` proceeds to slice with a
  negative length. Independently reproduced end-to-end outside this
  environment's native 64-bit toolchain: cross-compiled and ran a
  standalone Go program importing this package's real `DecodeIdentity`
  under `docker run --platform linux/386` on `i386/golang:1.26-bookworm`
  (qemu-emulated; confirmed `int size: 32 bits` inside the container),
  which produced `PANIC RECOVERED: runtime error: slice bounds out of
  range [12:11]` for a crafted SEQUENCE+UTF8String payload with two
  4-byte `0xFFFFFFFF` length fields. Regression tests added:
  `marshal/overflow_test.go` — `TestReadLen_LengthAccumulationOverflowsOnA32BitInt`
  proves the int/int32 divergence directly (passes on 64-bit CI today);
  `TestDecodeIdentity_LengthOverflowPanicsOn32BitPlatforms` runs the actual
  end-to-end panic whenever the test binary itself is built with a 32-bit
  `int` (`strconv.IntSize == 32`) and is otherwise skipped with an
  explanatory message pointing at the Docker/qemu reproduction, since
  this repo's own test suite only runs on 64-bit CI.
- **CONFIRMED (empirically reproduced) — HIGH severity availability/DoS
  (nil-pointer panic on attacker-supplied identity)**:
  `boolpolicy/deserializer.go` `TypedIdentityDeserializer.GetAuditInfoMatcher`
  and the structurally identical `multisig/deserializer.go`
  `TypedIdentityDeserializer.GetAuditInfoMatcher` both loop
  `matchers[k], err = d.AuditInfoMatcher.GetAuditInfoMatcher(ctx,
  <component-identity>, info.AuditInfo)` with no nil-check on the result.
  `deserializer.TypedVerifierDeserializerMultiplex.GetAuditInfoMatcher`
  (`deserializer/verifier.go`) returns `(nil, nil)` — no error — whenever
  the component identity's `IsNone()` is true (i.e. it is an empty byte
  slice). Neither `boolpolicy.WrapPolicyIdentity` nor
  `multisig.WrapIdentities` reject an individual empty/none component
  identity inside an otherwise-valid policy/multisig identity (they only
  check the overall list isn't empty, plus, for boolpolicy, that the
  policy string isn't empty). The resulting nil `driver.Matcher` slot is
  then dereferenced unchecked by `InfoMatcher.Match` in both packages'
  `identity.go` (`e.AuditInfoMatcher[k].Match(ctx, id)`), nil-pointer-
  dereferencing at match time. Proven end-to-end using the exact
  production wiring pattern from `token/core/fabtoken/v1/driver/deserializer.go`
  (`NewDeserializer()`, which passes one shared
  `deserializer.NewTypedVerifierDeserializerMultiplex()` as both the
  `VerifierDES` and `AuditInfoMatcher` args to
  `multisig.NewTypedIdentityDeserializer(des, des)` and
  `boolpolicy.NewTypedIdentityDeserializer(des, des)` — the real-world
  self-referential wiring that makes the "none component identity" case
  reachable, not just a synthetic setup). Regression tests added:
  `boolpolicy/security_test.go::TestNoneComponentIdentityProducesNilMatcherThatPanics`
  and `multisig/security_test.go::TestNoneComponentIdentityProducesNilMatcherThatPanics`,
  both passing today (proving the panic fires).
- **CONFIRMED (empirically reproduced) — MEDIUM/HIGH severity
  authorization bypass (boolpolicy AND-policy duplicate-identity)**:
  `boolpolicy/sig.go` `JoinSignatures` builds per-slot signatures by
  looking up `sigmas[id.UniqueID()]` for each identity in the ordered
  `Identities` list; nothing in `WrapPolicyIdentity` requires those
  identities to be distinct. When the same identity appears at multiple
  `$N` slots (e.g. `"$0 AND $1"` with `Identities[0] == Identities[1]`),
  `JoinSignatures` assigns that one identity's single signature to every
  slot where it appears, and `PolicyVerifier.evalNode`'s `AndNode` case
  independently re-verifies each `RefNode` slot against its own
  `Verifiers[i]` — so one real signer's single signature satisfies an
  AND-policy that looks like it requires two independent signers. The
  same root cause (duplicate identity + `UniqueID()`-keyed signature
  lookup with no duplicate-detection) as the pre-existing, already-tested
  multisig-native bypass below, but here breaking the boolean-policy
  AND-combinator specifically rather than plain multisig threshold
  counting. Regression test added:
  `boolpolicy/security_test.go::TestDuplicateIdentitySatisfiesANDWithOneSignature`,
  passing today (proving the bypass).
- **Pre-existing (confirmed in a prior session, unchanged this pass) —
  MEDIUM/HIGH severity authorization bypass, multisig-native analog of
  the boolpolicy finding above**: `multisig/sig_test.go`'s
  `TestVerifier_Verify_DuplicateMemberSingleSignerBypass` already proves
  a `MultiIdentity` that repeats the same member across every signer slot
  (faking e.g. a 3-of-3 policy) is satisfied by that one member's single
  real signature. No new test needed here — flagged for completeness
  alongside the boolpolicy analog above, since both share the same root
  mechanism (`JoinSignatures` keyed only by `UniqueID()`, no
  duplicate-member rejection anywhere in `WrapIdentities`/
  `WrapPolicyIdentity`).
- Stale-count correction: the marshal fuzz campaign's exec count noted
  above ("~385k") in an earlier note was a one-off snapshot from a single
  run; re-running the identical 20s campaign here produced ~369k execs.
  Fuzz exec counts are inherently run-to-run variable (machine load,
  scheduler jitter) — treat any quoted number as an order-of-magnitude
  sanity check, not an exact reproducible constant. No panics found in
  either run.

---

✅ COMPLETE

# Plan: Fix all 8 confirmed findings from the analysis phase above

## Goal
The analysis phase above (scoped "analysis + tests only") produced 8
confirmed findings. This phase implements production-code fixes for all of
them, converting each existing regression test from "proves the bug exists"
to "proves the bug is fixed/rejected", and adding fuzz tests for the
byte-parsing entry points that were missing one.

## Implementation Steps
1. [x] Fix Findings 1/2 (marshal 2-byte length truncation + 32-bit int
   overflow in `readLen`).
2. [x] Fix Finding 6 (membership RWMutex corruption + enrollment-ID leak).
3. [x] Fix Findings 3/4/5 (boolpolicy/multisig none/duplicate component
   identity bypass, at both Wrap-time and deserialization-time).
4. [x] Fix Finding 8 (idemix/idemixnym audit-info panics on attacker bytes).
5. [x] Full verification pass: `go test ./token/services/identity/...`,
   `go test -race ./token/services/identity/...`, `gofmt -l`, `go vet`,
   `golangci-lint run ./token/services/identity/...`, `make checks`.
6. [x] Update this plan and check `docs/` for needed updates.

## Fixes

- **Findings 1/2 fixed** — `marshal/marshal.go`:
  - `readLen`'s length accumulator changed from platform-width `int` to
    `uint64`, and the truncation bounds check now happens in unsigned
    64-bit arithmetic before ever converting to `int` — closes the
    32-bit-platform integer-wraparound panic unconditionally, not just on
    the 64-bit platforms this repo's CI happens to run on.
  - `appendTLV`'s long-form length encoding now emits the correct DER
    long-form width for the actual length (0x82/0x83/0x84 for 2/3/4
    length-bytes) instead of unconditionally emitting a 2-byte (0x82) form
    that silently truncated/wrapped any payload ≥ 64 KiB.
  - Test flips: `marshal/marshal_test.go`'s
    `TestEncodeIdentity_PayloadAtTwoByteLengthBoundary_SilentlyTruncates`
    now asserts the payload round-trips intact instead of asserting
    silent truncation. `marshal/overflow_test.go`'s
    `TestReadLen_LengthAccumulationOverflowsOnA32BitInt` and
    `TestDecodeIdentity_LengthOverflowPanicsOn32BitPlatforms` now assert
    the overflow no longer occurs / the decode is rejected with an error
    instead of panicking, on any platform's int width.
  - Fuzz test `marshal/marshal_fuzz_test.go`'s `FuzzDecodeIdentityNoPanic`
    (pre-existing) re-verified clean against the fixed code.
  - Verified: `go test ./token/services/identity/marshal/...` full package
    pass; `golangci-lint run` clean (added targeted `//nolint:gosec` on the
    three int→byte truncations in `appendTLV`, which are intentional
    low-byte masks bounded by the enclosing `case l < 0x...:` guard, and on
    the one int→uint64 conversion in `readLen`'s bounds check, which is
    never negative because `pos <= len(b)` is already enforced earlier in
    the same function).

- **Finding 6 fixed** — `membership/lm.go`:
  - `getLocalIdentity`'s imperative `RUnlock()` / `refreshAndGet()` /
    `RLock()` sequence changed to `RUnlock()` then `defer RLock()` before
    calling `refreshAndGet`, so the read-lock is always re-acquired during
    stack unwind even if `refreshAndGet` (or anything it calls) panics —
    closes the `fatal error: sync: RUnlock of unlocked RWMutex` that
    previously escaped `recover()`-based middleware and killed the whole
    process.
  - `GetIdentityInfo`'s "not found" error no longer embeds the full
    `localIdentitiesByName` map (which leaked every loaded identity's
    resolved enrollment ID via `%v`/`LocalIdentity.String()`); it now logs
    only the map's keys via the pre-existing `logging.Keys()` helper.
  - Test flips: `membership/lm_security_test.go`'s
    `TestGetLocalIdentity_PanicDuringRefreshCorruptsRWMutex`,
    `TestGetLocalIdentity_LockCorruptionDefeatsRecover`, and
    `TestGetIdentityInfo_NotFoundLeaksOtherIdentities` now assert the
    fatal error/leak no longer occurs.
  - Verified: `go test ./token/services/identity/membership/...` full
    package pass (including under `-race`).

- **Findings 3/4/5 fixed** — `boolpolicy/{identity,deserializer}.go` and
  `multisig/{identity,deserializer}.go`:
  - New `validateComponentIdentities` helper (one copy per package,
    identical shape) rejects an empty/none component identity and any
    duplicate identity (by `UniqueID()`) among a policy's/multisig's
    component identities.
  - Called from both choke points an attacker can reach: `WrapPolicyIdentity`/
    `WrapIdentities` (construction) and `DeserializeVerifier`/
    `GetAuditInfoMatcher` (deserialization of raw wire bytes, which bypasses
    Wrap entirely for an attacker crafting DER bytes directly) — closes the
    nil-`Matcher`-dereference panic (Finding 5) and the duplicate-identity
    single-signer-satisfies-N-slots authorization bypass (Findings 3/4) at
    both entry points.
  - Test flips: `boolpolicy/security_test.go`'s
    `TestNoneComponentIdentityProducesNilMatcherThatPanics` and
    `TestDuplicateIdentitySatisfiesANDWithOneSignature`, and
    `multisig/security_test.go`'s
    `TestNoneComponentIdentityProducesNilMatcherThatPanics`, now assert
    `WrapPolicyIdentity`/`WrapIdentities`/`DeserializeVerifier`/
    `GetAuditInfoMatcher` reject the malicious input with an error instead
    of panicking/bypassing. `multisig/sig_test.go`'s
    `TestVerifier_Verify_DuplicateMemberSingleSignerBypass` flipped the
    same way (asserts `WrapIdentities` now rejects the duplicate-member
    list outright).
  - Fuzz tests `multisig/multisig_fuzz_test.go`'s
    `FuzzMultiIdentityDeserializeNoPanic`/`FuzzMultiSignatureFromBytesNoPanic`
    (pre-existing) re-verified clean against the fixed code.
  - Verified: `go test ./token/services/identity/boolpolicy/...
    ./token/services/identity/multisig/...` full package pass.

- **Finding 8 fixed** — `idemix/crypto/audit.go`,
  `idemixnym/nym/audit.go`, and their production callers:
  - New exported `crypto.AuditInfo.Validate()` (with a new
    `crypto.MinAuditAttributes = 4` constant) checks
    `len(Attributes) >= 4` (the bound `EnrollmentID()`/`RevocationHandle()`
    unconditionally index into) and that `EidNymAuditData`/
    `RhNymAuditData` are non-nil (the fields `Match()` unconditionally
    dereferences). Called from `crypto.DeserializeAuditInfo`.
  - `idemixnym/nym.AuditInfo` embeds `*crypto.AuditInfo`; its own
    `DeserializeAuditInfo` now delegates to the embedded
    `Validate()` instead of duplicating a weaker check.
  - Closed three bypass sites that built/deserialized an `AuditInfo`
    without going through the validating entry point: `idemix/km.go`'s
    `Info()` (was hand-building the struct and calling `FromBytes`
    directly), `idemixnym/km.go`'s `signerInfo()`, and
    `idemixnym/skiprovider.go`'s `GetSKIsFromIdentity()` (both were using
    raw `json.Unmarshal` instead of `nym.DeserializeAuditInfo`) — all three
    now route through the validating `DeserializeAuditInfo` calls.
  - Test flips: `idemix/crypto/audit_test.go`'s `TestDeserializeAuditInfo`
    subtests and `TestAuditInfo_Match`'s short-Attributes/nil-audit-data
    subtests now assert rejection at deserialize time instead of a later
    panic (one subtest, "EidNymAuditData is nil - panics when the
    deserialize-time validation is bypassed", is kept as `assert.Panics`
    deliberately — it documents that `Match()` still trusts its caller to
    validate first, a contract rather than a residual bug, since an
    `AuditInfo` built directly via struct literal bypasses the
    boundary by construction). `idemix/crypto/deserializer_test.go`'s
    `TestDeserializer_DeserializeAuditInfo` fixtures updated to match.
    `idemixnym/nym/audit_test.go`'s equivalent subtests flipped the same
    way; `idemixnym/skiprovider_test.go`'s `InvalidIdemixSignature`
    fixture updated to supply a valid `AuditInfo` sub-struct so it still
    isolates the SKI-extraction failure it was designed to test, rather
    than colliding with the new upstream validation.
  - New fuzz tests: `idemix/crypto/audit_fuzz_test.go`'s
    `FuzzDeserializeAuditInfoNoPanic` and
    `idemixnym/nym/audit_fuzz_test.go`'s `FuzzDeserializeAuditInfoNoPanic`
    (byte-parsing entry points `crypto.DeserializeAuditInfo`/
    `nym.DeserializeAuditInfo`, per the original audit rule that
    byte-parsing entry points require fuzz tests) — both ran clean for a
    20s campaign (~428k / ~598k execs respectively) with no panics found,
    beyond passing their seeded corpus (valid audit info, empty bytes,
    invalid JSON, `{}`, and the two known-historical short-Attributes/
    nil-nym-data payloads).
  - Verified: `go test ./token/services/identity/idemix/...
    ./token/services/identity/idemixnym/...` full package pass (including
    under `-race`).

## Full verification (whole `token/services/identity/...` tree)
- `go build ./token/services/identity/...` — clean.
- `go test ./token/services/identity/...` — all packages `ok`.
- `go test -race ./token/services/identity/...` — all packages `ok`.
- `gofmt -l` on every touched file — clean.
- `go vet ./token/services/identity/...` — clean.
- `golangci-lint run ./token/services/identity/...` — 0 issues (after
  adding the targeted `//nolint:gosec` justifications noted under
  Findings 1/2 above, and fixing one `staticcheck` QF1008 redundant
  embedded-field selector in `idemixnym/nym/audit.go`, and one
  `testifylint` `require.Equal(len(...), len(...))` → `require.Len` in
  `marshal/marshal_test.go`).
- `make checks` — run against the full repo per AGENTS.md.

## Notes & Decisions
- No `docs/` update: none of these fixes change a public API's *shape*
  (signatures are unchanged) — they change *validation strictness* of
  existing entry points (`WrapPolicyIdentity`, `WrapIdentities`,
  `DeserializeVerifier`, `GetAuditInfoMatcher`, `DeserializeAuditInfo` for
  both idemix and idemixnym) to reject previously-accepted malformed/
  malicious input with an error instead of panicking or silently
  succeeding. This is a bug fix (closing an unintended panic/bypass), not
  a documented protocol or interface change, so no `docs/` page describes
  the old permissive behavior that would need updating.
- `Match()` on both `crypto.AuditInfo` and `idemixnym/nym.AuditInfo`
  deliberately still has no nil-check of its own for `EidNymAuditData`/
  `RhNymAuditData` — the fix centralizes validation at the deserialization
  boundary (`DeserializeAuditInfo`/`Validate()`), consistent with the
  `validateComponentIdentities` pattern used for boolpolicy/multisig. Any
  future new caller that constructs an `AuditInfo` directly (bypassing
  `DeserializeAuditInfo`) must call `.Validate()` itself before `Match()`.

## Nightly fuzz CI wiring
`.github/workflows/nightly-fuzz.yml` had a `strategy.matrix.include` list
of 5 targets, none from `token/services/identity/...` — none of this
audit's fuzz tests (new or pre-existing) ran on the nightly schedule.
Added all 5 identity-package fuzz targets to the matrix, following the
existing `{name, pkg, func}` shape:
- `identity-marshal-decode-identity` — `marshal.FuzzDecodeIdentityNoPanic`
- `identity-multisig-deserializer` —
  `multisig.FuzzMultiIdentityDeserializeNoPanic`
- `identity-multisig-signature-from-bytes` —
  `multisig.FuzzMultiSignatureFromBytesNoPanic`
- `identity-idemix-audit-info-deserializer` —
  `crypto.FuzzDeserializeAuditInfoNoPanic` (`idemix/crypto`)
- `identity-idemixnym-audit-info-deserializer` —
  `nym.FuzzDeserializeAuditInfoNoPanic` (`idemixnym/nym`)

Both `idemix/crypto` and `idemixnym/nym` expose a function named
`FuzzDeserializeAuditInfoNoPanic` in different packages — disambiguated
via distinct `pkg` values; matrix `name` fields kept distinct too since
`name` also seeds the `actions/cache@v4` key and the crash-artifact/issue
labels.

Verified: `go test <pkg> -list='^<func>$'` resolved for all 5 new
entries, YAML parses (Ruby `YAML.load_file`), and `actionlint` reports 0
issues on the file.

## AGENTS.md: codify the fuzz-testing workflow rule
The audit rule that byte-parsing entry points get fuzz tests, and the
gap that produced the "Nightly fuzz CI wiring" step above (new fuzz
tests existed but were never added to the nightly matrix), were both
only implicit/tribal knowledge for this one audit. Added a new "Fuzz
Testing" subsection to `AGENTS.md` (under Testing Strategy, alongside
Integration Tests/Mocking Best Practices) so future work follows the
same rule without rediscovering it:
- add `FuzzXxx` tests for any exported function parsing untrusted bytes,
  proactively (not only after a bug is found there);
- seed with valid/empty/malformed/known-historical-edge-case inputs;
- verify locally with a short `-fuzztime` run plus a plain `go test`;
- wire every new target into `.github/workflows/nightly-fuzz.yml`'s
  matrix, calling out explicitly that an unwired fuzz test never gets
  exercised beyond its seed corpus in CI.
