# ZKAT-SNARK Driver: Proof Systems

The `zkatsnark` driver (`x/token/core/zkatsnark/nogh/v1`) proves its spend, output and migration
circuits with [gnark](https://github.com/consensys/gnark). The proof system is selected by the
`ProofSystem` field of the public parameters (`proof_system` in `snarkpp.proto`) and is routed through
a single abstraction, `crypto/backend.ProofSystemBackend`
(`Setup`, `NewProvingKey`, `NewVerifyingKey`, `Prove`, `Verify`).

| `ProofSystem` | Curves | Arithmetization | Trusted setup | Proof size (spend/output, BN254) |
|---------------|--------|-----------------|---------------|----------------------------------|
| `groth16`     | BLS12-381, BN254 | R1CS | per circuit | ~164 B |
| `plonk`       | BLS12-381, BN254 | SparseR1CS | universal KZG SRS | ~520 B |
| `plonkfri`    | BN254 only       | SparseR1CS | none (transparent) | ~841 KiB |

## `plonkfri`

PLONK with FRI as the polynomial commitment scheme instead of KZG. It needs no structured reference
string: `Setup` is deterministic, so the same circuit always yields byte-identical keys.

`plonkfri` is provided by the gnark fork `github.com/dewindtk/gnark-fork` (`backend/plonkfri`), wired
in with a `replace` directive in `x/token/core/zkatsnark/go.mod`. Its security status is described in the
fork's `PROJECT.md`; it has **not been externally reviewed**, so treat it as experimental.

### Constraints and trade-offs

- **BN254 only.** `pp.Validate` rejects any other curve for `plonkfri`. The migration circuit is
  BLS12-381-only, so migration is unavailable with `plonkfri`.
- **Plain PLONK gates only.** `plonkfri` does not support BSB22 commitments (`api.Commit`). Circuits
  must not use gadgets built on it, such as emulated-field multiplication, the range checker's
  commitment variant, or `twistededwards.ScalarMul` (whose FakeGLV decomposition check is an emulated
  multiplication). `gadgets.ValueCommitCircuit` therefore computes `v·V + rcv·R` with a single
  `DoubleBaseScalarMul`, which uses no commitments. This circuit is shared by all proof systems, so
  the keys of every proof system differ from those generated before this change.
- **Large proofs.** A proof is about 841 KiB, roughly 1600x a `plonk` proof, and proving takes about
  2.5x longer. Verifying keys are tiny (172 B) but proving keys are about 3 MiB for the output circuit.
  Loading a proving key (`NewProvingKey`) recomputes the derived key material and is about 6x slower
  than for `plonk`, so load it once per key, as `prepared` already does.
- **Resource limits.** `driver.ResourceLimits.MaxProofBytes` defaults to 128 KiB and is enforced by the
  zkatdlog driver, not by zkatsnark. If you add such a check to zkatsnark, it must be per proof
  system, otherwise it rejects every `plonkfri` proof.

### Parameter bounds and decoding hardening

- **`MaxBits` is limited to 1..64.** Token values are `uint64` throughout, and value balance is checked by a
  Schnorr binding signature over the sum of value commitments, i.e. modulo the Edwards subgroup order
  (about 2^250). A `MaxBits` near 243 or more would let value sums wrap that order and inflate the supply, so
  `pp.Validate` and the `setup.Compile*Circuit` functions reject anything above 64.
- **Spend range check.** `SpendCircuit` range-checks `Value` against `MaxBits`, like `OutputCircuit`. The value
  commitment uses `DoubleBaseScalarMul`, which reduces the scalar modulo the subgroup order, so a value and that
  value plus the order open the same commitment; the range check removes the ambiguity. The note commitment
  already pins the value, so this is defence in depth. It changes the spend constraint system, so spend keys of
  existing public parameters must be regenerated.
- **Trailing bytes are rejected** for proving keys, verifying keys and proofs of every proof system
  (`backend.ErrTrailingBytes`); gnark's decoders alone stop at the end of the object and ignore the rest, which
  makes the encoding malleable.
- **Domain size cap.** A plonkfri verifying key is a few bytes, but decoding it builds the FRI evaluation domain
  (about 16 points per unit of circuit size). The decoder rejects circuits above 2^17 (the real spend and output
  circuits are 2^14), which bounds the allocation to about 0.4 GB.
- **`TotalValue`** of an issue action must fit in 64 bits; a field element above that is rejected rather than
  truncated.

### Serialization

Keys and proofs are encoded by the fork (`WriteTo` / `ReadFrom`). Decoders are bounded, reject
non-canonical field elements and inconsistent sizes, and `ProvingKey.ReadFrom` rejects a key whose
recomputed FRI commitment differs from the embedded verifying key. The format is new and unversioned.
`FuzzPlonkFRIVerifyNoPanic` (see `.github/workflows/nightly-fuzz.yml`) covers the decoders.

### Selecting it

```go
p := pp.DefaultPublicParams()
p.ProofSystem = "plonkfri"
p.Curve = ecc.BN254
p, err := setup.SetupAll(p)
```
