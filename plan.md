# Plan: Security/robustness hardening of token/core/zkatdlog/nogh

✅ COMPLETE

## Goal

Audit `token/core/zkatdlog/nogh/` (and its subfolders) for inputs that cause
panics, are wrongly accepted, or are wrongly rejected when processing
untrusted network/ledger data. Confirm each issue with a regression test,
fix it, and extend fuzz coverage.

Findings came from a 7-way parallel research audit (one subsystem per
agent) plus one retry, covering: protos-go/utils + crypto/math, driver/,
validator/ + issue/, transfer/ + token/, crypto/rp/bulletproof,
crypto/rp/csp, and audit/ + crypto/upgrade/ + crypto/common/ + setup/.

## Implementation steps

1. [x] Fix #1 (critical): bound-check `CurveID` in
   `token/core/common/encoding/asn1/asn1.go` before indexing `math.Curves`.
   Regression test + fuzz test. Done: added `curveAt` helper,
   `TestOutOfRangeCurveID`, `FuzzUnmarshallerNoPanic`, wired into
   `nightly-fuzz.yml`.
2. [x] Fix #2 (high): `transfer/typeandsum.go` `TypeAndSumVerifier.Verify`
   missing `InputValues` length check vs `len(v.Inputs)`. Done: added guard
   + `TestTypeAndSumVerify_ShortInputValues` (T-GAP-C7), confirmed genuine
   pre-fix panic reproduction.
3. [x] Fix #3 (high): `issue/cspissue.go` `CSPVerifier.Verify` missing
   `SameType.Validate(curveID)` call (mirror BulletProof fix from a5f0925a0).
   Done: added `curveID` field + `Validate` call, added
   `TestCSPVerifierRejectsTruncatedSameTypeProof` (T-GAP-C8), confirmed
   genuine pre-fix nil-pointer panic reproduction via `git stash`.
4. [x] Fix #4 (high): `audit/auditor.go` missing `Metadata.Validate()` call
   after deserializing output metadata in `validateIssueOutputs` /
   `validateTransferOutputs`. Done: added `tokenMetadata.Validate(true)` /
   `Validate(false)` right after each `Deserialize`, before the value flows
   into `NewInspectableToken`/`InspectOutput`'s commitment recomputation
   (whose local `commit()` helper has no nil-element guard). Added
   `TestValidateIssueOutputs_RejectsNilValueMetadata` and
   `TestValidateTransferOutputs_RejectsNilValueMetadata` (T-GAP-C9), each
   crafting a `TokenMetadata` proto with a nil `Value` directly (bypassing
   `Metadata.Serialize`, which can never itself produce a nil `Value` proto).
   Confirmed genuine pre-fix nil-pointer panic reproduction via `git stash`
   (`audit.commit` -> `G1.Mul` on both the issue and transfer paths).
