# Plan: Support ThresholdRule endorsement policies in FabricX nspolicy endorser selection

✅ COMPLETE

Issue: https://github.com/LFDT-Panurus/panurus/issues/1983
Fixes #1983

## Goal

`QueryServiceEndorserSelector.candidateMSPSetsFromPolicies` in
`token/services/network/fabricx/endorsement/nspolicy.go` currently hard-errors whenever a
namespace's endorsement policy is a `ThresholdRule` (raw public key + signature scheme,
`applicationpb.NamespacePolicy_ThresholdRule`), instead of `MspRule`. This makes
`SelectEndorsers` — and therefore any FabricX token flow needing namespace-policy-based
endorser selection — fail outright for ThresholdRule-protected namespaces.

`ThresholdRule` is a **single-signer** policy (a raw public key + scheme, not a k-of-n
group), so there is no "subset of endorsers" to compute the way there is for `MspRule`.
The only way to select a valid endorser for it is to find which *one* configured FSC
endorser's own MSP/X.509 identity holds the matching public key, and route the request to
that endorser specifically. This is also the objectively correct condition: FSC's own
pre-broadcast `ProposalResponse.VerifyEndorsement` (fabric-smart-client) independently
verifies every endorsement against the endorser's MSP/X.509 key, while fabric-x-committer
verifies on-chain against the namespace's actual `ThresholdRule` key — for one signature to
pass both, the endorser's MSP key and the `ThresholdRule` key must be the same key. No new
config surface is needed: the match is derived purely from data already present (the
policy's embedded key, and each configured endorser's existing MSP identity).

Only the `ECDSA` scheme is supported, since FSC endorser identities are X.509/ECDSA-based;
`BLS`/`EdDSA` `ThresholdRule` namespaces cannot be endorsed through the current FSC signing
path and will get a clear, scheme-specific error rather than a generic one.

## Implementation Steps

