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
info that does not decrypt (another wallet, another identity, tampering) or of an older version is
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

### Concurrent identity generation

Generating an identity is mostly generating its presentation. lazer converts the operands of a product to
the CRT domain in place, even those it only reads, and its prover states hold the statement and the
witness, so a prover state, or anything a product reads, cannot be shared by concurrent proofs as is. The
module therefore splits what is shared from what is per call:

- The public matrices of each proof system (P1, P2, SoK/equality-of-opening) are expanded once per process
  and converted to the CRT domain (`lin_pp_init`, lazer patch 0011). Every prover and verifier borrows them
  (`lin_prover_init_pp`, `lin_verifier_init_pp`): after the conversion they are only the left operand of
  products, so nothing writes them. They take about 20 MB per process.
- The wallet state (`anoncreds.User`: the public key, the masking randomness and the attributes) is only
  read by `PresGen` and `CredVerify`, which work on copies of what a product converts, so they may run
  concurrently on one `User`. Each presentation sets up its own P2 prover from the shared matrices and
  clears it, which wipes the witness. `MaskAttrs`, `Import`, `Export` and `Clear` are exclusive: they wait
  for the presentations in progress. The issuance prover (P1) is set up and cleared inside `MaskAttrs`, so a user holds no
  prover state between calls.
- A `ProverLimiter` (`prover.go`) bounds the presentations generated at the same time. Each one needs a few
  MB while it runs (its prover and its scratch space); a wallet holds no prover state between calls.
  `KeyManagerProvider` shares one limiter of `DefaultProverLimit()` (`min(GOMAXPROCS, 4)`) among all the key
  managers it creates, so this does not grow with the number of wallets; a key manager built directly with
  `NewKeyManager` has its own unless `WithProverLimiter` is passed. GOMAXPROCS alone would not bound the
  work: a goroutine in a cgo call does not count against it.
- `Identity` waits for a free slot; it fails if its context is done or the key manager is closed first.
  `Close` waits for all the presentations in progress before it releases the wallet state.

Concurrent proofs rely on lazer patch 0004 (the discrete gaussian sampler kept its sign bits in
process-global variables) and on 0011. Go's race detector does not see the C code: the tests check that the
shared matrices are unchanged after concurrent proving (`TestUserConcurrentUse`), and that proofs with fixed
coins are the same with shared or private matrices (`TestSharedPublicParamsSameProofs`).

## Benchmarks

`make lazer-bench` prints the sizes of identities, signatures, audit infos and signer infos, and the time
of generating identities, signing and verifying. The `TestParallelBenchmarkLazer*` tests in
`km_bench_test.go` measure throughput with concurrent workers, and `BenchmarkKmIdentityParallel` generates
identities with one key manager from parallel goroutines:

```bash
cd x/token/services/identity/lazer
go test -run 'TestParallelBenchmarkLazer' -v . -args -duration=60s -workers=1,10
go test -run '^$' -bench 'BenchmarkKmIdentity' -cpu 1,2,4,8 .
go test -run '^$' -bench '^BenchmarkLazer$' -benchtime=60s .
go test -run '^$' -bench '^BenchmarkMatchIdentity$' -benchtime=700x -count=3 .
```

Verifiers do not cache presentation checks, so every verification and every audit match pays for the full
presentation check; `BenchmarkMatchIdentity` measures the audit matching. Run the benchmarks on an idle
machine and compare runs of the same build only: the numbers depend heavily on the machine and the build.
Sizes: identity 225 B, signature about 53.4 KB, audit info about 75 KB, signer info about 51 KB (the last
three base64 in JSON or DER, dominated by the presentation).

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
make lazer-bench                       # sizes and benchmarks
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

