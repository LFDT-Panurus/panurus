# ZK-ATDLOG Range-Proof Fiat-Shamir Transcripts

The ZK-ATDLOG (`zkatdlog/nogh/v1`) driver supports two interchangeable range-proof systems,
selected per public-parameters set via `rp.ProofType`:

* **Bulletproofs** (`token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof/`) — an IPA-based range
  proof.
* **CSP** (`token/core/zkatdlog/nogh/v1/crypto/rp/csp/`) — a Compressed Sigma Protocol range
  proof.

Both are non-interactive via the Fiat-Shamir transform: every verifier challenge is derived
by hashing the statement and the prover's prior messages instead of being sent by an
interactive verifier. Both share one implementation of that hashing, `common.Transcript`
(`token/core/zkatdlog/nogh/v1/crypto/common/transcript.go`) — an `Absorb`/`Squeeze` running
SHA-256 state, seeded by a protocol-specific domain separator. This page documents the exact
absorb/squeeze order each system relies on, and why it must be followed exactly.

## Why the transcript must be a single running state

A Fiat-Shamir challenge is only as binding as what it is hashed from. If a challenge were
derived from a *strict subset* of what the protocol's security proof requires — say, from
only the two most recent prover messages, instead of from the full transcript so far — a
cheating prover could choose the inputs to that smaller hash after fixing an earlier, unbound
part of the proof. That would be a soundness break: the prover would get to pick values that
satisfy an equation the verifier assumed were committed before the challenge existed. Both
range-proof systems below are built so that every challenge instead depends on the full
transcript absorbed so far.

## The Bulletproof transcript

`token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof/rp.go` runs a single `common.Transcript`,
seeded with `InitHasherWithDomain(TranscriptDomain)` where
`TranscriptDomain = "Bulletproof-RangeProof-v1"`, through the entire range proof *and* its
inner IPA sub-proof. Prover (`rangeProver.preprocess`/`Prove`) and verifier
(`rangeVerifier.Verify`/`verifyIPA`) absorb in this exact order:

1. The public statement: `CommitmentGenerators` ‖ `LeftGenerators` ‖ `RightGenerators` ‖
   `BitLength` (`absorbPublicStatement`). This binds a proof to the specific generator basis
   and bit length it was produced under.
2. The value commitment `V`, then the bit-decomposition commitments `C`, `D` — squeeze `y`,
   then squeeze `z`.
3. The polynomial commitments `T1`, `T2` — squeeze `x`.
4. Continuing the *same* transcript into the IPA: the IPA's own generator basis, `Q`, its
   folded commitment, and the claimed inner product — squeeze the IPA's first challenge.
5. Each IPA folding round absorbs that round's `(L_i, R_i)` pair before squeezing that
   round's challenge, so round `i`'s challenge depends on every round `0..i-1` before it.

### A standalone IPA

The IPA can also be run on its own, outside a range proof (see
`bulletproof.NewIPAProver`/`NewIPAVerifier`'s tests). A standalone caller must seed its own
`common.Transcript` — `NewIPAProver`/`NewIPAVerifier` take the transcript as a required
parameter with no self-seeding fallback — using
`StandaloneIPATranscriptDomain = "Bulletproof-IPA-v1"`, a domain distinct from the
range-proof one, so a standalone IPA's challenges can never collide with one nested inside a
range proof.

## The CSP transcript

`token/core/zkatdlog/nogh/v1/crypto/rp/csp/rp.go` and `csp/csp.go` share the same
`common.Transcript`. Its initial state — the `TranscriptHeader` — absorbs the CSP public
statement once per public-parameters set (`PublicParams.initCSPTranscriptHeader`, in
`token/core/zkatdlog/nogh/v1/setup/setup.go`): `PedersenGenerators` ‖ `LeftGenerators` ‖
`RightGenerators` ‖ `BitLength`, under the domain `"CSP-RangeProof-v1"`. That header is
**not** part of the wire encoding (there is no corresponding proto field); every loader of a
`*PublicParams` must call `initCSPTranscriptHeader` before any CSP proof is produced or
verified. It is invoked from both `PublicParams.NewWith` (the genesis/prover path) and
`PublicParams.Validate` (every load path, since validators and provers alike call `Validate`
after deserializing a `*PublicParams`), so this holds in practice, but it is a precondition
worth knowing if you construct a `*PublicParams` by some other path.

From that header, `rangeProver.Prove`/`rangeVerifier.Verify` absorb the value commitment,
then run the Schnorr proof of knowledge and the inner CSP (compressed sigma protocol) proof
in lockstep, each absorbing its own prover messages before squeezing the next challenge.
`WithTranscriptHeader` is a required, non-empty parameter throughout this chain — every
constructor hard-fails on an empty header rather than falling back to a different seed.

## Known gap: no transaction-level binding

Neither range-proof system's transcript, nor the companion `TypeAndSum`
(`token/core/zkatdlog/nogh/v1/transfer/typeandsum.go`) / `SameType`
(`token/core/zkatdlog/nogh/v1/issue/sametype.go`) proofs, absorb anything from the
surrounding transaction — the token request, the transfer/issue action, the other inputs, or
the signing anchor. Each sub-proof's transcript is independent; the only thing tying a range
proof to the rest of a transfer/issue action is that both operate on the same
`output - commitmentToType` commitment, and `TypeAndSumVerifier.Verify` does bind that
commitment into its own challenge. In practice this only matters for an output whose
commitment (type, value, and blinding factor together) is byte-identical across two
transactions — already an unusual case that double-spend protection at the token layer is
responsible for handling — but it is a real gap in defense-in-depth. See
[#2515](https://github.com/LFDT-Panurus/panurus/issues/2515) for related follow-up.

## See also

* [Public Parameters Lifecycle](../public_parameters.md)
* [ZK-ATDLOG Testing Architecture](../drivers/benchmark/core/dlognogh/dlognogh_architecture.md)
