# lazer: post-quantum anonymous identities

A `membership.KeyManager` / `KeyManagerProvider` / `Deserializer` built on the
lattice anonymous credential of [lazer](https://github.com/lazer-crypto/lazer)
(Go binding in the `anoncreds` subpackage), as an alternative to `idemix`/`idemixnym`.

Status: **experimental**, not wired into any driver (see "Not done").

## Design

Credential attributes (32 bytes each): `usk` (hidden at issuance), `SHA-256(OU)`,
`SHA-256(EID)`, `SHA-256(RH)`, `SHA-256(role)`. See `schema.go`.

A pseudonym (the identity published in a token owner field) is
`ASN.1{nymSK, nymEID, nymRH}`, three commitments `Acom*(attr,r)`, about 0.7 KB.

- `pres`: a P2 presentation (hides every attribute) proving a valid credential
  exists and that the three nyms commit to attributes 0, 2 and 3. The verifier
  hardcodes those indices. It is bound to the issuer by a context string.
- Signature: `ASN.1{pres, sok}` where `sok` is a signature of knowledge of the
  opening of `nymSK`, bound to the message. Signing uses `nymSK`, so an auditor
  who holds the EID/RH openings cannot forge signatures.
- Audit info: `{EID, RH, openingEID, openingRH}`. `Match` recomputes the nyms.
  `Identity(ctx, auditInfo)` reuses the openings, so `nymEID` and `nymRH` stay
  fixed across pseudonyms (as in idemix).
- Signer info (identity store): `{openingSK, pres}`, enough to rebuild a signer.
- Wallet secret `usk || user state` is moved to the `idriver.Keystore` by the
  KMP and removed from the stored configuration (only the SKI stays).

## Sizes and timings (amd64 under emulation, indicative only)

| item | size |
|---|---|
| identity | 688 B |
| signature | about 52 KB (pres 37 KB + SoK 16 KB) |
| audit info | about 3 KB |
| signer info | about 50 KB |

Identity generation about 0.4 s, sign about 70 ms, verify about 270 ms uncached
and about 30 ms with the presentation check cached.

## Layout

`config.go` JSON config and on-disk layout (`msp/IssuerPublicKey`,
`user/SignerConfig[Full]`), `km.go`, `kmp.go`, `deserializer.go`, `audit.go`,
`identity.go` (encodings, verifier pool, presentation cache), `issuer.go`
(helpers used by `cmd/lazergen`).

```
lazergen ca-keygen -out DIR
lazergen signerconfig -ca DIR -out DIR -eid EID -ou OU [-role R] [-rh RH]
```

## Building and testing

The scheme (`anoncreds/anoncreds.c`) is compiled in this module by cgo and uses the lazer library (`lazer.h`,
`liblazer.a`), which is built and installed from the lazer repository. From the repository root:

```
make lazer-install                     # fetch the pinned lazer commit, build and install it into ./.lazer
make lazer-install LAZER_SRC=~/lazer   # or build a local checkout (it is copied, not modified)
make lazer-test                        # go test -race ./... in this module
make lazer-bench | lazer-clean
```

Requirements: C/C++ toolchain, make, cmake, unzip, patch, git, GMP, MPFR. See
`lazer.mk` and [docs/services/identity-lazer.md](../../../../../docs/services/identity-lazer.md)
for details. The module is not in `GO_MODULES`, so `make unit-tests` does not build it, and the
root Panurus build is unaffected.

## Open issues (must be resolved before production use)

1. **Commitment binding strength.** `Acom` is 2x11 over Z_12289[X]/(X^64+1).
   A rough estimate gives about 80-bit core-SVP for collisions of norm up to 150
   (about 120 bit for extracting an opening). A break lets a user open `nymEID`
   or `nymRH` to a false EID/RH (audit evasion). It does not allow forging
   signatures or linking pseudonyms. Run the lattice estimator and, if needed,
   raise `CBDC_COM_ROWS` and regenerate the `popen`/`p2` parameters. Do not claim
   audit soundness at production strength before that.
2. Proof sizes: about 52 KB per signature is far larger than idemix.

## Not done

Driver, public-parameter and nwo wiring (`nogh/v1/driver`, `IdemixIssuerPublicKeys`),
SKI cleanup provider, revocation (RH is committed but never checked), a
per-message P2 signature mode, and sending `pres` once per transaction.