By default `make lazer-install` builds a pinned lazer commit, `LAZER_COMMIT` in `lazer.mk`, of `LAZER_REPO`
(`https://github.com/lazer-crypto/lazer.git`). It is currently `b958e247a2fdb9593b3c25c9603c6c5305c19420`, the head
of upstream `main`. [lazer-crypto/lazer#33](https://github.com/lazer-crypto/lazer/pull/33) adds arm64/macOS support
and fixes `int_mod` for zero-padded multi-limb moduli. Until it is merged, it is carried as the local patch
`0000-lazer-pr33-arm64-int-mod.patch` (see [Local patches](#local-patches)) instead of building the PR head from the
fork it comes from: a commit that only exists on a fork can become unfetchable if that branch is force-pushed or
deleted. Once the PR is merged, delete patch 0000 and set `LAZER_COMMIT` to the merge commit.

There are two ways to build something else:

- `LAZER_REF=<branch or tag>` builds that ref of `LAZER_REPO` instead of the pinned commit.
- `LAZER_SRC=<dir>` builds a copy of a local checkout.

Either way the local patches are applied, starting with 0000. A source that already contains lazer-crypto/lazer#33
(the `adecaro/lazer` fork, the PR branch, or upstream `main` once the PR is merged) therefore fails to patch; build
it without patch 0000.

#### Local patches

Fixes to lazer that Panurus needs are not pushed upstream first: they live in this repository as patch files in
`x/token/services/identity/lazer/patches/`. `make lazer-install` (in `lazer-fetch`) applies them in lexical order
to the copy of the sources in `./.lazer/build`, for a fetched revision and for `LAZER_SRC` alike; a `LAZER_SRC`
checkout itself is never modified. Patching is strict (`patch --forward --strip=1 --fuzz=0`): a patch that does
not apply exactly, or was already applied, fails the build, so a patch that no longer fits a new lazer revision
cannot be silently skipped or misapplied.

| Patch | What it does |
|---|---|
| `0000-lazer-pr33-arm64-int-mod.patch` | The two commits of [lazer-crypto/lazer#33](https://github.com/lazer-crypto/lazer/pull/33), as `git format-patch` mails: `int_mod` (`src/int.c`) strips the zero top limbs of the modulus before `mpn_sec_div_r`, which requires a non-zero top limb (the result was undefined, and wrong on arm64); and lazer builds and runs on arm64 and macOS (portable `config.h` target and `lazer-in2.h` carry helpers, ARMv8 AES in `src/aes256ctr.c`, `src/hexl.patch` for Intel HEXL, arch-dependent Makefile flags, Homebrew GMP/MPFR). On x86-64 the compiler flags and code paths are unchanged. |
| `0001-coder-bounds-checks.patch` | Bounds for `src/coder.c`: `coder_dec_begin_bnd`/`coder_enc_begin_bnd` start a coder that never reads or writes past the buffer; an access that would sets a sticky error (`coder_get_err`) and decoding fails. Every read and write path is covered (bytes, fixed-width, hints and gaussian unary codes, end marker, sparse vectors). `lin_verifier_verify_ctx_bnd` and `lin_prover_prove_ctx_bnd` (`src/lin-proofs.c`) use them for proofs. Without it, a crafted proof, presentation, credential or masked attributes made the decoders read past the end of the input. |
| `0002-blindsig-no-exit.patch` | `falcon_decode_pubkey_chk`, `falcon_preimage_sample_chk` and `falcon_check_keypair` (`src/blindsig.c`) validate Falcon keys and return an error instead of calling `exit()`; secret temporaries are wiped. |
| `0003-zeroize-secrets.patch` | `bytes_clear` cannot be optimized away, `poly_wipe`/`polyvec_wipe`/`polymat_wipe` zero secrets before they are freed, and the LNP prover wipes its witness, its copies and its expanded coins. |
| `0004-grandom-sampler-state.patch` | Two defects of the discrete gaussian sampler (`src/grandom.c`, `src/intvec.c`) the provers draw their masking vectors from. `gaussian155` kept its sign bits in function-level `static` variables: shared by all threads (a data race between concurrent proofs), carried from one rng stream to the next and never wiped. For `log2o >= 24`, `_intvec_grandom` reseeded the rng with the same seed and domain for every element, so the elements of a vector shared their uniform part and coins: 64 elements with `log2o = 30` took about 2 distinct values. The presentation mask `y4` (`log2stdev4 = 30`) was affected, which undermines the zero-knowledge of presentations generated before the fix. The sign-bit buffer now belongs to the caller and a vector is sampled from one stream; the distribution of a sample is unchanged and verifiers are not affected. |
| `0005-lin-prover-wipe-witness.patch` | `lin_prover_wipe_witness` wipes the witness and the prover randomness of a prover state and keeps its public part, so that a prover reused for many proofs does not keep the last witness. |
| `0006-tbox-fiat-shamir-h-z3-z4.patch` | [lazer-crypto/lazer#28](https://github.com/lazer-crypto/lazer/issues/28): three prover messages of the toolbox protocol were not bound by the challenges meant to bind them. `h` was never hashed, so `mu` and the final challenge did not depend on it; `z3` and `z4` were never hashed, and the challenges compressing their equations came from the hash before them; and those challenges also came from the hash before `tg`. `h`, `z3` and `z4` are now absorbed (their own proof encodings; `coder_grandom3_nbits` in `src/coder.c` sizes the `z3`/`z4` buffer), and the `z3`/`z4` challenges come from the hash after `tg`. The JL matrices `R`, `R'` stay derived from the hash before `z3`/`z4`. Ported from the fix in the issue thread. |
| `0007-tbox-l2-equation-domains.patch` | [lazer-crypto/lazer#25](https://github.com/lazer-crypto/lazer/issues/25): the exact-ℓ2 equations shared one challenge domain, so only their sum was checked and norm could move from one block to another. Equation `i` now uses domain `dom + i`, which stays below the next domain in use. |
| `0008-tbox-accumulator-challenge-pairs.patch` | [lazer-crypto/lazer#24](https://github.com/lazer-crypto/lazer/issues/24): `___schwartz_zippel_accumulate2` gave all `lambda / 2` accumulator pairs the same challenges. It now draws a `lambda × M` matrix and pair `i` uses rows `2i` and `2i + 1`. |
| `0009-quad-verify-bound-z21.patch` | [lazer-crypto/lazer#26](https://github.com/lazer-crypto/lazer/issues/26): `lnp_quad_verify` and `abdlop_verify` bounded only `z22`, not `z21`, while the parameters assume a bound on all of `z2`. They now check `‖z21‖² + ‖w − γ·w1‖² ≤ Bsqr`. Honest proofs and the transcript are unchanged. |
| `0010-prover-zk-z2-rejection-beta-signs.patch` | [lazer-crypto/lazer#31](https://github.com/lazer-crypto/lazer/issues/31), prover-only: the `z2` rejection (`rej2 == 2`) used the bimodal test with `σ1²` and no sign; it now uses the one-time rejection Rej1 of Lyubashevsky–Nguyen–Seiler, "Shorter lattice-based zero-knowledge proofs via one-time commitments" (`rej_onetime`, `src/rejection.c`) with `σ2²`, which roughly doubles the expected `z2` attempts. The toolbox prover's β4 sign read an always-zero bit on every fourth attempt; it now reads the next unused bit, and the sign bits are wiped. |
| `0011-lin-shared-public-params.patch` | `lin_pp_init` expands the public matrices of a proof system once and converts them to the CRT domain; `lin_prover_init_pp` / `lin_verifier_init_pp` set up provers and verifiers that borrow them (the parameters come from the `lin_pp_t`). After the conversion nothing writes the matrices, so provers and verifiers sharing them may run concurrently, and setting one up no longer expands the matrices. `lin_prover_init` / `lin_verifier_init` are unchanged; proofs do not change. See [Concurrent identity generation](#concurrent-identity-generation). |

Patches 0006-0008 change `src/lin-proofs.c`, not `src/lnp-tbox.c`: `lin_prover_prove` and
`lin_verifier_verify` run a private copy of the toolbox protocol in `lin-proofs.c`, so a fix to `lnp-tbox.c`
alone, as proposed upstream, does not reach the proofs this module makes. 0009 and 0010 change the shared
`src/lnp-quad.c`, `src/abdlop.c` and `src/rejection.c`. 0006-0008 change the transcript: presentations, SoKs
and equality-of-opening proofs made before and after them do not verify against each other. Credentials, nyms,
parameters and proof sizes are unchanged.

`make lazer-check` (run by `lazer-test` and `lazer-bench`) fails if the installed lazer was not built with
exactly the patches in the directory (it compares their sha256 with `./.lazer/REVISION`), so a build made before
a patch was added cannot be used by mistake.

To change a patch, apply the series to a pristine checkout of `LAZER_COMMIT`, edit, and regenerate the patch
with `git diff` or `git format-patch --no-signature` (paths `a/...`, `b/...`, relative to the lazer root; no
signature, so that the sha256 of a patch does not depend on the git version that produced it). Patches must not
touch the Falcon or Intel HEXL sources under `third_party/`: lazer's own Makefile extracts them and applies
`src/falcon.patch` and `src/hexl.patch` (added by patch 0000) at build time, after `lazer-fetch`. A change to them
goes into those files. When moving to a new lazer revision, drop the patches that are merged upstream and refresh the
others.

`make lazer-install` records what it built in `./.lazer/REVISION`, and `make lazer-check` (run by the test
targets) prints it. For a fetched build, this is the repository, ref and commit. For `LAZER_SRC`, it is the
source directory and the checkout's `HEAD`, marked `(with uncommitted changes)` if the working tree is dirty.
Both are followed by the applied patches with their sha256. `lazer-tests.yml` runs the unit tests on Linux
amd64 and arm64 (`ubuntu-24.04-arm`), so the arm64 support of patch 0000 is built and tested in CI. CI (`lazer-tests.yml`, and the `fuzz-lazer` job of
`nightly-fuzz.yml`) does not cache `./.lazer`: lazer is built with `-march=native`, so every run builds it from
scratch.

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
| `identity.go` | identity and signature encodings, verifier pool |
| `km.go`, `kmp.go` | `KeyManager`, `KeyManagerProvider`, identity cache wrapper |
| `prover.go` | `ProverLimiter`, see [Concurrent identity generation](#concurrent-identity-generation) |
| `deserializer.go`, `audit.go` | verifier/matcher deserializers, `AuditInfo` |
| `strictjson.go` | strict and canonical JSON decoding of audit infos, signer infos and configurations |
| `issuer.go`, `cmd/lazergen` | issuer key generation and wallet issuance for tools and tests |
| `anoncreds/anoncreds.c`, `anoncreds/anoncreds.h` | the CBDC credential scheme in C, built on lazer (derived from lazer, MIT, see `anoncreds/LICENSE.lazer`) |
| `anoncreds/anoncreds.go` | cgo binding of the scheme, with tests and a demo in `anoncreds/cmd/anoncreds_demo` |
| `anoncreds/anoncreds_params.h`, `anoncreds/params/` | proof system parameters: the generated header and the `.py` files it is generated from |

## Open issues

**Advisory: presentations generated before lazer patch 0004.** Before the patch, lazer's discrete Gaussian
sampler gave the 64 coefficients of each polynomial of the presentation mask `y4` (`log2stdev4 = 30`) about
two distinct values (see `0004-grandom-sampler-state.patch` in [Local patches](#local-patches)). That mask
hides the projections of the P2 witness, so a published presentation made with an unpatched build may leak
information about it: the credential, the masking randomness, the hidden attributes (including `usk`) and the
commitment openings. Treat identities, signatures and audit infos generated with such a build as possibly
exposing their wallet: re-issue the credentials of those wallets with a new `usk` and stop using their
pseudonyms. The defect is in upstream lazer as well and must be reported to its maintainers.

0. **Unforgeability argument.** A credential-less forgery present in an earlier version (and in lazer's
   `cbdc.py`) is fixed by a public non-zero target in the credential equation, see
   [the scheme description, §10.1](identity-lazer-anoncreds.md#101-unforgeability-of-credentials). Credentials
   issued before that fix must be reissued. A formal reduction is still missing.
1. **Commitment binding.** The commitment key is 2x11 over Z_12289[X]/(X^64+1). A rough estimate
   gives about 80-bit core-SVP for finding two openings (collision norm up to 150) and about
   120 bit for extracting an opening. A break lets a user open `nymEID`/`nymRH` to a false
   enrollment id or revocation handle, or prove a false enrollment id with `eid_proof` (audit evasion);
   it does not allow linking. Since the identity is `nymEID` alone, extracting an opening of a victim's
   `nymEID` would also let a holder of a credential with the same enrollment id sign for it. Before any production use, run the lattice estimator and, if needed, increase
   `CBDC_COM_ROWS` in `anoncreds/anoncreds.h` and regenerate the `popen` and `p2` parameters.
2. **Size.** A signature is about 53.4 KB (the identity is 225 B).
3. **Patched lazer.** The build carries local fixes to lazer (bounds-checked decoders, non-exiting key
   decoding, zeroization, the discrete gaussian sampler, the toolbox soundness and zero-knowledge issues, see
   [Local patches](#local-patches)). The sampler defects fixed by 0004 are in upstream lazer too, and affect
   presentations generated before the fix (see the patch).
   The open upstream issues lazer-crypto/lazer#24, #25, #26, #28 and #31 are fixed locally by 0006-0010, in
   the toolbox copy of `src/lin-proofs.c` that the proofs run; fixes to upstream `src/lnp-tbox.c` alone would
   not cover this build. Proofs made before and after the patches are incompatible. The fixes for #28, #25
   and #26 were checked by code review against the issue threads and the reporters' fix commits; they are not
   covered by negative regression tests (tests that a cheating proof is rejected), and #24 has no dedicated
   test either: the honest-proof tests show that the patched prover and verifier agree. Entry points of
   `liblazer.a` that this module does not use stay unpatched: lazer's own toolbox in `src/lnp-tbox.c`
   (`lnp_prover_prove` / `lnp_verifier_verify`) still has #24, #25, #28 and the β4 sign defect, and
   `src/lnp-quad-eval.c` does not hash `h` before `lnp_quad_many_*` either. These fixes should
   be upstreamed, and lazer's
   remaining unchecked entry points (e.g. `signer_sign`, `verifier_vrfy`, `user_sign` of its own blind
   signature, which this module does not use) still use the unbounded coder.
4. **Secrets in memory.** Secrets are wiped on `Clear` and after use, in C and in the Go wrapper, but copies
   made by the Go runtime (e.g. when growing slices, or by the garbage collector) cannot be wiped, and memory is
   not locked against swapping.
5. **Build flags.** lazer's own `tests/abdlop-test` has a randomized check (`tests/abdlop-test.c:146`, marked "sometimes fails" upstream) that a perturbed commitment is rejected. On amd64 it fails in a few percent of the runs with lazer's default `-O3`, but in about 90% with `-O2` (gcc). On arm64 with clang, `-O2` and `-O3` give the same low rate, and an ASan/UBSan build of lazer found no memory errors. The cause is not understood; the tests of this module pass in both builds, but use the default flags and do not rely on other flag sets until it is.

## Not done

Driver, public-parameter (`IdemixIssuerPublicKeys` equivalent) and integration (`nwo`) wiring,
a SKI cleanup provider, revocation (the RH is committed but not checked against a list), a
per-message presentation signature mode, and sending the presentation once per transaction (with
one signature per pseudonym each signature carries its own presentation, about 37 KB).