1. **Add identity/key-matching helper** in `nspolicy.go`:
   - `endorserForThresholdRule(rule *applicationpb.ThresholdRule, configured []view.Identity) (view.Identity, error)`
   - Rejects non-ECDSA schemes (case-insensitive compare, matching
     fabric-x-committer's `strings.ToUpper(scheme)` convention) with a clear error naming
     the unsupported scheme.
   - Parses `rule.GetPublicKey()` as a PEM/PKIX `*ecdsa.PublicKey` (same format
     fabric-x-committer's `newEcdsaVerifier` expects).
   - For each `configured` identity: unmarshal as `msp.SerializedIdentity`, PEM-decode
     `IdBytes` as an X.509 certificate, extract its `*ecdsa.PublicKey`, and compare with
     `(*ecdsa.PublicKey).Equal`. Skip (don't error on) identities that aren't
     ECDSA-X.509-shaped — they simply can't match.
   - Exactly one match → return it. Zero matches → clear error. (Multiple matches would
     mean two endorsers share a private key, which is a misconfiguration; treat as an
     error too rather than picking arbitrarily.)

2. **Restructure policy resolution** so `SelectEndorsers` fetches the namespace policy once
   and branches on rule type, instead of always going through the MSP-candidate-set path:
   - Extract the existing "look up `PolicyItem` for namespace + unmarshal
     `NamespacePolicy`" logic out of `candidateMSPSetsFromPolicies` into a small pure
     helper (e.g. `namespacePolicyFor(policies, namespace) (*applicationpb.NamespacePolicy, error)`), reused by both paths.
   - `candidateMSPSetsFromPolicies` keeps its current signature/behavior for `MspRule`
     (unchanged), and keeps erroring for `ThresholdRule` — producing an MSP-ID candidate
     set is not a meaningful operation for a single-raw-key policy, so this function's
     existing contract is still correct; it's just no longer the only path.
   - `SelectEndorsers` becomes: fetch policies → resolve namespace policy once → if
     `ThresholdRule`, call `endorserForThresholdRule` and return `[]view.Identity{that one}`;
     if `MspRule`, proceed as today (MSP candidate sets + `fsc.SelectEndorsersForMSPSets`).

3. **Tests** (`nspolicy_test.go`):
   - Keep the existing `"threshold-rule namespace - hard error"` subtest under
     `TestCandidateMSPSetsFromPolicies` as-is (still correct: that specific helper
     legitimately doesn't support `ThresholdRule`), but reword the assertion/message if it
     changes.
   - Add a new test (e.g. `TestEndorserForThresholdRule`) covering: matching ECDSA key
     among configured endorsers → correct single endorser selected; no configured endorser
     matches → clear error; unsupported scheme (BLS/EdDSA) → clear scheme error; malformed
     `public_key` bytes → clear error (feeds into fuzz seed corpus too).
   - Add a `FuzzXxx` test for the new PEM/X.509/key-parsing code path per AGENTS.md (it
     parses attacker-influenced bytes coming off the wire via the query service RPC), seeded
     with: a valid ECDSA PEM key, empty bytes, truncated PEM, non-PEM garbage, a valid PEM
     block of the wrong type (e.g. a certificate instead of a public key).
   - Wire the new `FuzzXxx` target into `.github/workflows/nightly-fuzz.yml`'s fuzz job
     matrix.

4. **Docs**: update `docs/services/network-fabricx.md`'s "Namespace Policy" section
   (currently documents `ThresholdRule` as an unconditional hard error) to describe the new
   ECDSA single-endorser-key-match behavior, and that BLS/EdDSA `ThresholdRule` namespaces
   are still unsupported.

5. **Verification**: `make checks`, `make lint-auto-fix`, `make unit-tests` (or targeted
   `go test ./token/services/network/fabricx/endorsement/...`), plus the fuzz smoke test
   command from AGENTS.md (`go test <pkg> -run='^$' -fuzz='^FuzzXxx$' -fuzztime=20s`).

## Out of scope (follow-up, not this issue)

- **Integration/NWO test topology support** for configuring a `ThresholdRule` namespace
  policy end-to-end (`integration/nwo/token/fabricx`, `integration/nwo/token/fabric/opts.go`
  currently has no such knob). This issue is scoped to the unit-level selection logic and
  docs; wiring up a full integration test would need NWO changes to generate/configure an
  ECDSA keypair for a namespace and provision it to one FSC endorser's identity, which is
  substantial enough to warrant its own issue.
- **BLS/EdDSA `ThresholdRule` support** — would require changes to FSC's endorsement
  signing path itself (identity model beyond X.509/ECDSA), not just endorser selection.

## Implementation Progress

- [x] Step 1: `endorserForThresholdRule` key-matching helper
- [x] Step 2: Restructure `SelectEndorsers` to branch on rule type
- [x] Step 3: Unit tests + fuzz test + nightly-fuzz.yml wiring
- [x] Step 4: Update `docs/services/network-fabricx.md`
- [x] Step 5: `make checks` / `make lint-auto-fix` / unit tests / fuzz smoke test all green

## Notes & Decisions

- Confirmed via direct reads of fabric-x-common v0.2.8 (`applicationpb.ThresholdRule` wire
  format), fabric-x-committer v1.0.4 (`utils/signature/verify.go`, `verify_ecdsa.go`,
  `docs/namespace-policy.md` — not a panurus dependency, but the reference implementation),
  and fabric-smart-client v0.15.1 (`platform/fabricx/core/transaction/pr.go`,
  `platform/fabric/core/generic/msp/x509/*`) that:
  - `ThresholdRule` is single-signer, not k-of-n.
  - No MSP/org metadata links a `ThresholdRule` key to any identity anywhere upstream —
    the match must be derived by comparing keys directly.
  - FSC's pre-broadcast endorsement verification is MSP/X.509-based regardless of the
    namespace's actual on-chain policy type, which is why ECDSA-key-equality is the correct
    (not just convenient) selection criterion.
- User confirmed direction: match the `ThresholdRule` key against configured endorsers'
  existing identities to identify which one(s) can sign, rather than adding a new config
  knob or only improving the error message.
- Issue #1983 already exists, is assigned/labeled — no new issue needed. PR must include
  `Fixes #1983`.
