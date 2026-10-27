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

`AuditInfo` is JSON: `{eid, rh, opening_eid, opening_rh}`. `Match(identity)` recomputes `nymEID`
and `nymRH` from the values and openings and compares. `EnrollmentID()` and `RevocationHandle()`
return the plaintext. `KeyManager.Identity(ctx, auditInfo)` with a reference audit info reuses
its openings, so the new pseudonym has the same `nymEID` and `nymRH`, as in Idemix.

### Signer info

`IdentityDescriptor.SignerInfo` is JSON `{opening_sk, pres}`. It is kept by the identity store, and
`DeserializeSigner` uses it, together with the wallet secret, to rebuild a signer. The signer is
only returned if the secret opens `nymSK` of the identity, which ties the identity to the wallet.

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

## Sizes and timings

Measured on linux/amd64 under emulation, so only indicative; run `make lazer-bench`.

| Item | Size |
|---|---|
| identity | 688 B |
| signature | about 52 KB (presentation 37 KB, SoK 16 KB) |
| audit info | about 3 KB |
| signer info | about 50 KB |

Generating an identity takes about 0.4 s, signing about 70 ms, verifying about 270 ms
(about 30 ms when the presentation check is cached).

## Building and testing

The CBDC credential scheme itself (`anoncreds/anoncreds.c`, `anoncreds/anoncreds.h`) is part of this module and is compiled
by cgo together with the Go package. It uses the lazer library (`lazer.h`, `liblazer.a`) from the lazer
repository, which `make lazer-install` builds and installs; lazer itself contains no CBDC code. It needs
`gcc`/`clang`, `make`, `cmake`, `unzip`, `patch`, `git`, GMP and MPFR, and builds on linux/amd64
and on arm64 (e.g. macOS on Apple silicon).

```bash
# apt-get install -y build-essential cmake unzip patch git libgmp-dev libmpfr-dev
# brew install cmake gmp mpfr

make lazer-install                     # fetch lazer (LAZER_REPO at LAZER_COMMIT), build, install into ./.lazer
make lazer-install LAZER_SRC=~/lazer   # or copy and build a local checkout (it is not modified)
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

`make lazer-install` records what it built in `./.lazer/REVISION`, and `make lazer-check` (run by the test
targets) prints it. For a fetched build, this is the repository, ref and commit. For `LAZER_SRC`, it is the
source directory and the checkout's `HEAD`, marked `(with uncommitted changes)` if the working tree is dirty.

The tests cover, among others: identity and signature round trips, unlinkability, signatures under
the wrong identity or issuer, swapped nyms, audit matching and reuse, signers of another wallet,
remote wallets, invalid credentials, malformed inputs and fuzzing, and the provider flow.

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

The dimensions in the parameters must agree with the constants in `anoncreds.h` (`CBDC_COM_ROWS` etc.), which the
tests of the `anoncreds` package check. lazer's Python reference (`python/cbdc/cbdc.py`) uses its own copy of these
files and is the reference for the scheme, not part of this module.

## Layout

| File | Content |
|---|---|
| `schema.go` | attribute layout, domain separators |
| `config.go` | configuration and on-disk layout |
| `identity.go` | identity and signature encodings, verifier pool, presentation cache |
| `km.go`, `kmp.go` | `KeyManager`, `KeyManagerProvider`, identity cache wrapper |
| `deserializer.go`, `audit.go` | verifier/matcher deserializers, `AuditInfo` |
| `issuer.go`, `cmd/lazergen` | issuer key generation and wallet issuance for tools and tests |
| `anoncreds/anoncreds.c`, `anoncreds/anoncreds.h` | the CBDC credential scheme in C, built on lazer (derived from lazer, MIT, see `anoncreds/LICENSE.lazer`) |
| `anoncreds/anoncreds.go` | cgo binding of the scheme, with tests and a demo in `anoncreds/cmd/anoncreds_demo` |
| `anoncreds/anoncreds_params.h`, `anoncreds/params/` | proof system parameters: the generated header and the `.py` files it is generated from |

## Open issues

1. **Commitment binding.** The commitment key is 2x11 over Z_12289[X]/(X^64+1). A rough estimate
   gives about 80-bit core-SVP for finding two openings (collision norm up to 150) and about
   120 bit for extracting an opening. A break lets a user open `nymEID`/`nymRH` to a false
   enrollment id or revocation handle (audit evasion); it does not allow signature forgery or
   linking. Before any production use, run the lattice estimator and, if needed, increase
   `CBDC_COM_ROWS` in `anoncreds/anoncreds.h` and regenerate the `popen` and `p2` parameters.
2. **Size.** A signature is about 52 KB.
3. **Build flags.** lazer's own `tests/abdlop-test` has a randomized check (`tests/abdlop-test.c:146`, marked "sometimes fails" upstream) that a perturbed commitment is rejected. On amd64 it fails in a few percent of the runs with lazer's default `-O3`, but in about 90% with `-O2` (gcc). On arm64 with clang, `-O2` and `-O3` give the same low rate, and an ASan/UBSan build of lazer found no memory errors. The cause is not understood; the tests of this module pass in both builds, but use the default flags and do not rely on other flag sets until it is.

## Not done

Driver, public-parameter (`IdemixIssuerPublicKeys` equivalent) and integration (`nwo`) wiring,
a SKI cleanup provider, revocation (the RH is committed but not checked against a list), a
per-message presentation signature mode, and sending the presentation once per transaction.