5. [x] Fix #5 (defense-in-depth, flagged for extra scrutiny): `issue/verifier.go`
   `BulletProofVerifier.Verify` was missing the `RangeCorrectness.Validate(curveID)`
   call that `SameType` already got, and that the transfer path's
   `Proof.Validate` (bftransfer.go) performs for both sub-proofs before any
   further processing. Done: added `tp.RangeCorrectness.Validate(v.curveID)`
   right after `tp.SameType.Validate(v.curveID)`.
   Investigated whether the gap was a genuine silent-accept soundness bypass
   (matching 13a8bd89d's original point-at-infinity fix to IPA.Validate) by
   substituting the point at infinity for every individual and combined
   element across IPA.L, IPA.R, and RangeProofData's C/D/T1/T2, both with the
   fix applied and with it reverted (via `git stash`). All 8 constructions
   were rejected in both cases — never a silent accept — because the Pedersen
   commitment equation checks in `rangeVerifier.Verify`/`ipaVerifier.Verify`
   and the Fiat-Shamir challenge derivation already catch these tamperings
   independently of `Validate`. Pre-fix rejections came back as generic
   "invalid IPA"/"invalid range proof" errors; post-fix they come back as the
   specific "element is infinity" from the structural check. Conclusion:
   this closes a defense-in-depth / diagnostics gap (same category as the
   truncated-proof nil-field case below), not a demonstrated soundness bypass
   — revising the "reopens the point-at-infinity hole" framing accordingly.
   Added `TestBulletProofVerifierRejectsInfinityInRangeCorrectnessProof` and
   `TestBulletProofVerifierRejectsTruncatedRangeCorrectnessProof` (T-GAP-C10),
   both confirmed to fail against the pre-fix `verifier.go` (via `git stash`)
   and pass post-fix.
6. [x] Fix #6 (medium): `crypto/math/curves.go` `NewCachedNegZrFromInt`
   shadowed-variable bug. On a cache hit for the curve but a miss for the
   specific index, the cache-miss branch computed the negated value into a
   `v := c.NewZrFromUint64(i); v.Neg()` that shadowed the outer
   `v, ok := cc[i]` (nil, since the index lookup failed) — the computed
   value was discarded and the function returned nil. Every curve in
   `valueNegCache` (BN254, BLS12_381_BBS_GURVY, BLS12_381_BBS,
   BLS12_381_GURVY, BLS12_381_BBS_GURVY_FAST_RNG) is pre-populated only for
   indices 0..129 (`2*NumBits+1`, NumBits=64), so any call with a larger
   index — e.g. from `crypto/rp/csp/rp.go`'s `uint64(b-a)`/`uint64(j)`
   call sites — silently returned nil instead of panicking loudly or
   computing the right value, a nil-pointer time bomb for callers that
   don't explicitly nil-check. Fixed by changing `v :=` to `v =` so the
   computed value is assigned to the outer variable instead of discarded.
   Added `TestNewCachedNegZrFromInt_IndexMiss` in `curves_test.go`
   (snapshots/restores `valueNegCache` locally, since the existing
   `snapshotCaches`/`restoreCaches` helpers don't cover it), confirmed to
   fail pre-fix (`require.NotNil` on the returned value fails: nil) via
   `git stash push --keep-index` / `git stash pop`, and to pass post-fix.
   Full `crypto/math` and `nogh/...` test trees pass with no regressions.
7. [x] Fix #7-13 (defense-in-depth batch, per user go-ahead to fix all of
   them too):
   - Fix #7: `driver/ws.go` unchecked type assertion on a deserialized
     `driver.TokenFormat`/action payload. Guarded with an `ok`-checked
     assertion returning a clean error instead of panicking on mismatch.
   - Fix #8: `token/services/utils/protos/protos.go` `FromProtosSlice`
     missing a length assertion before pairwise-converting two slices
     assumed to be equal length. Added an explicit length check.
   - Fix #9: `token/core/zkatdlog/nogh/v1/token/token.go` `commit()`
     missing a bounds check on the Pedersen-generator/element slices it
     indexes into. Added a guard before indexing.
   - Fix #10: `crypto/common/array.go` `HashG1Array` called `e.Bytes()` on
     every element of its variadic `elements` slice without checking for
     nil first — a nil `*math.G1` (e.g. from a partially-deserialized or
     corrupted commitment) caused a nil-pointer-dereference panic inside
     `mathlib.(*G1).Bytes()`. Changed the signature to return an error,
     added a nil check per element. No production callers existed yet, so
     the signature change was zero-impact downstream. Added
     `TestHashG1ArrayWithNilElementErrors` (T-GAP-C14); confirmed genuine
     pre-fix panic via `git stash` + a temporary `poc_test.go` (deleted
     after use), confirmed clean post-fix pass, full `crypto/common`
     package suite green.
   - Fix #11: `transfer/sender.go` `Sender.GenerateZKTransfer` indexed
     `s.InputInformation[0]` (to compare token types and set the output
     type) without checking `InputInformation` was non-empty.
     `NewSender`'s length-equality guard doesn't reject the degenerate
     all-zero-length case (0==0==0 passes), so a zero-input `Sender` is
     constructible and reaches the panic. Added a
     `len(s.InputInformation) == 0` guard reusing the existing
     `ErrInvalidInputs`. Added the "GenerateZKTransfer empty inputs"
     subtest (T-GAP-C15) inside `TestSender`, parameterized over both
     RangeProof and CSPRangeProof proof types; confirmed genuine pre-fix
     panic (`index out of range [0] with length 0`) via `git stash` for
     both variants, confirmed post-fix pass, full `transfer` package suite
     green.
   - Fix #12: `transfer/bftransfer.go` `NewBulletProofProver` and
     `transfer/csptransfer.go` `NewCSPBasedProver` both indexed
     `inputWitness[0].Type` (to compute the commitment to the token type)
     without checking `inputWitness` was non-empty. Fix #11 already
     prevents the current sole call path (`sender.go` -> `NewProver`
     dispatcher -> these provers) from reaching this with an empty
     witness, but both functions are exported and independently
     reachable, so guarded them too (`len(inputWitness) == 0`, reusing
     `ErrInvalidInputs`). Added
     `TestNewBulletProofProver_EmptyInputWitness` in `bftransfer_test.go`
     and `TestNewCSPBasedProver_EmptyInputWitness` in the new
     `csptransfer_test.go` (T-GAP-C16); confirmed genuine pre-fix panics
     (`index out of range [0] with length 0`) via `git stash` for both,
     confirmed post-fix pass, full `transfer` package suite green.
   - Fix #13: `transfer/typeandsum.go` `NewTypeAndSumWitness` indexed
     `in[0].Type` without checking `in` was non-empty. Changed the
     signature to return `(*TypeAndSumWitness, error)`, added a
     `len(in) == 0` guard (T-GAP-C16, same defense-in-depth family as
     Fix #12), and updated both production callers (`bftransfer.go`,
     `csptransfer.go`, which already checked `inputWitness` themselves via
     Fix #12 but still must handle the new return) and the one test
     helper caller (`typeandsum_test.go`'s `prepareIOCProver`). Added
     `TestNewTypeAndSumWitness_EmptyInputs`; confirmed genuine pre-fix
     panic via `git stash` + a temporary `poc_test.go` (deleted after
     use, since the signature change means the new test can't compile
     against the old signature), confirmed post-fix pass, full `transfer`
     package suite green (`go test ./token/core/zkatdlog/nogh/...`
     clean).
   - Fix #14: `issue/bfissue.go` `NewBulletProofProver` and
     `issue/cspissue.go` `NewCSPBasedProver` both indexed `tw[0].Type` (to
     compute the commitment to the token type) without checking `tw` was
     non-empty — the same unchecked-`[0]`-indexing pattern as Fix #11-13,
     found in the `issue` package's provers while triaging fuzz targets for
     step 8. Reachable from production: `issue.NewProver` (dispatcher) ->
     `issuer.go`'s `Issuer.GenerateZKIssue` -> `IssueService.Issue`
     (`token/core/zkatdlog/nogh/v1/issue.go:116`); an empty `values` slice
     produces an empty token-witness slice via
     `token.GetTokensWithWitness`/`GetTokensWithWitnessAndBF`, which has no
     guard against `len(values) == 0`. Added a new `ErrInvalidInputs`
     sentinel to `issue/errors.go` (the `issue` package didn't already have
     one, unlike `transfer/errors.go`) and a `len(tw) == 0` guard to both
     constructors. Added `TestNewBulletProofProver_EmptyTokenWitness` and
     `TestNewCSPBasedProver_EmptyTokenWitness` in `prover_test.go`
     (T-GAP-C17); confirmed genuine pre-fix panics
     (`index out of range [0] with length 0`) via `git stash` for both,
     confirmed post-fix pass, full `issue` package and full
     `token/core/zkatdlog/nogh/...` tree green with no regressions
     (including re-confirming Fix #3's `TestCSPVerifierRejectsTruncatedSameTypeProof`
     still passes, since stashing `cspissue.go` transiently reverted that
     fix too).
8. [x] Fix #15 (defense-in-depth): `crypto/rp/csp/rp.go` `RangeProof.Validate`
   was a no-op — it only restored the transient `Curve` field lost during
   deserialization and never validated any other field, unlike its
   BulletProof sibling (`RangeProofData.Validate`/`IPA.Validate`, hardened by
   Fix #5). A truncated/corrupted `csp.RangeProof` (e.g. missing everything
   past `pokV.A`) was reported valid by `Validate` alone. Fixed by delegating
   to the existing `validateRangeProof` helper (already used internally by
   `rangeVerifier.Verify`), so `Validate` now performs the same non-nil/
   correct-curve/not-infinity structural checks at the outer boundary.
   Investigated whether this was a genuine silent-accept soundness bypass
   (same scrutiny as Fix #5) by truncating a genuine proof's raw ASN.1 wire
   bytes down to its first 2 of 10 top-level elements (technique: serialize
   the real proof, unmarshal at the raw `encoding/asn1` level into a local
   struct mirroring `asn1.Values{Values [][]byte}}`, truncate, re-marshal —
   necessary because `RangeProof`'s fields are all unexported, unlike
   BulletProof's exported `Data`/`IPA`) and confirming via `git stash` that
   the truncated proof was already rejected before the fix, one layer deeper
   inside `rangeVerifier.Verify`'s own `validateRangeProof` call (wrapped as
   "invalid range proof structure" on the issue path, "invalid range proof"
   on the transfer path). Conclusion: closes a defense-in-depth/diagnostics
   gap, not a demonstrated soundness bypass — same category as Fix #5.
   Added `TestRangeProofValidateRejectsZeroValue` (csp package level) and
   updated `TestRangeProofValidate`/`TestCSPRangeCorrectnessValidate`'s
   `valid_single` case to use a genuinely-constructed proof instead of a
   zero-value one (which the no-op previously reported valid, now correctly
   rejected). Added `TestCSPVerifierRejectsTruncatedRangeCorrectnessProof` in
   both `issue/cspissue_test.go` and `transfer/csptransfer_test.go`
   (T-GAP-C18) mirroring each other, the latter using a 2-input/2-output
   transfer since 1-input/1-output ownership transfers skip
   `RangeCorrectness` entirely. Full `transfer`, `issue`, and
   `crypto/rp/csp` package suites green with no regressions.
9. [x] Additional fuzz coverage for proof-parsing/deserialization entry
   points across `token/core/zkatdlog/nogh/v1`. The asn1-package-level
   fuzzer (`FuzzUnmarshallerNoPanic`) and the validator-level
   `FuzzActionDeserializerNoPanic`/`FuzzActionDeserializerMultiActionNoPanic`/
   `FuzzActionResourceLimits` targets already existed from earlier in this
   effort (step 1) but none of them reached into `Verifier.Verify(proof
   []byte)` — the exact call `IssueValidate`/`TransferValidate` make on
   unauthenticated wire bytes, and the exact code path every T-GAP-C3/C5/
   C8/C10/C18 structural-validation fix above hardened. Closed that gap and
   two others found while triaging:
   - `issue/verifier_fuzz_test.go` (new): `FuzzBulletProofVerifierNoPanic`,
     `FuzzCSPVerifierNoPanic`, seeded with a genuine issue proof built via
     `NewBulletProofProver`/`NewCSPBasedProver`.
   - `transfer/verifier_fuzz_test.go` (new): `FuzzBulletProofVerifierNoPanic`,
     `FuzzCSPVerifierNoPanic`, mirroring the issue-side pair, using a
     2-input/2-output transfer (via `setupWithProofType`/
     `prepareInputsForZKTransfer(pp, 2, 2)`) so `RangeCorrectness` is
     populated (a 1-in/1-out ownership transfer skips it).
   - `token/fuzz_test.go` (new): `FuzzTokenDeserializeNoPanic`,
     `FuzzMetadataDeserializeNoPanic` — reached directly from ledger-stored
     output/metadata bytes via `driver.TokenDeserializer`/`TokensService`.
   - `setup/fuzz_test.go` (new): `FuzzPublicParamsDeserializeNoPanic` —
     reached from ledger-stored public parameters via
     `PublicParametersDeserializer`/`NewPublicParamsFromBytes` on every
     validator/prover/verifier startup and params update. First seed attempt
     used a nil idemix issuer PK, which `Validate()` (called inside
     `Serialize()`) rejects before the proof can even be built as a seed —
     fixed by using the same real testdata issuer key
     (`testdata/idemix/msp/IssuerPublicKey`) the existing `setup_test.go`
     suite uses.
   - `crypto/upgrade/fuzz_test.go` (new): `FuzzProofDeserializeNoPanic` —
     reached from an untrusted upgrade request via
     `Service.checkUpgradeProof`.
   All 8 new fuzzers run clean for `-fuzztime=20s` locally (tens of
   thousands to hundreds of thousands of execs each, no crashes, no
   `testdata/fuzz/` crash artifacts left behind) plus a plain `go test` pass
   confirming every seed corpus entry (genuine proof, empty, `"malformed"`,
   truncated-half) passes as an ordinary test case. Wired all
   8 into `.github/workflows/nightly-fuzz.yml`'s `strategy.matrix.include`
   (YAML syntax confirmed valid). Full `token/setup`, `token/token`,
   `crypto/upgrade`, `issue`, and `transfer` package suites green with no
   regressions.
10. [x] Ran `make checks`, `make lint-auto-fix`, `make unit-tests-race` across
    the whole repo. `make checks` was clean after gofmt'ing
    `audit/auditor_test.go` (import-block formatting only, left unformatted
    from an earlier session's Fix #4 edits — no logic change).
    `make lint-auto-fix` reported 0 issues across all 9 Go modules.
    `make unit-tests-race` (full repo, race detector on, excludes
    `/integration/` and `regression`-tagged tests) plus
    `integration/nwo`'s own suite both passed with no failures, no data
    races, across every package including all touched
    `token/core/zkatdlog/nogh/...` packages.

## Notes & Decisions

- User confirmed (via AskUserQuestion): fix #5 immediately like the others
  (flagged for extra review in final summary), fix all low-severity items
  too, and add fuzz coverage at the asn1-package level plus keep the
  existing validator-level fuzz targets in sync.
- Every fix must ship with a unit test that reproduces the bug pre-fix
  (panic or wrong accept/reject) and demonstrates correct behavior
  post-fix, kept permanently as a regression test.
