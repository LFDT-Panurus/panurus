# ZKAT-SNARK Driver

This page covers the [proof systems](#proof-systems) the driver supports and how it
[upgrades tokens](#token-upgrades) created by other drivers.

## Proof Systems

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

### `plonkfri`

PLONK with FRI as the polynomial commitment scheme instead of KZG. It needs no structured reference
string: `Setup` is deterministic, so the same circuit always yields byte-identical keys.

`plonkfri` is not part of upstream gnark. Its implementation lives in the zkatsnark module itself, in
`nogh/v1/crypto/internal/plonkfri` (`bn254` for the prover and verifier, `nativefri` for the FRI
commitment scheme), on top of plain upstream `github.com/consensys/gnark`; no `replace` directive is
needed. It is a copy of the PLONK-FRI backend of the gnark fork `github.com/dewindtk/gnark-fork`, taken
from `github.com/adecaro/gnark` at commit `ccc2c555fda533f9b09cf05cbeddb732552002d0`. The package
documentation (`bn254/doc.go`) lists the differences from that source. The security arguments are
described in the fork's `PROJECT.md` and backed by the attack and zero-knowledge tests that came with the
code. It has **not been externally reviewed**, so treat it as experimental.

The fork was based on gnark v0.16.3; the module now builds on upstream gnark v0.16.4. The spend,
output and migration constraint systems compiled with either are identical (apart from the gnark
version recorded in the serialized constraint system), and `plonkfri` keys are byte-identical, so public
parameters generated with the fork remain valid.

#### Constraints and trade-offs

- **BN254 only.** `pp.Validate` rejects any other curve for `plonkfri`. The migration circuit is
  BLS12-381-only, so ZKAT-DLOG upgrades are unavailable with `plonkfri`: `pp.Validate` and
  `setup.CompileMigrationCircuit` refuse the combination.
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

#### Parameter bounds and decoding hardening

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

#### Serialization

Keys and proofs are encoded by `crypto/internal/plonkfri/bn254` (`WriteTo` / `ReadFrom`). Decoders are bounded, reject
non-canonical field elements and inconsistent sizes, and `ProvingKey.ReadFrom` rejects a key whose
recomputed FRI commitment differs from the embedded verifying key. The format is new and unversioned.
`FuzzPlonkFRIVerifyNoPanic` and `FuzzNewKeysNoPanic` (through the backend) and `FuzzReadFrom`
(directly on the decoders) cover them; see `.github/workflows/nightly-fuzz.yml`.

#### Selecting it

```go
p := pp.DefaultPublicParams()
p.ProofSystem = "plonkfri"
p.Curve = ecc.BN254
p, err := setup.SetupAll(p)
```

## Token Upgrades

A network that moves to ZKAT-SNARK still holds tokens created by the driver it used before. This
section describes how those tokens become ZKAT-SNARK notes. It follows the two mechanisms described
in [Upgradability](../upgradability.md#token-upgradability-ledger):

- **In place**, inside an ordinary transfer. The owner spends the legacy token as if it were a
  ZKAT-SNARK note. No issuer is involved and no separate transaction is needed.
- **Through the issuer** (burn-and-re-issue). The owner asks an issuer to redeem the legacy tokens
  and issue new notes worth the same.

| Source | In place, during a transfer | Through the issuer |
|---|---|---|
| Fabtoken, precision `<= min(PrecisionValue, MaxBits)` | Yes | No (already spendable) |
| Fabtoken, precision `> min(PrecisionValue, MaxBits)` | No | Yes |
| ZKAT-DLOG on BLS12-381 | No (follow-up) | Yes |
| ZKAT-DLOG on BN254 | No | No |

Every legacy token takes exactly one path, as in ZKAT-DLOG. A token that can be spent in place
never needs the issuer, and the issuer refuses to burn a spendable token.

This split is enforced **off-chain, by the issuer**: `upgrade.Service.checkUpgradable` checks the
format each token is declared with in the upgrade request. The ledger does not check it. A
malicious issuer can therefore burn and re-issue a token that was eligible for the in-place path.
This does not affect soundness: the ledger still requires the token to exist and be spent once,
and the minted notes to carry its type and value.

On both paths, a fabtoken whose quantity cannot be represented at the ZKAT-SNARK `PrecisionValue`
is refused, as in ZKAT-DLOG. It can be neither spent in place nor re-issued.

The code is in `x/token/core/zkatsnark/nogh/v1`. Paths below are relative to it.

### Notation

A ZKAT-SNARK note is `{v, t, r}`: value, token type and randomness. Its ledger output carries:

- `cm = MiMC(v, enc(t), r)`, the note commitment;
- `cv = v·V + rcv·R`, a value commitment on the embedded Edwards curve;
- `tc = MiMC(enc(t), tr)`, a type commitment, with `tr` shared by every input and output of an
  action.

`enc(t)` is `mimc.TokenTypeBytes(t)`, the first 31 bytes of `SHA-256("zkatsnark/token-type/v1" ‖ t)`.
It is always a canonical field element on BN254 and BLS12-381, and it is injective up to SHA-256
collisions. An earlier encoding, the type bytes reduced mod r, mapped `"\x00USD"` and `"USD"` (or
any two types congruent mod r) to the same commitment.

**Breaking change.** ZKAT-SNARK notes and type commitments created with the earlier encoding
cannot be spent after this change. Their commitments no longer open with `enc(t)`, so their spend
proofs fail. A network that already holds such notes must re-issue them.

A transfer conserves value through the binding signature over `Σcv_in − Σcv_out`. It keeps one
type through the shared `tc`, which every spend and output proof opens.

### Which fabtoken formats take which path

The spend and output circuits range-check every value to `[1, 2^MaxBits)`, and fabtoken quantities
are parsed with the ZKAT-SNARK quantity precision `PrecisionValue`. A fabtoken format of precision
`p` is therefore spendable in place iff `p <= min(PrecisionValue, MaxBits)`
(`token.InPlaceFabtokenPrecisions`).

- **In place.** `TokensService.SupportedTokenFormats()` returns `"zkatsnark"` plus the in-place
  fabtoken formats. The token store marks such tokens spendable when the driver is installed, and
  the wallet selects them for transfers like any other token.
- **Through the issuer.** `token.NewTokensUpgradeService` accepts exactly the fabtoken formats
  missing from that list.
- **Pinned.** `TestTokensService_InPlaceFabtokenFormats` checks that no format is on both lists or
  on neither.

With the default public parameters (`PrecisionValue = 64`, `MaxBits = 64`) every fabtoken format is
spent in place, and the issuer path is only reachable with a smaller precision or `MaxBits`. This is
the same as ZKAT-DLOG with a 64-bit precision.

`TokensService.Deobfuscate` and `Recipients` open a fabtoken output from its cleartext content, so
wallets, auditors and the token store handle the tokens before they are upgraded. As in ZKAT-DLOG,
`Deobfuscate` requires the fabtoken output metadata (`actions.OutputMetadata`) that the fabtoken
driver stores with every output, and refuses metadata it cannot parse.

### In-place upgrade of fabtoken during a transfer

The design mirrors the ZKAT-DLOG upgrade witness (`TransferUpgradeWitnessValidate`). There, a
fabtoken input is reinterpreted as a Pedersen commitment with a disclosed blinding factor. Here, it
is reinterpreted as a note with a disclosed randomness.

#### Assembling the transfer (owner)

`TransferService.Transfer` loads the inputs from the vault. For each input that is a fabtoken
output (`token.IsFabtokenOutput`, a typed token with the fabtoken tag), `inputNote` does the
following:

1. **Parse.** It parses the output with `token.ParseFabtokenOutput`. The owner and type must be
   non-empty, and the quantity must be a positive integer at `PrecisionValue`. The value must not
   exceed `pp.MaxTokenValue()`, otherwise the transfer fails with a request to upgrade the token
   through the issuer.
2. **Fresh note.** It builds a fresh note `{v, t, r}` from the cleartext value and type, with
   uniformly random `r`.
3. **Spend request.** It marks the spend request `Upgraded`.

The orchestrator then proves the input with the ordinary `SpendCircuit` over that note. Because the
request is marked `Upgraded`, it also discloses `r` in the spend description as
`UpgradeRandomness`. The transfer's `InputTokens` carries the fabtoken bytes **exactly as loaded**.
The ledger derives the key it checks and deletes from these bytes, so they must not be re-encoded.

Inputs can mix fabtoken outputs and ZKAT-SNARK notes of the same type. The outputs are ordinary
ZKAT-SNARK notes. The owner signs the token request as for any transfer; the signer is the
fabtoken's owner.

```
SpendDescription {
  CommitmentIn       = MiMC(v, enc(t), r)   // as for any input
  ValueCommitInX/Y   = v·V + rcv·R
  TypeCommitment     = MiMC(enc(t), tr)
  SpendProof         = SpendCircuit proof
  UpgradeRandomness  = r                    // only for an upgraded fabtoken
}
```

`UpgradeRandomness` is field 6 of `SpendDescription` in `snarkactions.proto`.

#### Validation (ledger)

`TransferSignatureValidate` loads each input from the ledger by its ID. `ledgerInputOwner` then
decides how to bind the spend to the ledger token, from the ledger bytes, which no transaction can
choose:

- **ZKAT-SNARK output.**
  - `CommitmentIn` must equal its `CommitmentOut`.
  - `UpgradeRandomness` must be empty; otherwise the action is `ErrMalformedAction`.
  - The owner is its `Recipient`.
- **Fabtoken output.**
  1. It is parsed with `ParseFabtokenOutput`, and its value must not exceed `pp.MaxTokenValue()`.
  2. `UpgradeRandomness` must be a canonical field element.
  3. `MiMC(v, enc(t), UpgradeRandomness)` is recomputed from the **cleartext ledger token** and must
     equal `CommitmentIn` (`ErrCommitmentMismatch` otherwise).
  4. The owner is the fabtoken's owner.

The owner's signature is then verified as for any input. The remaining checks need no change.

**Value and type.** The spend proof shows that `cv` and `tc` open to the same `v` and `enc(t)`
as `CommitmentIn`. Since `CommitmentIn` is now pinned to the ledger token's value and type, the type
homogeneity check and the binding signature conserve them.

**Existence and single use.** The translator checks and deletes
`CreateOutputSNKey(txID, index, InputTokens[i])`. Changing a single byte of the fabtoken gives a
key that does not exist.

**What the upgrade reveals.** It reveals the spent token's value and type, which were public on the
ledger anyway. The outputs are as private as in any transfer.

#### Requirements

- The auditor must hold the spent fabtoken outputs in its token store, as for every transfer input.
  If auditing moved to another node when the network switched drivers, that node needs the
  fabtoken-era tokens first.
- Owners of script (HTLC), multisig or policy identities follow the same rules as for ZKAT-SNARK
  inputs. A transfer whose required signatures are missing is rejected.

### Upgrading fabtoken through the issuer (burn-and-re-issue)

This path reuses the ZKAT-DLOG protocol and trust model:

- a 32-byte challenge;
- owner signatures over `SHA256(challenge ‖ tokens)`;
- `ttx.RequestTokensUpgrade` and `ttx.Transaction.Upgrade`.

**Trust model.** The owners' consent is the upgrade proof, which the issuer verifies off-chain.
On the ledger the issuance is authorized by the **issuer signature alone**; no owner signature is
required at validation time.

**What the ledger adds over ZKAT-DLOG.** ZKAT-SNARK also checks on the ledger that the redeemed
tokens are real, spent once, and match the value and type of what is minted. An issuer can burn a
token it was not asked to upgrade, exactly as in ZKAT-DLOG, but it cannot mint more than it burns
or change the type.

#### Protocol

1. **Request (owner).** `ttx.RequestTokensUpgrade` sends the issuer the tokens to upgrade and the
   recipient data. The issuer replies with a challenge.
2. **Proof (owner).** `TokensService.GenUpgradeProof` signs `SHA256(challenge ‖ tokens)` with every
   token owner's key.
3. **Issue (issuer).** `ttx.Transaction.Upgrade` calls `IssueService.Issue` with the upgrade
   request. Its `upgrade` branch does the following:
   - `ProcessTokensUpgradeRequest` checks three things off-chain:
     - every token is upgrade-eligible, with at most `MaxUpgradeRequestTokens` (256) tokens;
     - the challenge;
     - the owner signatures. This is the owners' consent.
   - `token.SummarizeUpgradeInputs` parses every token as a fabtoken output and checks the inputs.
     The owner must be a plain x509 or idemix identity, the IDs pairwise distinct and the types all
     equal. The values are summed, with an overflow check.
   - It splits the total into `k = ⌈total / pp.MaxTokenValue()⌉` values (`splitUpgradeTotal`):
     `k − 1` values of `MaxTokenValue` and the remainder. Each value is in `[1, MaxTokenValue]`,
     and they sum to the total. If `k` exceeds the validator's `ResourceLimits.MaxOutputs`
     (256 by default), the upgrade is refused before any proving, with an error stating `k` and
     the limit.
   - It gets the issuer identity for the type from the issuer wallet.
   - `Orchestrator.BuildUpgradeIssueAction` builds an ordinary ZKAT-SNARK issue of the `k`
     outputs, all to the single recipient. It adds the redeemed tokens as `Inputs` and reveals the
     type randomness.
   - It fills the issue metadata with one output entry per output note. `Inputs` holds the
     redeemed token IDs, and there are no extra signers.
4. **Endorsement.** The issuer collects the auditor endorsement and its own signature. The recipient
   accepts the transaction with `ttx.NewAcceptView`, as for any issue.

#### The upgrade issue action

`snarkactions.proto` adds two fields to `IssueAction`. A plain issue leaves both empty.

```
IssueAction {
  Issuer, TokenType, TypeCommitment, Outputs, BindingSignature, TotalValue, Metadata  // as before
  Inputs          []IssueActionInput{ID, Token}   // field 8: redeemed fabtoken outputs, ledger bytes verbatim
  TypeRandomness  []byte                          // field 9: opens TypeCommitment; set iff Inputs is non-empty
}
```

- `NumInputs`, `GetInputs` and `GetSerializedInputs` expose the inputs, so the translator spends them
  like transfer inputs (must exist, then delete).
- `ExtraSigners()` returns nil, as in ZKAT-DLOG.
- `Validate` rejects nil or incomplete inputs, duplicate input IDs (`ErrDuplicateUpgradeInput`), and
  `TypeRandomness` without inputs or the other way round. The input count is bounded by
  `ResourceLimits.MaxInputs`.

#### Validation (ledger)

The issue pipeline runs four checks, in order:

1. `IssueZKValidate`: the output proofs and the binding signature over `TotalValue`, unchanged.
2. `IssueSignatureValidate`: the issuer is authorized and signed. This is the only signature an
   upgrade issue needs.
3. `IssueUpgradeInputsValidate`, for an issue with inputs:
   - every input is a well-formed fabtoken output with a plain owner, the inputs are distinct, and
     they all have `TokenType`. Otherwise the action fails with `ErrMalformedAction` or
     `ErrTypeMismatch`;
   - `TypeCommitment == MiMC(enc(TokenType), TypeRandomness)`, with `TypeRandomness` canonical.
     This binds the type the outputs' proofs commit to; the cleartext `TokenType` alone is just a
     label (`ErrTypeMismatch`);
   - `TotalValue` equals the redeemed total. The binding signature already ties `TotalValue` to the
     outputs (`ErrUpgradeValueMismatch`).

   It consumes no signature. An issue that reveals `TypeRandomness` without inputs is malformed.
4. `common.IssueApplicationDataValidate`: the action metadata, as for any issue.

The checks of step 3 are in `Validator.validateUpgradeInputs`. `Validator.ValidateIssue`, the
local path behind `IssueService.VerifyIssue`, also runs them.

Any signature beyond the issuer's is rejected as unconsumed. The translator then checks and deletes
the key of every input, so each fabtoken is redeemed at most once, whichever path redeems it.

**Open issuer policy.** If `PP.Issuers()` is empty, `IssueSignatureValidate` accepts any issuer.
Since the owners' consent is checked off-chain, anyone can then burn other parties' fabtokens and
re-mint their value to a recipient of their choice. This matches ZKAT-DLOG. Deployments that hold
fabtokens should register their issuers.

#### Auditing

`AuditorService.checkUpgradeIssue` refuses an upgrade issue whose metadata does not describe it
exactly:

- the input token IDs, in order;
- each parsed input matches the auditor's own copy of that token from its token store, as
  returned by `QueryEngine.ListAuditTokens`. The owner, type and quantity must all match, and the
  quantities are compared as numbers at `PrecisionValue`, so `"0x10"` and `"16"` match. A missing
  copy is refused;
- every output note opens its output commitment and has the redeemed type;
- the note values sum to the redeemed total.

#### Requirements

- The issuer must hold the owners' verifiers, to check the upgrade proof. x509 always works; idemix
  needs the issuer key in the ZKAT-SNARK public parameters.
- Script, multisig and policy owners cannot sign the upgrade proof and are refused.
- The issuer must be authorized for the token type, as for any issue.
- The auditor must hold the redeemed fabtoken outputs in its token store, as for the in-place
  path, since it compares every input with its own copy.

### Upgrading ZKAT-DLOG tokens

ZKAT-DLOG outputs are upgraded **through the issuer** (burn-and-re-issue). The design mirrors the
fabtoken path and reuses the owner-consent protocol ZKAT-DLOG itself uses for commitment tokens of
an older generation of public parameters. The in-place path is a follow-up (see below).

#### Trust model and requirements

- **Trusted ZKAT-DLOG parameters.** The ZKAT-SNARK public parameters embed, verbatim, the serialized
  ZKAT-DLOG (`zkatdlognogh` v1) public parameters being migrated from, in
  `PublicParams.LegacyDLogPublicParams` (`snarkpp.proto` field 19, `legacy_dlog_public_params`). They
  are governed and hash-pinned with the rest of the ZKAT-SNARK public parameters. From them come:
  - the Pedersen generators, which `setup.PedersenGeneratorCoordsFromDLog` reads from the public
    parameters (never re-derived) and bakes into the migration circuit;
  - the ZKAT-DLOG token formats, computed by ZKAT-DLOG's own `token.CommTokenFormats`;
  - the public parameters hash carried by the owners' upgrade proofs.

  `pp.(*PublicParams).LegacyDLog()` parses and validates them. Only one ZKAT-DLOG generation is
  supported: its generators are compile-time constants of the migration circuit.
- **Migration keys.** `setup.SetupMigration(p)` compiles `circuit.MigrationCircuit` with those
  generators and fills `PKMigration`/`VKMigration`. It is not part of `SetupAll`.
- **Curves and proof systems.** Both deployments must be on BLS12-381: the ZKAT-SNARK `Curve` must be
  `BLS12_381`, and the ZKAT-DLOG curve one of `BLS12_381`, `BLS12_381_GURVY`, `BLS12_381_BBS` and
  `BLS12_381_BBS_GURVY`. The proof system must be `groth16` or `plonk`, not `plonkfri`. ZKAT-DLOG
  deployments on BN254 (the tokengen and NWO default) cannot be upgraded.
- **Fail closed.** `pp.Validate` refuses migration keys without ZKAT-DLOG parameters, and ZKAT-DLOG
  parameters that are malformed, on another curve, combined with a non-BLS12-381 ZKAT-SNARK curve or
  `plonkfri`, or without `VKMigration`. The validator refuses any ZKAT-DLOG upgrade with
  `ErrMigrationNotConfigured` when `VKMigration` or the ZKAT-DLOG parameters are absent, and the
  issuer refuses it without a migration prover.

#### The migration circuit

`circuit.MigrationCircuit` is a *legacy spend* proof for one redeemed ZKAT-DLOG output:

- public inputs: the Pedersen commitment `C` (BLS12-381 G1, emulated), the type scalar `TokenTypePed`,
  and the value commitment `cv`;
- private inputs: the value `v`, the Pedersen blinding factor `bf` and the value-commitment
  randomness `rcv`;
- constraints: `C = TokenTypePed·G0 + v·G1 + bf·G2`, `cv = v·V + rcv·R`, and `1 <= v < 2^MaxBits`.

Every public input is set by the validator. `C` is read from the ledger bytes of the redeemed token,
never from the prover, and `TokenTypePed = SHA256(TokenType) mod r` (`token.TokenTypePed`, ZKAT-DLOG's
`HashToZr`) is computed from the issue's cleartext `TokenType`. By the binding property of the
Pedersen commitment, a proof only exists if the redeemed token carries exactly that type. The
minted notes are ordinary `OutputCircuit` outputs.

#### Wire format

Each redeemed ZKAT-DLOG output is an `IssueActionInput` whose `migration` field (3) is set:

```
IssueActionInput {
  id, token                                       // as for fabtoken: ID and ledger bytes verbatim
  migration  MigrationDescription {               // field 3: set iff token is a ZKAT-DLOG output
    value_commit_in_x, value_commit_in_y          // cv of the redeemed value, 32 bytes each
    migration_proof                               // proof for MigrationCircuit
  }
}
```

The upgrade issue reveals `TokenType` and `TypeRandomness`, as for fabtoken, but `TotalValue` is the
zero encoding. Value is conserved by the binding signature over `Σcv_in − Σcv_out` with
`valueBalance = 0`, under the action type `issue-migration` (`token.ActionTypeIssueMigration`),
where each input's commitment in the action hash is its 96-byte Pedersen point `X‖Y`. One action
never mixes fabtoken and ZKAT-DLOG inputs. The former `MigrationAction` message is deprecated and
unused; it remains only for protobuf compatibility.

#### Protocol

1. **Request and proof (owner).** As for fabtoken: `ttx.RequestTokensUpgrade`, and
   `GenUpgradeProof` signs `SHA256(challenge ‖ tokens)` with every owner's key. ZKAT-DLOG formats are
   never in `SupportedTokenFormats`. The upgrade service resolves them, and their public parameters
   hash, through `token.LegacyDLogResolver`, which the driver builds from the embedded ZKAT-DLOG
   parameters.
2. **Issue (issuer).** After `ProcessTokensUpgradeRequest`, `IssueService.upgrade` routes an
   all-ZKAT-DLOG request to `upgradeDLog` and refuses a mixed one. `upgradeDLog` refuses, before any
   proving: no migration prover or ZKAT-DLOG parameters, an opening that does not match
   (`token.OpenDLogOutput`), a non-plain owner, mixed types, duplicate IDs, a value of 0 or above
   `MaxTokenValue`, an overflowing total, or more than `MaxOutputs` outputs. It then splits the total
   as for fabtoken and calls `Orchestrator.BuildDLogUpgradeIssueAction`, which proves the migrations
   and outputs concurrently with one shared type randomness.

#### Validation (ledger)

The same four issue validators run (`validator.go` is unchanged). For a ZKAT-DLOG upgrade:

1. `IssueZKValidate` checks the outputs as usual, then, for every input:
   - parses the ledger bytes as a `comm` typed ZKAT-DLOG output with a plain owner and a
     non-identity commitment on the configured ZKAT-DLOG curve (`token.SummarizeDLogInputs`);
   - checks that `cv` is canonical and on the embedded twisted Edwards curve;
   - verifies the migration proof against `VKMigration`, with `C` from the ledger bytes and
     `TokenTypePed` from `TokenType`.

   It also requires `TotalValue == 0` and verifies the binding signature with the migration value
   commitments, `issue-migration` and a value balance of 0.
2. `IssueSignatureValidate`: the issuer signature alone authorizes the upgrade.
3. `IssueUpgradeInputsValidate`: a single kind of input, a migration proof exactly on ZKAT-DLOG inputs,
   the input checks above, and `TypeCommitment == MiMC(enc(TokenType), TypeRandomness)`.
4. `common.IssueApplicationDataValidate`.

`Validator.ValidateIssue` runs the same checks. The translator checks and deletes the key derived
from each input's ID and ledger bytes, so each ZKAT-DLOG output is redeemed at most once, and only if
it exists on the ledger.

#### Auditing

`AuditorService.checkDLogUpgradeIssue` requires the input IDs in order, each input's owner to equal
the auditor's own copy of the token, the audited type to be `TokenType`, every output note to open
its commitment with that type, and the note values to sum to the audited input quantities.

#### What the upgrade reveals

The token type and the number of redeemed tokens are public, as on the fabtoken path. The values are
not: `TotalValue` is 0 and only value commitments are published. The issuer learns the openings,
the same trust model as ZKAT-DLOG's own commitment-token upgrade.

#### Wallets and the token store

`TokensService.Deobfuscate` and `Recipients` open or parse a not-yet-upgraded ZKAT-DLOG output with
the embedded parameters, so wallets, the token store and auditors keep handling it until it is
upgraded. Without ZKAT-DLOG parameters they return an error.

#### Follow-ups

- **In-place upgrade.** Spending a ZKAT-DLOG output inside a transfer keeps the type hidden, so the
  circuit would have to prove `SHA256(t) mod r` and `enc(t)` in-circuit (about 276k R1CS constraints
  for two SHA-256 hashes of a 64-byte type, against about 72k for the whole migration circuit), or
  reveal the type in such transfers.
- **ZKAT-DLOG on BN254.** It needs a circuit that emulates BN254 G1.
- **Key generation and integration tests.** tokengen and NWO do not yet emit `VKMigration` and the
  embedded ZKAT-DLOG parameters, and no integration test switches a network from ZKAT-DLOG to
  ZKAT-SNARK.
- **Several ZKAT-DLOG generations.** Each needs its own migration keys.

### Test coverage

- **Fabtoken parsing and summaries.** `token/upgrade_test.go` covers parsing, the input summary, the
  issue action's inputs and extra signers, the ledger key derived from the inputs, and the upgrade
  service.
- **Path split and opening.** `token/service_test.go` covers the split between the two paths and
  opening fabtoken outputs.
- **On-chain checks.** `validator/validator_issue_upgrade_test.go` exercises the full validator
  pipeline:
  - burn-and-re-issue end to end on both curves;
  - issuer-only signing, a missing issuer signature, an unconsumed owner signature;
  - value and type mismatches, a relabelled type, wrong or stripped type randomness;
  - duplicate inputs, and script, empty or non-fabtoken inputs;
  - the local `ValidateIssue` path rejecting a wrong `TotalValue` or `TypeRandomness`;
  - splitting a large total over several outputs with `MaxBits = 8`, and refusing a total that
    needs more than `MaxOutputs` outputs;
  - the auditor checks, including a note of the wrong type and inputs that differ from the
    auditor's copies;
  - in-place transfers end to end on both curves, including an HTLC script claim;
  - in-place tampering: wrong randomness, ledger value, type or owner changed, script owner,
    value above `MaxTokenValue`, upgrade randomness on a ZKAT-SNARK input.
- **Issuer branch.** `issue_upgrade_test.go` and `issue_split_internal_test.go` cover the argument
  checks of the upgrade branch and the split.
- **Driver wiring.** `driver/upgrade_wiring_test.go` checks that the services wired by the driver
  give every fabtoken format exactly one path, with the default parameters and with `MaxBits = 32`,
  and runs a burn-and-re-issue of a 64-bit fabtoken through that wiring.
- **ZKAT-DLOG upgrades.**
  - `circuit/migration_test.go`: valid openings, a wrong value, randomness or value commitment,
    another token's point, zero and overflowing values, and the type swap (an honest `"USD"`
    opening against the public type scalar of `"EUR"`).
  - `setup/setup_test.go`, `pp/migration_test.go`: generators read from a BBS ZKAT-DLOG deployment,
    refusal of BN254, `plonkfri` and missing ZKAT-DLOG parameters, every `pp.Validate` rule, and the
    field-19 round trip.
  - `token/dlog_test.go`: parsing and opening ZKAT-DLOG outputs, the resolver, the services, the
    input structure and the ledger key.
  - `validator/validator_issue_migration_test.go`: upgrades end to end with 1 and 3 inputs and a
    split, and the rejections: type swap, relabelled type, replaced or altered commitments, swapped
    proofs, value inflation, nonzero `TotalValue`, type randomness, missing proofs, mixed kinds,
    duplicates, script owners, a BN254 token, signatures, missing configuration, double spend and
    wrong ledger ID through the translator, and the auditor checks.
  - `issue_dlog_internal_test.go` and `driver/migration_test.go`: the issuer's refusals and the
    driver's routing of ZKAT-DLOG formats.
- **Lifecycle.** `regression.TestFabtokenInPlaceUpgrade_Lifecycle` upgrades a fabtoken in place,
  spends the resulting note, and checks that a forged value is rejected.
  `regression.TestDLogUpgrade_Lifecycle` upgrades a ZKAT-DLOG output through the issuer, commits it
  through the translator, spends the resulting note, and refuses a second upgrade.
- **Fuzzing.** `FuzzIssueActionWithInputsDeserializeNoPanic` (including migration descriptions),
  `FuzzParseFabtokenOutputNoPanic`, `FuzzParseDLogOutputNoPanic` and `FuzzOpenDLogOutputNoPanic`
  run nightly (`.github/workflows/nightly-fuzz.yml`).
