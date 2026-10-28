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
- Audit info: `{eid, rh, opening_eid, opening_rh, pres}`, canonical JSON. Matching
  (`Deserializer.MatchIdentity`/`GetAuditInfoMatcher`) recomputes the nyms and checks
  `pres` under the issuer key, so an identity made up from someone's EID/RH without a
  credential does not match. `Identity(ctx, auditInfo)` reuses the openings, so `nymEID`
  and `nymRH` stay fixed across pseudonyms (as in idemix).
- Signer info (identity store): `{version, enc_opening_sk, pres}`. The opening of `nymSK`
  reveals `usk` together with `nymSK`, so it is AES-256-GCM encrypted under a key derived
  from the wallet secret, with the identity as additional data.
- Wallet secret `usk || user state` is moved to the `idriver.Keystore` by the
  KMP and removed from the stored configuration (only the SKI stays).
- Concurrency: a presentation mutates its C prover state, so each key manager
  clones prover states from a never-proving template and keeps them in a pool
  (`prover.go`). A `ProverLimiter`, shared by all key managers of a
  `KeyManagerProvider` (default `min(GOMAXPROCS, 4)`), bounds the presentations
  running at once, hence the prover states in use (about 10 MB each); each key
  manager also keeps `WithIdleProvers` (default 1) of them between calls.
- Presentations generated before lazer patch 0004 may leak their witness: see
  the advisory in `docs/services/identity-lazer.md`, "Open issues". See
  `docs/services/identity-lazer.md`, "Concurrent identity generation".

## Benchmarks

`make lazer-bench` prints sizes and timings; the `TestParallelBenchmarkLazer*`
tests in `km_bench_test.go` measure throughput with concurrent workers. See
`docs/services/identity-lazer.md`.

## Layout

`config.go` JSON config and on-disk layout (`msp/IssuerPublicKey`,
`user/SignerConfig[Full]`), `km.go`, `kmp.go`, `prover.go` (prover pool and limiter), `deserializer.go`, `audit.go`, `strictjson.go`,
`identity.go` (encodings, verifier pool), `issuer.go`
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
per-message P2 signature mode, and sending `pres` once per transaction. Verification does not
cache presentation checks (one signature per pseudonym), so every `Verify` pays for the full check.
