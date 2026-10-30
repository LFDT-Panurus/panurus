# Lazer: post-quantum anonymous identities

The `lazer` identity type is an experimental alternative to Idemix/IdemixNym for
**owner** wallets. It provides pseudonymous, unlinkable identities with auditability, like
Idemix, but is built on a lattice-based anonymous credential
([lazer](https://github.com/lazer-crypto/lazer), CBDC scheme) instead of pairings.

!!! warning "Status: experimental"
    The module is not wired into any token driver, there is no revocation, the unforgeability of
    credentials is argued but not proven, and the commitment parameters have not been through a
    lattice-estimator review (see [Open issues](#open-issues)). Do not use it in production.

Code: `x/token/services/identity/lazer` (a separate Go module, because it uses cgo).

| Item | Value |
|---|---|
| Identity type | `LazerIdentityType = 7` (`"lazer"`), see `token/driver/wallet.go` |
| Interfaces | `membership.KeyManager`, `membership.KeyManagerProvider`, `idriver.ProbeFreeSignerDeserializer`, verifier/matcher deserializer, `idriver.AuditInfo` |
| Anonymous | yes |
| Auditable | yes (enrollment id and revocation handle) |

## Scheme

For a full technical description of the underlying cryptography (lattices, issuance, proofs, security) see
[The Lazer Anonymous Credential Scheme](identity-lazer-anoncreds.md). The fixes Panurus applies to the lazer
library are described in [Local patches to lazer](identity-lazer-patches.md).

A credential is issued by the issuer (a Falcon-512 key pair, `lazergen ca-keygen`) on five
32-byte attributes:

| Index | Attribute | At issuance | In an identity |
|---|---|---|---|
| 0 | `usk`, random user secret | hidden | committed (`nymSK`) |
| 1 | SHA-256 of the OU | revealed | hidden |
| 2 | SHA-256 of the enrollment id (EID) | revealed | committed (`nymEID`) |
| 3 | SHA-256 of the revocation handle (RH) | revealed | committed (`nymRH`) |
| 4 | SHA-256 of the role | revealed | hidden |

Attributes are always hashed with a domain separator, see `schema.go`.

### Identity (pseudonym)

The identity is what is stored in the token's owner field: the commitment to the enrollment id,
`nymEID = Acom * (H(EID), r)`, and nothing else, as with `idemixnym`. It is 225 bytes (two
polynomials of 64 coefficients, 14 bits each, plus the end marker) and is wrapped in a
`TypedIdentity` of type 7. A fresh `r` makes two pseudonyms of the same wallet unrelated.
Deserializing an identity only checks its length; every proof that uses it decodes it
canonically (coefficients below 12289, exact end marker), so two byte strings never denote the
same nym.

### Signatures

A signature is the DER-encoded ASN.1 sequence `{nymSK, nymRH, pres, sok}`:

- `nymSK = Acom * (usk, r1)` and `nymRH = Acom * (H(RH), r3)` are the other two commitments of the
  pseudonym. They travel with every signature, as the `Creator` of an `idemixnym` signature does.
- `pres` is a zero-knowledge presentation of the credential, with all attributes hidden. It proves
  that a credential of the issuer exists and that `nymSK`, the identity `nymEID` and `nymRH` commit
  to its attributes 0, 2 and 3. It is bound to the issuer public key. The verifier fixes the
  committed indices itself, and takes `nymEID` from the identity, never from the signature.
- `sok` is a signature of knowledge of the opening of `nymSK`, bound to the signed message
  (with a domain separator).

Verification checks the SoK and then the presentation, on every call. The signature must
re-encode to the same bytes (`encoding/asn1` alone accepts extra elements in a sequence), the nyms
must have the length of a nym, and the proofs must be non-empty and bounded.

Who can sign under a `nymEID`: whoever has a credential of the issuer with that enrollment id
**and** the opening of `nymEID` (the presentation proves knowledge of both), plus `usk` and the opening
of a `nymSK` bound to them. That is the wallet that made the pseudonym. The opening of `nymEID` is
therefore never revealed, not even to the auditor (see [Audit info](#audit-info)): otherwise the issuer,
which can make itself a credential for any enrollment id, or another wallet with the same enrollment
id, would only need an audit info to sign for the victim. `TestAuditorCannotSign` checks this.

The revocation handle is bound per signature: `nymRH` is part of the signature, so a future
revocation check must look at `nymRH` of every signature. In particular the audit info does **not**
attest the RH of later signatures: it proves the RH of the presentation it carries. A user who holds
the opening of a `nymEID` and a second credential with the same enrollment id but another RH (for
example a revoked one, or one moved over from another wallet) can sign under the same `nymEID` with
that RH. An auditor that cares compares `nymRH` of an audited signature with `nym_rh` of the audit info.

Nothing is cached, because the design is **one signature per pseudonym**: a wallet takes a fresh
pseudonym (`Identity`) for each signature. This is also what keeps signatures unlinkable, since two
signatures under the same pseudonym share their nyms. A pseudonym that signs again still verifies,
but each verification pays the full presentation check again (the dominant cost, see
[Benchmarks](#benchmarks)), and the signatures are linkable.

### Audit info

`AuditInfo` is JSON:
`{eid, rh, nym_eid, nym_sk, nym_rh, opening_rh, eid_proof, enc_opening_eid, pres}`, where `pres` is
the presentation generated with the pseudonym (the one its signatures carry) and `nym_sk`/`nym_rh`
its other nyms. `EnrollmentID()` and `RevocationHandle()` return the plaintext.

- `eid_proof` proves that `nym_eid` commits to `H(eid)` without revealing the opening: a proof of
  knowledge of a short `r` with `nymEID - Acom_0 * H(eid) = Acom_r * r` (`anoncreds.EqOpeningVerify`,
  see [the scheme description](identity-lazer-anoncreds.md#63-proof-that-a-nym-commits-to-a-public-attribute)).
  It is bound to the issuer public key, the nym and the attribute.
- `opening_rh` opens `nym_rh` in the clear: `nymRH` is not a signing key.
- `enc_opening_eid` is the opening of `nymEID`, `nonce || AES-256-GCM(opening)` under a key derived
  from the wallet secret (`HKDF-SHA256(usk || user state, SHA-256(ipk), "panurus/lazer/auditopening/v1")`,
  `auditKey` in `schema.go`), with `domain || nymEID || H(eid) || H(rh)` as additional data. Only the
  wallet that made the pseudonym decrypts it, to derive pseudonyms with the same `nymEID` (see below).
  Matching ignores it.

An identity is only checked syntactically when it is deserialized, so matching it against an audit
info must also prove that it has a credential: otherwise anyone could commit to a victim's EID, without
any credential, and have the auditor accept the pseudonym as the victim's. Matching therefore checks
that the identity is `nym_eid`, verifies `eid_proof`, checks that `opening_rh` opens `nym_rh` to the
RH, and verifies `pres` against `(nym_sk, identity, nym_rh)` under the issuer public key
(`presVerifier.verifyPres`, the same check as for signature presentations). This is done by the paths
bound to a `Deserializer`, which has the issuer public key:

- `Deserializer.MatchIdentity` (used by the token services on issue and transfer, by the auditor and
  by `RegisterRecipient`);
- `Deserializer.GetAuditInfoMatcher`, whose matcher is bound to the issuer public key;
- `Deserializer.DeserializeAuditInfo`, which requires the identity and matches it;
- `Deserializer.Info`.

`AuditInfo` itself has no `Match` method: without the issuer public key the presentation cannot be
checked, and a credential-less pseudonym would match. `AuditInfoDeserializer` (no issuer public
key, meant for the EID/RH deserializer) only reads the enrollment id and revocation handle out of an
audit info. It does not match anything and returns a value that is not a `driver.Matcher`.
Callers must have matched the identity through a `Deserializer` first, as the wallet and auditor
flows of Panurus do.

The decoder is canonical: the input must be exactly what `Bytes()` (`json.Marshal`) produces, so
unknown, duplicate or case-variant members and trailing data are rejected. Nyms, the opening and the
ciphertext must have their fixed lengths, `eid_proof` at most `anoncreds.MaxLenSok()` and `pres` at
most `anoncreds.MaxLenPres()` bytes.

`KeyManager.Identity(ctx, auditInfo)` with a reference audit info of the same wallet decrypts its
opening of `nymEID` and reuses it and the opening of `nymRH`, so the new pseudonym **is the same
identity** (the same `nymEID`, as with `idemixnym`), with a fresh `nymSK` and presentation. The audit
info of another wallet, even one with the same enrollment id and revocation handle, is refused. Since the
identity bytes are the same, the identity store may keep the signer info and audit info of any of these
descriptors (depending on the backend, the first or the last written); every combination is valid: each
audit info matches the identity and each signer info signs for it.

An opening reveals the committed value: a nym is `Acom * (enc(attribute), r)`, linear in the
attribute, and `Acom` is public, so given a nym and its opening anyone solves for the attribute
(`TestRegressionOpeningRevealsAttribute` does it). For `nymRH` in the audit info this is by design: whoever
holds it learns the RH hash. The opening of `nymSK` is never part of the audit info, see below.

### Signer info

`IdentityDescriptor.SignerInfo` is JSON `{version, enc_opening_sk, nym_rh, pres}`. It is kept by the
identity store, and `DeserializeSigner` uses it, together with the wallet secret, to rebuild a
signer.

The identity store is not a secret store, and the opening of `nymSK` is as sensitive as `usk`:
from `nymSK` and its opening anyone recovers `usk` (see above), and with it signs for every pseudonym
of the wallet. The opening is therefore encrypted at rest:

- `enc_opening_sk` is `nonce || AES-256-GCM(opening)`, with a random 12-byte nonce;
- the key is `HKDF-SHA256(secret = usk || user state, salt = SHA-256(ipk), info = "panurus/lazer/signerinfo/v1")`,
  that is, it is derived from the wallet secret held by the key manager (see `signerInfoKey` in `schema.go`);
- the additional data is `SHA-256("panurus/lazer/signerinfo/ad/v2" || identity || nym_rh || pres)`, so a
  signer info only decrypts for its identity, and a signer info whose `nym_rh` or `pres` was altered in the
  store is refused instead of producing signatures that do not verify.

`DeserializeSigner` decodes the signer info strictly (canonical JSON, `version` 2, fixed lengths,
presentation length), decrypts the opening and recomputes `nymSK` from `usk` and the opening. A signer
info that does not decrypt (another wallet, another identity, tampering) or has another version is
refused. The plaintext opening only lives in memory, in the returned `Signer`.

What ties a pseudonym to the wallet is the presentation, bound to the credential of the wallet.
The decryption only establishes that this key manager can sign for it.

### Wallet secret and configuration

On disk, a wallet is the same layout as Idemix (`lazergen` writes it):

```
<dir>/msp/IssuerPublicKey
<dir>/user/SignerConfigFull   credential and secret
<dir>/user/SignerConfig       credential only: a verify-only (remote) wallet
```

The key manager provider (`NewKeyManagerProvider`) loads the configuration, stores the secret
(`usk` and the masking randomness) in the `idriver.Keystore` under a SKI, and removes it from
`IdentityConfiguration.Raw`, which keeps only the SKI. It then wraps the key manager with the
Idemix identity cache, so pseudonyms are pre-generated in the background. Call `Close()` on the
key manager when done. With `ignoreVerifyOnlyWallet` unset, `SignerConfig` is used and the
key manager is remote: it verifies but cannot create identities.

`NewConfigWithIPK(ipk, dir, ...)` reads the wallet from `<dir>/user`, or from `<dir>/msp/user` if
`<dir>` has no `user` directory. It fails if neither exists, and if a signer configuration exists but
cannot be read or decoded (it never falls back to the other location then). A `user` directory
without signer configuration is the intended verify-only (remote) wallet. Signer configurations and
`IdentityConfiguration.Raw` are decoded strictly: unknown, duplicate or case-variant members and
trailing data are rejected.

`IssueSignerConfig` resamples `usk` until the encoded attributes satisfy the norm bound of the
issuance proof. A random `usk` almost always does, so it gives up after 1000 attempts: then the
encodings of the OU, role, EID and RH alone exceed the bound, and one of them must change.

`KeyManager.Close` (and `WrappedKeyManager.Close`) is idempotent and safe to call concurrently with
the other methods. Afterwards `Identity` and the signer deserializers fail, as do verifications
with the key manager's verifier. `IsRemote` does not change. The identity cache cannot wait for its
provisioning goroutine, so `WrappedKeyManager.Close` cancels it: a generation waiting for a prover
fails with "key manager closed", one in progress completes, and the goroutine exits. Identities
still in the cache are not served.

## Concurrency and memory

Generating an identity is mostly generating its presentation, a $P_2$ proof. lazer converts the operands of a
product to the CRT domain in place, even those it only reads, and a prover state holds the statement and the
witness. The module therefore separates what the process shares, read-only, from what each call owns:

- **Public matrices, per process.** The public matrices of each proof system ($P_1$, $P_2$ and popen, the last
  shared by the SoK and the equality-of-opening proof) are expanded once, at the first use of the `anoncreds`
  package, and converted to the CRT domain (`lin_pp_init`, lazer patch 0011). Every prover and verifier borrows
  them (`lin_prover_init_pp`, `lin_verifier_init_pp`) and only uses them as the left operand of products, so
  nothing writes them. They take about 20 MB per process.
- **Provers, per call.** Each presentation sets up its own $P_2$ prover from the shared matrices (about 3 MB) and
  clears it at the end, which wipes the witness. The issuance prover ($P_1$) lives only inside `MaskAttrs`. A wallet
  holds no prover state between calls.
- **Wallet state, one per key manager.** The `anoncreds.User` of a key manager (public key, masking randomness and
  attributes) is behind a read-write lock: `PresGen` and `CredVerify` only read it, working on private copies of
  whatever a product converts, and run concurrently; `MaskAttrs`, `Import`, `Export` and `Clear` are exclusive and
  wait for the presentations in progress.
- **Verifiers.** A C verifier state holds the statement being checked and is not shared; each `Deserializer`
  keeps a pool of up to 4 idle verifiers (`presVerifier` in `identity.go`), each about 2.7 MB on top of the shared
  matrices.
- **Prover limiter.** A `ProverLimiter` (`prover.go`) bounds the presentations generated at the same time.
  `KeyManagerProvider` shares one limiter of `DefaultProverLimit()` = `min(GOMAXPROCS, 4)` among all the key
  managers it creates, so the number of concurrent provers does not grow with the number of wallets; a key manager
  built with `NewKeyManager` has its own limiter unless `WithProverLimiter` is passed. GOMAXPROCS alone would not
  bound the work, because a goroutine in a cgo call does not count against it.
- **Cancellation and close.** `Identity` waits for a free slot and fails if its context is done or the key manager
  is closed first. `Close` (like `User.Clear`) waits for all the presentations in progress before it releases the
  wallet state.

Concurrent proofs rely on two properties of the patched lazer: the discrete Gaussian sampler keeps no global state
(patch 0004), and nothing writes the shared matrices (patch 0011, see its
[invariant](identity-lazer-patches.md#0011-lin-shared-public-params)). Go's race detector does not see the C code,
so the tests check them directly: `TestUserConcurrentUse` compares a digest of the shared matrices before and after
concurrent proving and verification, and `TestSharedPublicParamsSameProofs` checks that proofs with fixed coins are
identical with shared and with private matrices.

## Benchmarks

`make lazer-bench` prints the sizes of identities, signatures, audit infos and signer infos and benchmarks
generating identities, signing and verifying. The `TestParallelBenchmarkLazer*` tests in `km_bench_test.go`
measure throughput with concurrent workers, and `BenchmarkKmIdentityParallel` generates identities with one key
manager from parallel goroutines:

```bash
cd x/token/services/identity/lazer
go test -run 'TestParallelBenchmarkLazer' -v . -args -duration=60s -workers=1,10
go test -run '^$' -bench 'BenchmarkKmIdentity' -cpu 1,2,4,8 .
go test -run '^$' -bench '^BenchmarkLazer$' -benchtime=60s .
go test -run '^$' -bench '^BenchmarkMatchIdentity$' -benchtime=700x -count=3 .
```

Verifiers do not cache presentation checks, so every verification and every audit match pays for a full
presentation check; `BenchmarkMatchIdentity` measures audit matching. Run the benchmarks on an idle machine and
compare runs of the same build only. Sizes: identity 225 B, signature about 53.4 KB, audit info about 75 KB, signer
info about 51 KB (the last three base64 in JSON or DER, dominated by the presentation).

## Building and testing

The CBDC credential scheme (`anoncreds/anoncreds.c`, `anoncreds/anoncreds.h`) is part of this module and is
compiled by cgo together with the Go package. It uses the lazer library (`lazer.h`, `liblazer.a`), which
`make lazer-install` builds from the lazer repository with the local patches and installs; lazer itself contains
no CBDC code. The build needs `gcc`/`clang`, `make`, `cmake`, `unzip`, `patch`, `git`, GMP and MPFR, and works on
linux/amd64 and on arm64 (for example macOS on Apple silicon).

```bash
# apt-get install -y build-essential cmake unzip patch git libgmp-dev libmpfr-dev
# brew install cmake gmp mpfr

make lazer-install                     # fetch lazer (LAZER_REPO at LAZER_COMMIT), patch, build, install into ./.lazer
make lazer-install LAZER_SRC=~/lazer   # or copy, patch and build a local checkout (it is not modified)
make lazer-test                        # go test -race in the module
make lazer-test-short                  # without the race detector
make lazer-bench                       # sizes and benchmarks
make lazer-clean                       # remove ./.lazer
```

Variables (see `lazer.mk`): `LAZER_REPO`, `LAZER_COMMIT`, `LAZER_REF`, `LAZER_SRC`, and `LAZER_CFLAGS`, which is
empty by default so that lazer's own optimization and architecture flags are used (overriding them, for example
with `-O2`, is not recommended, see [Open issues](#open-issues)). Everything is installed under `./.lazer`
(git-ignored): `include/lazer.h`, `include/src/moduli.h`, `lib/liblazer.a`, `lib/libhexl.a` and `REVISION`. The
scheme is compiled against that `lazer.h`, so it must be built with the same lazer that built `liblazer.a`. The cgo
directives of the `anoncreds` package refer to that directory, so after `make lazer-install` plain `go test ./...`
in `x/token/services/identity/lazer` works too. Without it the module does not compile, which is why it is not part
of `GO_MODULES` and not covered by `make unit-tests`.

**CI.** `lazer-tests.yml` runs `make lazer-install` and `make lazer-test` on Linux amd64 (`ubuntu-latest`) and
arm64 (`ubuntu-24.04-arm`). The `fuzz-lazer` job of `nightly-fuzz.yml` runs the fuzz targets of the module. Neither
caches `./.lazer`: lazer is built with `-march=native` (and HEXL picks AVX-512 when present), so a build from one
runner may not run on another. CI builds Linux only; macOS (arm64, Homebrew) is built and tested locally, not in
CI.

### lazer revision

By default `make lazer-install` builds the pinned commit `LAZER_COMMIT` of `LAZER_REPO`
(`https://github.com/lazer-crypto/lazer.git`), `b958e247a2fdb9593b3c25c9603c6c5305c19420` on upstream `main`. The
arm64/macOS support and the `int_mod` fix of [lazer-crypto/lazer#33](https://github.com/lazer-crypto/lazer/pull/33),
an open pull request, are carried as patch 0000 rather than built from the fork the pull request comes from: a
commit that only exists on a fork can become unfetchable if its branch is force-pushed or deleted. When the pull
request is merged, patch 0000 is deleted and `LAZER_COMMIT` set to the merge commit.

Two variables build something else:

- `LAZER_REF=<branch or tag>` builds that ref of `LAZER_REPO` instead of the pinned commit.
- `LAZER_SRC=<dir>` builds a copy of a local checkout.

Either way the local patches are applied, starting with 0000, so a source that already contains
lazer-crypto/lazer#33 (the `adecaro/lazer` fork, the pull-request branch, or upstream `main` once it is merged)
fails to patch; build it without patch 0000.

### Local patches

Fixes to lazer that Panurus needs live in this repository as patch files in
`x/token/services/identity/lazer/patches/`. [Local patches to lazer](identity-lazer-patches.md) describes each of
them in detail: the defect, why it matters for the scheme, the fix and its compatibility.

| Patch | Summary |
|---|---|
| `0000-lazer-pr33-arm64-int-mod` | [lazer-crypto/lazer#33](https://github.com/lazer-crypto/lazer/pull/33): `int_mod` with zero-padded moduli, arm64 and macOS builds |
| `0001-coder-bounds-checks` | bounded decoders and encoders, so that crafted proofs, credentials and masked attributes cannot cause out-of-bounds reads |
| `0002-blindsig-no-exit` | Falcon keys are validated with an error instead of `exit()`; key temporaries are wiped |
| `0003-zeroize-secrets` | non-elidable wiping; the prover wipes its witness, copies and expanded randomness |
| `0004-grandom-sampler-state` | discrete Gaussian sampler without global state and with one random stream per vector (zero-knowledge of presentations, concurrent proofs) |
| `0005-lin-prover-wipe-witness` | `lin_prover_wipe_witness` for reused prover states |
| `0006-tbox-fiat-shamir-h-z3-z4` | [#28](https://github.com/lazer-crypto/lazer/issues/28): $h$, $\mathbf{z}_3$, $\mathbf{z}_4$ are absorbed into the Fiat–Shamir transcript before the challenges that bind them |
| `0007-tbox-l2-equation-domains` | [#25](https://github.com/lazer-crypto/lazer/issues/25): every exact-$\ell_2$ equation gets its own challenges, so each block bound is enforced |
| `0008-tbox-accumulator-challenge-pairs` | [#24](https://github.com/lazer-crypto/lazer/issues/24): independent challenges for each of the $\lambda/2$ accumulator pairs |
| `0009-quad-verify-bound-z21` | [#26](https://github.com/lazer-crypto/lazer/issues/26): the verifier bounds all of $\mathbf{z}_2=(\mathbf{z}_{21},\mathbf{z}_{22})$ |
| `0010-prover-zk-z2-rejection-beta-signs` | [#31](https://github.com/lazer-crypto/lazer/issues/31): one-time rejection for $\mathbf{z}_2$ and a uniform $\beta_4$ sign (prover only) |
| `0011-lin-shared-public-params` | `lin_pp_t`: public matrices expanded and CRT-converted once, borrowed read-only by provers and verifiers |

Patches 0006–0008 change the transcript: proofs made with and without them do not verify against each other.
Credentials, nyms, parameters and proof sizes do not depend on the patches.

**How they are applied.** `make lazer-install` (in `lazer-fetch`) applies the patches in lexical order to the copy
of the sources in `./.lazer/build`, for a fetched revision and for `LAZER_SRC` alike; a `LAZER_SRC` checkout itself
is never modified. Patching is strict (`patch --forward --strip=1 --fuzz=0`): a patch that does not apply exactly,
or is already applied, fails the build, so a patch that does not fit a lazer revision cannot be skipped or
misapplied silently.

**What was built.** `make lazer-install` records the build in `./.lazer/REVISION`: for a fetched build the
repository, ref and commit; for `LAZER_SRC` the source directory and the checkout's `HEAD`, marked
`(with uncommitted changes)` if the working tree is dirty; then the applied patches with their sha256.
`make lazer-check`, run by `lazer-test`, `lazer-test-short` and `lazer-bench`, prints it and fails unless the
installed lazer was built with exactly the patches in the directory, so a build with a different patch set cannot
be used by mistake.

**Changing a patch.** Apply the series to a pristine checkout of `LAZER_COMMIT`, edit, and regenerate the patch with
`git diff` or `git format-patch --no-signature` (paths `a/...`, `b/...` relative to the lazer root; no signature, so
that the sha256 of a patch does not depend on the git version). Patches must not touch the Falcon or Intel HEXL
sources under `third_party/`: lazer's own Makefile extracts them and applies `src/falcon.patch` and `src/hexl.patch`
(added by patch 0000) at build time, after `lazer-fetch`, so a change to them goes into those files. When moving to
a new lazer revision, drop the patches that are merged upstream and refresh the others.

### Tests

The module tests cover, among others: identity and signature round trips, unlinkability, signatures under the wrong
identity or issuer, swapped nyms, audit matching and reuse, signers of another wallet, remote wallets, invalid
credentials, forgery without a credential (`TestForgeryWithoutCredential`), malformed inputs, concurrent identity
generation and `Close`, and the provider flow. Further tests pin the documented properties: the sizes of
identities, signatures, audit and signer infos (`TestSizesWithinStatedBounds`), the shared limiter of a provider and
its default (`TestKeyManagerProviderSharesOneLimiter`, `TestDefaultProverLimit`), the verifier pool
(`TestVerifierPoolKeepsAtMostFourIdle`), the revocation handle carried by each signature
(`TestRevocationHandleIsBoundPerSignature`) and the attribute encoding (`TestAttributeEncodingBoundAndInjective`).
`TestKnownAnswerProofs` (`anoncreds`) pins the proof transcript with fixed coins: a build whose patches change the
transcript fails it. The `lazer-tests` workflow also checks that `make lazer-check` refuses an install built from a
different patch set.

The `anoncreds` package has regression tests for crafted inputs aimed at the decoders, malformed issuer keys, misuse
of the API (export before masking, use after `Clear`, a SoK for another attribute), the zero-witness forgery
(`TestZeroWitnessForgery`), the parameter check, the Gaussian sampler and concurrent use of one `User`. Fuzz targets
cover every decoder of untrusted input: `FuzzSokVerify`, `FuzzEqOpeningVerify`, `FuzzPresVer`, `FuzzIssue`,
`FuzzCredVerify`, `FuzzImport`, `FuzzCommitWithOpening` and `FuzzNewVerifier` in `anoncreds`, and
`FuzzDeserializers`, `FuzzDeserializeAuditInfo`, `FuzzSignerInfoDecode` and `FuzzNewConfigFromRaw` in the module
root. The `anoncreds` fuzz targets and its out-of-bounds regression tests place their inputs right before an
inaccessible page (`newGuard` in `guard_test.go`), so that a read past the input crashes instead of passing
silently. The `fuzz-lazer` job of the nightly fuzz workflow runs all of them:

```bash
cd x/token/services/identity/lazer
go test ./anoncreds -run='^$' -fuzz='^FuzzPresVer$' -fuzztime=20s
```

### Generating test material

```bash
cd x/token/services/identity/lazer
go run ./cmd/lazergen ca-keygen -out /tmp/ca
go run ./cmd/lazergen signerconfig -ca /tmp/ca -out /tmp/alice -eid alice -ou org1 [-role member] [-rh <handle>]
```

### Proof system parameters

`anoncreds/anoncreds_params.h` is generated by lazer's `scripts/lin-codegen.sage` (needs SageMath) from the three
files in `anoncreds/params/` (`anoncreds_p1_params.py`, `anoncreds_p2_params.py`, `anoncreds_popen_params.py`):

```bash
cd <lazer>/scripts
for p in p1 p2 popen; do sage lin-codegen.sage <panurus>/x/token/services/identity/lazer/anoncreds/params/anoncreds_${p}_params.py; done \
  > <panurus>/x/token/services/identity/lazer/anoncreds/anoncreds_params.h
```

The parameters must agree with `anoncreds.h` and the witness layout of `anoncreds.c`: dimensions (`CBDC_COM_ROWS`,
`CBDC_MAXCOM`, the witness length and the rows of each statement), the binary part (τ) and every squared ℓ2 bound
that the C code checks before proving (`CBDC_L2SQR_BND_*`: 109², 91², 34034726, 64², 39²). `cbdc_params_check`
compares all of them with the generated `l2Bsqr`, `n`, `Es` and `Ps` tables; the library aborts at initialization if
they differ, and `TestParamsMatchConstants`/`TestParamsCheck` fail. lazer's Python reference
(`python/cbdc/cbdc.py`) has its own copy of these files and is not part of this module.

## Layout

| File | Content |
|---|---|
| `schema.go` | attribute layout, domain separators |
| `config.go` | configuration and on-disk layout |
| `identity.go` | identity and signature encodings, verifier pool |
| `km.go`, `kmp.go` | `KeyManager`, `KeyManagerProvider`, identity cache wrapper |
| `prover.go` | `ProverLimiter`, see [Concurrency and memory](#concurrency-and-memory) |
| `deserializer.go`, `audit.go` | verifier/matcher deserializers, `AuditInfo` |
| `strictjson.go` | strict and canonical JSON decoding of audit infos, signer infos and configurations |
| `issuer.go`, `cmd/lazergen` | issuer key generation and wallet issuance for tools and tests |
| `anoncreds/anoncreds.c`, `anoncreds/anoncreds.h` | the CBDC credential scheme in C, built on lazer (derived from lazer, MIT, see `anoncreds/LICENSE.lazer`) |
| `anoncreds/anoncreds.go` | cgo binding of the scheme, with tests and a demo in `anoncreds/cmd/anoncreds_demo` |
| `anoncreds/anoncreds_params.h`, `anoncreds/params/` | proof system parameters: the generated header and the `.py` files it is generated from |
| `patches/` | the local patches to lazer, see [Local patches](#local-patches) |

## Open issues

1. **Unforgeability argument.** The credential equation has a public non-zero target $\mathbf{c}_0$, derived from
   the issuer key, so that the all-zero witness does not satisfy it (see
   [the scheme description, §10.1](identity-lazer-anoncreds.md#101-unforgeability-of-credentials)). A formal
   reduction for the credential scheme with this target is still missing.
2. **Commitment binding.** The commitment key is 2x11 over Z_12289[X]/(X^64+1). A rough estimate gives about 80-bit
   core-SVP for finding two openings (collision norm up to 150) and about 120 bit for extracting an opening. A break
   lets a user open `nymEID`/`nymRH` to a false enrollment id or revocation handle, or prove a false enrollment id
   with `eid_proof` (audit evasion); it does not allow linking. Since the identity is `nymEID` alone, extracting an
   opening of a victim's `nymEID` would also let a holder of a credential with the same enrollment id sign for it.
   Before any production use, run the lattice estimator and, if needed, increase `CBDC_COM_ROWS` in
   `anoncreds/anoncreds.h` and regenerate the `popen` and `p2` parameters.
3. **Size.** A signature is about 53.4 KB (the identity is 225 B).
4. **Patched lazer.** The build depends on the local patches; none of them is merged upstream. The fixes for #28,
   #25 and #26 were checked by code review, not by negative tests with a cheating prover, and #24 has no dedicated
   test. Entry points of `liblazer.a` that this module does not use keep the upstream defects (see
   [Scope and testing of 0006–0010](identity-lazer-patches.md#scope-and-testing-of-00060010)).
5. **Secrets in memory.** Secrets are wiped on `Clear` and after use, in C and in the Go wrapper, but copies made by
   the Go runtime (for example when growing slices, or by the garbage collector) cannot be wiped, and memory is not
   locked against swapping.
6. **Build flags.** lazer's own `tests/abdlop-test` has a randomized check (`tests/abdlop-test.c:146`, marked
   "sometimes fails" upstream) that a perturbed commitment is rejected. In local runs (an observation, not a CI
   measurement), on amd64 it fails in a few percent of the
   runs with lazer's default `-O3`, but in about 90% with `-O2` (gcc). On arm64 with clang, `-O2` and `-O3` give the
   same low rate, and an ASan/UBSan build of lazer found no memory errors. The cause is not understood; the tests of
   this module pass in both builds, but use the default flags and do not rely on other flag sets until it is.

## Not done

Driver, public-parameter (`IdemixIssuerPublicKeys` equivalent) and integration (`nwo`) wiring, a SKI cleanup
provider, revocation (the RH is committed but not checked against a list), a per-message presentation signature
mode, and sending the presentation once per transaction (with one signature per pseudonym each signature carries
its own presentation, about 36.9 KiB).
