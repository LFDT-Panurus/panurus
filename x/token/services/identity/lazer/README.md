# lazer: post-quantum anonymous identities

A `membership.KeyManager` / `KeyManagerProvider` / `Deserializer` built on a
lattice-based anonymous credential (Go binding in the `anoncreds` subpackage, built on
the [lazer](https://github.com/lazer-crypto/lazer) library), as an alternative to
`idemix`/`idemixnym` for owner wallets. Identity type `lazer` (7).

Status: **experimental**, not wired into any driver (see "Not done").

Documentation:

- [Lazer: post-quantum anonymous identities](../../../../../docs/services/identity-lazer.md):
  encodings, audit, concurrency, building, testing, parameters, open issues.
- [The Lazer Anonymous Credential Scheme](../../../../../docs/services/identity-lazer-anoncreds.md):
  the cryptography (issuance, nyms, presentations, proofs, security).
- [Local patches to lazer](../../../../../docs/services/identity-lazer-patches.md):
  the fixes in `patches/`, applied to lazer at build time.

## Design

Credential attributes (32 bytes each): `usk` (hidden at issuance), `SHA-256(OU)`,
`SHA-256(EID)`, `SHA-256(RH)`, `SHA-256(role)`, domain separated, see `schema.go`.

A pseudonym (the identity published in a token owner field) is `nymEID`, the
commitment `Acom*(H(EID),r)` to the enrollment id, 225 B, as in idemixnym.

- Signature: DER `{nymSK, nymRH, pres, sok}`. `pres` is a P2 presentation (hides every
  attribute) proving that a valid credential exists and that `nymSK`, the identity and
  `nymRH` commit to attributes 0, 2 and 3; the verifier fixes those indices and takes
  `nymEID` from the identity. `sok` is a signature of knowledge of the opening of
  `nymSK`, bound to the message.
- The opening of `nymEID` is never revealed: with it, anyone holding a credential with
  the same EID (the issuer can make one) could sign under the identity.
- Audit info: `{eid, rh, nym_eid, nym_sk, nym_rh, opening_rh, eid_proof, enc_opening_eid, pres}`,
  canonical JSON. `eid_proof` proves that `nym_eid` commits to the EID without revealing
  the opening (`anoncreds.EqOpeningVerify`). Matching checks it, the RH opening and `pres`
  under the issuer key, so an identity made up from someone's EID/RH without a credential
  does not match. `enc_opening_eid` is the opening encrypted for the wallet:
  `Identity(ctx, auditInfo)` reuses it, so the new pseudonym is the same `nymEID` (as in
  idemixnym), with a fresh `nymSK` and presentation.
- Signer info (identity store): `{version: 2, enc_opening_sk, nym_rh, pres}`. The opening of
  `nymSK` reveals `usk` together with `nymSK`, so it is AES-256-GCM encrypted under a key
  derived from the wallet secret, bound to the identity, `nym_rh` and `pres`.
- The wallet secret `usk || user state` is moved to the `idriver.Keystore` by the
  KMP and removed from the stored configuration (only the SKI stays).
- Concurrency: the public matrices of the proof systems are expanded once per
  process and shared read-only by every prover and verifier (lazer patch 0011).
  Each presentation sets up its own prover and only reads the wallet state, so
  presentations run concurrently on one key manager. A `ProverLimiter`
  (`prover.go`), shared by all key managers of a `KeyManagerProvider` (default
  `min(GOMAXPROCS, 4)`), bounds how many run at once; `Close` waits for them.

## Layout

`config.go` JSON config and on-disk layout (`msp/IssuerPublicKey`,
`user/SignerConfig[Full]`), `km.go`, `kmp.go`, `prover.go` (presentation limiter),
`deserializer.go`, `audit.go`, `strictjson.go`, `identity.go` (encodings, verifier pool),
`issuer.go` (helpers used by `cmd/lazergen`), `anoncreds/` (the scheme in C and its cgo
binding, parameters in `anoncreds/params/`), `patches/` (local patches to lazer).

```
lazergen ca-keygen -out DIR
lazergen signerconfig -ca DIR -out DIR -eid EID -ou OU [-role R] [-rh RH]
```

## Building and testing

The scheme (`anoncreds/anoncreds.c`) is compiled in this module by cgo and uses the lazer
library (`lazer.h`, `liblazer.a`), which is built from the lazer repository, with the local
patches, and installed into `./.lazer`. From the repository root:

```
make lazer-install                     # fetch the pinned lazer commit, patch, build and install it into ./.lazer
make lazer-install LAZER_SRC=~/lazer   # or build a local checkout (it is copied, not modified)
make lazer-test                        # go test -race ./... in this module
make lazer-bench                       # sizes and benchmarks
make lazer-clean
```

Requirements: C/C++ toolchain, make, cmake, unzip, patch, git, GMP, MPFR; linux/amd64 or
arm64. See `lazer.mk` and the documentation above. The module is not in `GO_MODULES`, so
`make unit-tests` does not build it, and the root Panurus build is unaffected.

## Open issues (must be resolved before production use)

1. **Commitment binding strength.** `Acom` is 2x11 over Z_12289[X]/(X^64+1).
   A rough estimate gives about 80-bit core-SVP for collisions of norm up to 150
   (about 120 bit for extracting an opening). A break lets a user open `nymEID`
   or `nymRH` to a false EID/RH (audit evasion). Run the lattice estimator and, if
   needed, raise `CBDC_COM_ROWS` and regenerate the `popen`/`p2` parameters.
2. **Unforgeability** of credentials is argued, not formally proven.
3. **Patched lazer.** The proof system's soundness and zero-knowledge depend on local
   patches that are not merged upstream.
4. **Size.** About 53 KB per signature, far larger than idemix.

## Not done

Driver, public-parameter and nwo wiring (`nogh/v1/driver`, `IdemixIssuerPublicKeys`),
SKI cleanup provider, revocation (RH is committed but never checked), a
per-message P2 signature mode, and sending `pres` once per transaction. Verification does not
cache presentation checks (one signature per pseudonym), so every `Verify` pays for the full check.
