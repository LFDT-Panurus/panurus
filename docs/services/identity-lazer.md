# Lazer: post-quantum anonymous identities

The `lazer` identity type is an experimental alternative to Idemix/IdemixNym for
**owner** wallets. It provides pseudonymous, unlinkable identities with auditability, like
Idemix, but is built on a lattice-based anonymous credential
([lazer](https://github.com/lazer-crypto/lazer), CBDC scheme) instead of pairings.

!!! warning "Status: experimental"
    The module is not wired into any token driver, there is no revocation, and the
    commitment parameters have not been through a lattice-estimator review
    (see [Open issues](#open-issues)). Do not use it in production.

Code: `x/token/services/identity/lazer` (a separate Go module, because it uses cgo).

| Item | Value |
|---|---|
| Identity type | `LazerIdentityType = 7` (`"lazer"`), see `token/driver/wallet.go` |
| Interfaces | `membership.KeyManager`, `membership.KeyManagerProvider`, `idriver.ProbeFreeSignerDeserializer`, verifier/matcher deserializer, `idriver.AuditInfo` |
| Anonymous | yes |
| Auditable | yes (enrollment id and revocation handle) |

## Scheme

For a full technical description of the underlying cryptography (lattices, issuance, proofs, security) see
[The Lazer Anonymous Credential Scheme](identity-lazer-anoncreds.md).

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

The identity is what is stored in the token's owner field: an ASN.1 sequence of three
commitments, `{nymSK, nymEID, nymRH}`. Each is `Acom * (attribute, r)` with fresh
randomness `r`, so two pseudonyms of the same wallet share nothing. The identity is small
(about 0.7 KB) because no proof is part of it. It is wrapped in a `TypedIdentity` of type 7.

### Signatures

A signature is the ASN.1 sequence `{pres, sok}`:

- `pres` is a zero-knowledge presentation of the credential, with all attributes hidden. It proves
  that a credential of the issuer exists and that the three nyms commit to attributes 0, 2 and 3.
  It is bound to the issuer public key. The verifier fixes the committed indices itself and never
  reads them from the identity.
- `sok` is a signature of knowledge of the opening of `nymSK`, bound to the signed message
  (with a domain separator).

Signing uses `nymSK`, not `nymEID`: the auditor knows the openings of the EID and RH nyms
(see below), so it must not be able to sign. Verifiers cache the result of the (expensive)
presentation check per identity, so repeated signatures of one pseudonym cost one SoK check.

### Audit info

`AuditInfo` is JSON: `{eid, rh, opening_eid, opening_rh, pres}`, where `pres` is the presentation
generated with the pseudonym (the one its signatures carry). `EnrollmentID()` and `RevocationHandle()`
return the plaintext.

An identity is only checked syntactically when it is deserialized, so matching it against an audit
info must also prove that it has a credential: otherwise anyone could commit to a victim's EID and
RH, without any credential, and have the auditor accept the pseudonym as the victim's. Matching
therefore recomputes `nymEID` and `nymRH` from the values and openings, and verifies `pres` against
the three nyms under the issuer public key (`presVerifier.verifyPres`, cached like signature
presentations). This is done by the paths bound to a `Deserializer`, which has the issuer public key:

- `Deserializer.MatchIdentity` (used by the token services on issue and transfer, by the auditor and
  by `RegisterRecipient`);
- `Deserializer.GetAuditInfoMatcher`, whose matcher is bound to the issuer public key;
- `Deserializer.DeserializeAuditInfo`, which requires the identity and matches it;
- `Deserializer.Info`.

`AuditInfo` itself has no `Match` method: without the issuer public key a match could only check
the openings, which a credential-less pseudonym passes. `AuditInfoDeserializer` (no issuer public
key, meant for the EID/RH deserializer) only reads the enrollment id and revocation handle out of an
audit info. It does not match anything and returns a value that is not a `driver.Matcher`.
Callers must have matched the identity through a `Deserializer` first, as the wallet and auditor
flows of Panurus do.

The decoder is canonical: the input must be exactly what `Bytes()` (`json.Marshal`) produces, so
unknown, duplicate or case-variant members and trailing data are rejected. The openings must have
the length of an opening and `pres` at most `anoncreds.MaxLenPres()` bytes.

`KeyManager.Identity(ctx, auditInfo)` with a reference audit info reuses its openings, so the new
pseudonym has the same `nymEID` and `nymRH`, as in Idemix. Its presentation is fresh, so a new
pseudonym is matched with its own audit info, not with the reference one.

An opening reveals the committed value: a nym is `Acom * (enc(attribute), r)`, linear in the
attribute, and `Acom` is public, so given a nym and its opening anyone solves for the attribute
(`TestRegressionOpeningRevealsAttribute` does it). For the audit info this is by design: whoever
holds it learns the EID and RH hash, which is what the auditor needs. The opening of `nymSK` is
never part of the audit info, see below.

### Signer info

`IdentityDescriptor.SignerInfo` is JSON `{version, enc_opening_sk, pres}`. It is kept by the
identity store, and `DeserializeSigner` uses it, together with the wallet secret, to rebuild a
signer.

The identity store is not a secret store, and the opening of `nymSK` is as sensitive as `usk`:
from `nymSK` and its opening anyone recovers `usk` (see above), and with it signs for every pseudonym
of the wallet. The opening is therefore encrypted at rest:

- `enc_opening_sk` is `nonce || AES-256-GCM(opening)`, with a random 12-byte nonce;
- the key is `HKDF-SHA256(secret = usk || user state, salt = SHA-256(ipk), info = "panurus/lazer/signerinfo/v1")`,
  that is, it is derived from the wallet secret held by the key manager (see `signerInfoKey` in `schema.go`);
- the additional data is the serialized identity, so a signer info only decrypts for its identity.

`DeserializeSigner` decodes the signer info strictly (canonical JSON, `version` 1, fixed ciphertext
length, presentation length), decrypts the opening and checks that `usk` and the opening open `nymSK`
of the identity. A signer info that does not decrypt (another wallet, another identity, tampering)
or an older plaintext signer info is refused. The plaintext opening only lives in memory, in the
returned `Signer`.

What ties a pseudonym to the wallet is the presentation, bound to the credential of the wallet.
The decryption and the opening check only establish that this key manager can sign for it.

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
provisioning goroutine, so `WrappedKeyManager.Close` cancels it: a generation in progress fails
with "key manager closed" and the goroutine exits. Identities still in the cache are not served.

## Benchmarks

`make lazer-bench` prints the sizes of identities, signatures, audit infos and signer infos, and
the time of generating identities, signing and verifying. The `TestParallelBenchmarkLazer*` tests
in `km_bench_test.go` measure throughput with concurrent workers:

```bash
cd x/token/services/identity/lazer
go test -run 'TestParallelBenchmarkLazer' -v .
```

Verifiers cache presentation checks, so benchmarks that reuse identities measure the cached
cost; `BenchmarkMatchIdentityCold` measures the uncached one.

## Building and testing

The CBDC credential scheme itself (`anoncreds/anoncreds.c`, `anoncreds/anoncreds.h`) is part of this module and is compiled
by cgo together with the Go package. It uses the lazer library (`lazer.h`, `liblazer.a`) from the lazer
repository, which `make lazer-install` builds and installs; lazer itself contains no CBDC code. It needs
`gcc`/`clang`, `make`, `cmake`, `unzip`, `patch`, `git`, GMP and MPFR, and builds on linux/amd64
and on arm64 (e.g. macOS on Apple silicon).

```bash
# apt-get install -y build-essential cmake unzip patch git libgmp-dev libmpfr-dev
# brew install cmake gmp mpfr

make lazer-install                     # fetch lazer (LAZER_REPO at LAZER_COMMIT), patch, build, install into ./.lazer
make lazer-install LAZER_SRC=~/lazer   # or copy, patch and build a local checkout (it is not modified)
make lazer-test                        # go test -race in the module
make lazer-test-short                  # without the race detector
make lazer-bench                       # sizes and timings
make lazer-clean                       # remove ./.lazer
```

Variables (see `lazer.mk`): `LAZER_REPO`, `LAZER_COMMIT`, `LAZER_REF`, `LAZER_SRC`, and `LAZER_CFLAGS`, which is empty by default so that lazer's own optimization and architecture flags are used (overriding them, e.g. with `-O2`, is not recommended, see below). Everything
is installed under `./.lazer` (git-ignored): `include/lazer.h`, `include/src/moduli.h`,
`lib/liblazer.a`, `lib/libhexl.a`. The scheme is compiled against that `lazer.h`, so it must be built with the
same lazer that built `liblazer.a`. The cgo directives of the `anoncreds` package refer to that
directory, so after `make lazer-install` plain `go test ./...` in
`x/token/services/identity/lazer` works too. Without it the module does not compile, which
is why it is not part of `GO_MODULES` and not covered by `make unit-tests`.

### lazer revision

By default `make lazer-install` builds a pinned lazer commit, `LAZER_COMMIT` in `lazer.mk`. It is currently
`cc536f79c73a1cce6c8de5120564b15c809d1caa`, the head of
[lazer-crypto/lazer#33](https://github.com/lazer-crypto/lazer/pull/33). That PR adds arm64/macOS support and fixes
`int_mod` for zero-padded multi-limb moduli. Until it is merged, `LAZER_REPO` points at the fork the PR comes from
(`https://github.com/adecaro/lazer.git`). A commit that only exists on a fork can become unfetchable if that branch is
force-pushed or deleted. Once the PR is merged, set `LAZER_REPO` back to `https://github.com/lazer-crypto/lazer.git`
and `LAZER_COMMIT` to the merge commit.

There are two ways to build something else:

- `LAZER_REF=<branch or tag>` builds that ref of `LAZER_REPO` instead of the pinned commit.
- `LAZER_SRC=<dir>` builds a copy of a local checkout.

#### Local patches

Fixes to lazer that Panurus needs are not pushed upstream first: they live in this repository as patch files in
`x/token/services/identity/lazer/patches/`. `make lazer-install` (in `lazer-fetch`) applies them in lexical order
to the copy of the sources in `./.lazer/build`, for a fetched revision and for `LAZER_SRC` alike; a `LAZER_SRC`
checkout itself is never modified. Patching is strict (`patch --forward --strip=1 --fuzz=0`): a patch that does
not apply exactly, or was already applied, fails the build, so a patch that no longer fits a new lazer revision
cannot be silently skipped or misapplied.

| Patch | What it does |
|---|---|
| `0001-coder-bounds-checks.patch` | Bounds for `src/coder.c`: `coder_dec_begin_bnd`/`coder_enc_begin_bnd` start a coder that never reads or writes past the buffer; an access that would sets a sticky error (`coder_get_err`) and decoding fails. Every read and write path is covered (bytes, fixed-width, hints and gaussian unary codes, end marker, sparse vectors). `lin_verifier_verify_ctx_bnd` and `lin_prover_prove_ctx_bnd` (`src/lin-proofs.c`) use them for proofs. Without it, a crafted proof, presentation, credential or masked attributes made the decoders read past the end of the input. |
| `0002-blindsig-no-exit.patch` | `falcon_decode_pubkey_chk`, `falcon_preimage_sample_chk` and `falcon_check_keypair` (`src/blindsig.c`) validate Falcon keys and return an error instead of calling `exit()`; secret temporaries are wiped. |
| `0003-zeroize-secrets.patch` | `bytes_clear` cannot be optimized away, `poly_wipe`/`polyvec_wipe`/`polymat_wipe` zero secrets before they are freed, and the LNP prover wipes its witness, its copies and its expanded coins. |

To change a patch, apply the series to a pristine checkout of `LAZER_COMMIT`, edit, and regenerate the patch
with `git diff` (paths `a/src/...`, `b/src/...`). Patches must not touch the Falcon sources under
`third_party/`: lazer's own Makefile extracts them and applies `src/falcon.patch` at build time, after
`lazer-fetch`. When moving to a new lazer revision, drop the patches that are merged upstream and refresh the
others.

`make lazer-install` records what it built in `./.lazer/REVISION`, and `make lazer-check` (run by the test
targets) prints it. For a fetched build, this is the repository, ref and commit. For `LAZER_SRC`, it is the
source directory and the checkout's `HEAD`, marked `(with uncommitted changes)` if the working tree is dirty.
Both are followed by the applied patches with their sha256. The CI cache of `./.lazer` (`lazer-tests.yml`, and
the `fuzz-lazer` job of `nightly-fuzz.yml`) is keyed on `lazer.mk` and the patches, so changing either rebuilds
lazer.

The tests cover, among others: identity and signature round trips, unlinkability, signatures under
the wrong identity or issuer, swapped nyms, audit matching and reuse, signers of another wallet,
remote wallets, invalid credentials, malformed inputs and fuzzing, and the provider flow.

The `anoncreds` package has regression tests for crafted inputs that used to read out of bounds, malformed
issuer keys, misuse of the API (export before masking, use after `Clear`, a SoK for another attribute) and the
parameter check, and fuzz targets for every decoder of untrusted input (`FuzzSokVerify`, `FuzzPresVer`,
`FuzzIssue`, `FuzzCredVerify`, `FuzzImport`, `FuzzCommitWithOpening`, `FuzzNewVerifier`). Tests and fuzz
targets place their inputs right before an inaccessible page, so a read past the input crashes instead of
passing silently. The nightly fuzz workflow runs them, and the fuzz targets of the module root, in its
`fuzz-lazer` job:

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

`anoncreds/anoncreds_params.h` is generated by lazer's `scripts/lin-codegen.sage` (needs SageMath) from the three files in
`anoncreds/params/` (`anoncreds_p1_params.py`, `anoncreds_p2_params.py`, `anoncreds_popen_params.py`):

```bash
cd <lazer>/scripts
for p in p1 p2 popen; do sage lin-codegen.sage <panurus>/x/token/services/identity/lazer/anoncreds/params/anoncreds_${p}_params.py; done \
  > <panurus>/x/token/services/identity/lazer/anoncreds/anoncreds_params.h
```

The parameters must agree with `anoncreds.h` and the witness layout of `anoncreds.c`: dimensions (`CBDC_COM_ROWS`,
`CBDC_MAXCOM`, the witness length and the rows of each statement), the binary part (τ) and every squared ℓ2 bound
that the C code checks before proving (`CBDC_L2SQR_BND_*`: 109², 91², 34034726, 64², 39²). `cbdc_params_check`
compares all of them with the generated `l2Bsqr`, `n`, `Es` and `Ps` tables; the library aborts at initialization
if they differ, and `TestParamsMatchConstants`/`TestParamsCheck` fail. lazer's Python reference (`python/cbdc/cbdc.py`) uses its own copy of these
files and is the reference for the scheme, not part of this module.

## Layout

| File | Content |
|---|---|
| `schema.go` | attribute layout, domain separators |
| `config.go` | configuration and on-disk layout |
| `identity.go` | identity and signature encodings, verifier pool, presentation cache |
| `km.go`, `kmp.go` | `KeyManager`, `KeyManagerProvider`, identity cache wrapper |
| `deserializer.go`, `audit.go` | verifier/matcher deserializers, `AuditInfo` |
| `strictjson.go` | strict and canonical JSON decoding of audit infos, signer infos and configurations |
| `issuer.go`, `cmd/lazergen` | issuer key generation and wallet issuance for tools and tests |
| `anoncreds/anoncreds.c`, `anoncreds/anoncreds.h` | the CBDC credential scheme in C, built on lazer (derived from lazer, MIT, see `anoncreds/LICENSE.lazer`) |
| `anoncreds/anoncreds.go` | cgo binding of the scheme, with tests and a demo in `anoncreds/cmd/anoncreds_demo` |
| `anoncreds/anoncreds_params.h`, `anoncreds/params/` | proof system parameters: the generated header and the `.py` files it is generated from |

## Open issues

0. **Unforgeability argument.** A credential-less forgery present in an earlier version (and in lazer's
   `cbdc.py`) is fixed by a public non-zero target in the credential equation, see
   [the scheme description, §10.1](identity-lazer-anoncreds.md#101-unforgeability-of-credentials). Credentials
   issued before that fix must be reissued. A formal reduction is still missing.
1. **Commitment binding.** The commitment key is 2x11 over Z_12289[X]/(X^64+1). A rough estimate
   gives about 80-bit core-SVP for finding two openings (collision norm up to 150) and about
   120 bit for extracting an opening. A break lets a user open `nymEID`/`nymRH` to a false
   enrollment id or revocation handle (audit evasion); it does not allow signature forgery or
   linking. Before any production use, run the lattice estimator and, if needed, increase
   `CBDC_COM_ROWS` in `anoncreds/anoncreds.h` and regenerate the `popen` and `p2` parameters.
2. **Size.** A signature is about 52 KB.
3. **Patched lazer.** The build carries local fixes to lazer (bounds-checked decoders, non-exiting key
   decoding, zeroization, see [Local patches](#local-patches)). They should be upstreamed, and lazer's
   remaining unchecked entry points (e.g. `signer_sign`, `verifier_vrfy`, `user_sign` of its own blind
   signature, which this module does not use) still use the unbounded coder.
4. **Secrets in memory.** Secrets are wiped on `Clear` and after use, in C and in the Go wrapper, but copies
   made by the Go runtime (e.g. when growing slices, or by the garbage collector) cannot be wiped, and memory is
   not locked against swapping.
5. **Build flags.** lazer's own `tests/abdlop-test` has a randomized check (`tests/abdlop-test.c:146`, marked "sometimes fails" upstream) that a perturbed commitment is rejected. On amd64 it fails in a few percent of the runs with lazer's default `-O3`, but in about 90% with `-O2` (gcc). On arm64 with clang, `-O2` and `-O3` give the same low rate, and an ASan/UBSan build of lazer found no memory errors. The cause is not understood; the tests of this module pass in both builds, but use the default flags and do not rely on other flag sets until it is.

## Not done

Driver, public-parameter (`IdemixIssuerPublicKeys` equivalent) and integration (`nwo`) wiring,
a SKI cleanup provider, revocation (the RH is committed but not checked against a list), a
per-message presentation signature mode, and sending the presentation once per transaction.
