# ZKAT-SNARK Driver

This page covers the [proof systems](#proof-systems) the driver supports, how the driver is
[wired](#driver-wiring) into a token management service, how it
[upgrades tokens](#token-upgrades) created by other drivers, and the
[regression vectors](#regression-vectors) that freeze its wire formats.

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
  The request-level limits do apply to zkatsnark: a `plonkfri` transfer carries one ~841 KiB proof
  per input and output, so operators must raise `maxRequestBytes` and `maxActionBytes` under
  `token.validation.limits` (see [Driver wiring](#driver-wiring)); the defaults (256 KiB per action)
  reject `plonkfri` requests.

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
- **Output public inputs.** `OutputCircuit`'s four public inputs (`CommitmentOut`, `ValueCommitOutX/Y`,
  `TypeCommitment`) must be tagged `gnark:",public"`, with the leading comma. gnark reads the text before the
  first comma as the field name, so `gnark:"public"` left them secret. The output verifying key then bound no
  public input, and the validator's public-only witness (`publicWitnessForOutput`) was empty, so an output proof
  verified against any commitment. The tag is fixed, which changes the output constraint system, so output keys
  of existing public parameters must be regenerated (`setup.SetupAll`). `TestOutputCircuitPublicInputs`,
  `TestOutputCircuitRejectsTamperedPublicWitness` (`nogh/v1/circuit`) and
  `TestPublicWitnessForOutput_BindsAllPublicInputs` (`nogh/v1/validator`) pin the layout.
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

## Ownership Authorization

The driver decides whether a token is owned by a local wallet (`IsMine`), was issued by a local issuer (`Issued`), and whether this node is an auditor (`AmIAnAuditor`) through `common.NewStandardAuthorization` (`token/core/common/authorization.go`), the same chain fabtoken and zkatdlog use. The chain is first-match-wins. The type-gated htlc, multisig and boolpolicy authorizations run first and record escrow-typed tokens under prefixed ownership ids (`htlc.sender<wallet>`, `htlc.recipient<wallet>`, `escrow<wallet>`, `policy<wallet>`). The wallet-based authorization runs last as the catch-all for plain owners. If it ran first, it would claim an escrow token whose composite identity is bound to a wallet under that wallet id and drop its ownership entries. See [Identity Service](../services/identity.md) for how to add a new escrow type. The ordering is covered by `TestAuthorizationOrder_BoundPolicyIdentity` in `token/services/ttx/boolpolicy/auth_test.go`.

## Driver Wiring

`Driver.NewTokenService` (`x/token/core/zkatsnark/nogh/v1/driver/driver.go`) builds a TMS the same way
the ZKAT-DLOG driver does:

- **Resource limits.** `NewTokenDriver` takes the `driver.ResourceLimitsProvider` registered by
  `token/sdk/dig` (backed by the `token.validation.limits` configuration key, see
  [Validation Resource Limits](validation-resource-limits.md)). `NewTokenService` resolves it per TMS
  and passes the limits to the validator, which stamps them onto every deserialized action. The same
  limits bound the burn-and-re-issue split (`IssueService.maxOutputs`). Configured limits are
  therefore enforced on the endorser side too, consistently with the standalone validator driver.
- **Circuit breaker.** The wallet service is wrapped with
  `observability.NewWalletServiceDecoratorWithConfig`, configured from the TMS `circuitBreaker` key
  (disabled by default) and the TMS-scoped metrics provider. The decorated wallet service backs the
  authorization, the issue and transfer services and the token service. The standalone
  `WalletServiceFactory` applies the same decorator with a `disabled.Provider`.
- **Policy identities.** `NewEIDRHDeserializer` registers `boolpolicy.NewAuditInfoDeserializer(d)`
  with the parent multiplex, so a `boolpolicy` identity whose components share an enrollment ID
  resolves to it, and malformed component audit info is reported as an error.

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
| ZKAT-DLOG | Not yet supported | Not yet supported (proof only, see below) |

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
     (256 by default, configurable via `token.validation.limits`), the upgrade is refused before
     any proving, with an error stating `k` and the limit.
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

ZKAT-DLOG outputs are not yet upgradable on either path. The building blocks exist, but nothing
ties them to the ledger.

- **Circuit.** `circuit.MigrationCircuit` proves that a ZKAT-DLOG Pedersen commitment
  `C = t_ped·G0 + v·G1 + bf·G2` on BLS12-381 G1 (emulated in the circuit) opens to the same value as
  a new note commitment, value commitment and type commitment.
- **Prover.** `prover.BuildMigrationWitness` and `Orchestrator.BuildMigrationAction` produce a
  `MigrationAction`.
- **Validator.** `validator.ValidateMigration` checks its proof against `VKMigration`.
- **Proof systems.** The circuit uses emulated arithmetic and BSB22 commitments, so it needs a
  BLS12-381 deployment and is unavailable with `plonkfri`.

`ValidateMigration` is **not** in the validator pipeline, and the action is not bound to anything on
the ledger:

- it has no input ID, so the ledger does not spend the ZKAT-DLOG token;
- nothing authorizes it: no owner or issuer signature covers it, including its `Recipient`;
- the circuit does not constrain the Pedersen type encoding `t_ped = HashToZr(t)` to the note's type
  `enc(t)`, so the type can be swapped.

A successful `ValidateMigration` is therefore not an authorization to mint. To support ZKAT-DLOG
upgrades, the action must follow the fabtoken design:

- **In place.** A ZKAT-DLOG input would be a transfer input whose spend description carries a
  migration proof instead of a spend proof. Its `CommitmentPedersen` would have to equal the ledger
  token's commitment, and the owner would sign.
- **Through the issuer.** The ZKAT-DLOG tokens would be the issue's `Inputs`, each with its migration
  proof, authorized by the issuer as for fabtoken.

Both also need the type link above to be constrained in the circuit.

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
- **Lifecycle.** `regression.TestFabtokenInPlaceUpgrade_Lifecycle` upgrades a fabtoken in place,
  spends the resulting note, and checks that a forged value is rejected.
- **Fuzzing.** `FuzzIssueActionWithInputsDeserializeNoPanic` and `FuzzParseFabtokenOutputNoPanic`
  run nightly (`.github/workflows/nightly-fuzz.yml`).
- **Frozen vectors.** `regression.TestGoldenVectors` replays frozen issue, transfer, redeem and swap
  requests against frozen public parameters (see [Regression Vectors](#regression-vectors)).

## Regression Vectors

The tests in `x/token/core/zkatsnark/nogh/v1/regression` prove and verify in the same run, so a change
that alters the prover and the validator together goes unnoticed by them. `regression/golden_test.go`
(`TestGoldenVectors`) closes that gap: it replays requests generated earlier and committed under
`regression/testdata`, like the zkatdlog vectors in `token/core/zkatdlog/nogh/v1/regression/testdata`.

### Layout

Each configuration has its own directory, `<proof system>-<curve>-<MaxBits>`:

| Directory | Why it is in the matrix | Cases |
|-----------|-------------------------|-------|
| `groth16-bn254-16`     | groth16 on BN254 with a small range check | all |
| `groth16-bls12_381-64` | the production default (`pp.DefaultPublicParams`) | all |
| `plonk-bls12_381-16`   | plonk with a universal KZG SRS | all |
| `plonkfri-bn254-16`    | the transparent proof system | `issues_0`, `transfers_0` |

Each directory contains two files:

- `params.txt`: the base64-encoded serialized public parameters. The proving keys are stripped,
  because validation needs only the verifying keys. This keeps the vectors small (about 5 MiB in total,
  most of it the plonkfri proofs, which are about 841 KiB each).
- `testdata.json`: one `testutils.TestCase` per fixture, keyed by name. Each case holds:
  - the serialized token request (`req_raw`) and its anchor (`txid`);
  - the request metadata and the audit inputs;
  - the ledger entries its transfer inputs are looked up in (`ledger`). For plonkfri this field is
    omitted, because it equals the request's `InputTokens`, and the test rebuilds the ledger from
    them.
  - the expected outcome (`expected_error`). It is empty for a request that must validate.
    Otherwise it is a substring of the expected error, as for `unclaimed_metadata_0`
    (`more metadata than those validated`) and `extra_signature_0` (`unconsumed signatures`).

### What the test checks

For each configuration, the test:

1. decodes the public parameters, checks the proof system, curve and `MaxBits`, checks that they
   re-serialize to the frozen bytes, and builds the validator through the production entry point
   (`core.NewValidatorDriverService` with `driver.NewValidatorDriver`);
2. validates every request with `token.Validator.UnmarshallAndVerifyWithMetadata` and checks the
   expected outcome, and the number and kind of actions;
3. checks that every action re-serializes to its frozen bytes. Actions with a metadata map are
   compared as decoded protobuf messages, because protobuf map order is not deterministic;
4. tampers with the frozen proofs and commitments of one issue and one transfer, and checks that
   validation then fails. This shows that the frozen verifying keys are actually used.

### Regenerating

```bash
cd x/token/core/zkatsnark/nogh/v1/regression/testdata/generator
go run .                              # every configuration
go run . -configs=groth16-bn254-16    # a single configuration
```

The setup is randomized, so regenerating replaces every byte of the chosen configurations. The vectors
are meant to fail when the wire format changes. Regenerate them only on purpose, and explain the
break in the pull request. Changes that need new vectors include:

- the spend or output circuits, which change the verifying keys;
- the gnark version, if it changes the key or proof encoding;
- the public parameters or action protobufs, or the type and commitment encodings;
- a protobuf library bump that changes the `proto.Marshal` output.

The generator's ECDSA test certificates expire after 24 hours. Validation does not check their
expiry, so the vectors stay valid.
