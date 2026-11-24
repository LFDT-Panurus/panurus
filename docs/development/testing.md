# Testing Guide

This document outlines how to run tests for Panurus.

## Getting Started

To work with Panurus and run tests, you first need to clone the repository and set up your environment.

### Clone the Repository

Clone the code and make sure it is on your `$GOPATH`.
(Important: we assume in this documentation and default configuration that your `$GOPATH` has a single root-directory!).
Sometimes, we use `$PANURUS_PATH` to refer to Panurus repository in your filesystem.

```bash
export PANURUS_PATH=$GOPATH/src/github.com/LFDT-Panurus/panurus
git clone https://github.com/LFDT-Panurus/panurus.git $PANURUS_PATH
```

## Prerequisites

Before running tests, ensure you have the necessary tools and environment set up.

Panurus uses a system called `NWO` from Fabric Smart Client for its integration tests and samples to programmatically create a fabric network along with the fabric-smart-client nodes.

1.  **Install Tools**:
    ```bash
    make install-tools
    ```

    After installing the tools, run the checks to verify that everything is in order:
    ```bash
    make checks
    ```

2.  **Download Fabric Binaries**:
    The integration tests require Hyperledger Fabric binaries.
    
    In order for a fabric network to be able to be created you need to ensure you have downloaded the appropriate version of the hyperledger fabric binaries from [Fabric Releases](https://github.com/hyperledger/fabric/releases) and unpack the compressed file onto your file system. This will create a directory structure of /bin and /config. You will then need to set the environment variable `FAB_BINS` to the `bin` directory.
    
    **Do not store the fabric binaries within your panurus cloned repo as this will cause problems running the samples and integration tests as they will not be able to install chaincode.**

    Almost all the samples and integration tests require the fabric binaries to be downloaded and the environment variable `FAB_BINS` set to point to the directory where these binaries are stored. One way to ensure this is to execute the following in the root of the panurus project:

    ```bash
    make download-fabric
    export FAB_BINS=$PWD/../fabric/bin
    ```
    
    You can also use this to download a different version of the fabric binaries, for example:
    ```shell
    FABRIC_VERSION=2.5 make download-fabric
    ```

3.  **Docker Images**:
    Build the necessary Docker images for testing.
    ```bash
    make testing-docker-images
    make docker-images
    ```

## Unit Tests

You can run unit tests using the following make targets:

-   **Standard Unit Tests**:
    ```bash
    make unit-tests
    ```
-   **Unit Tests with Race Detection**:
    ```bash
    make unit-tests-race
    ```
    CI runs this suite as three parallel shards, which you can also run on their own:
    `make unit-tests-race-db` (selector and storage packages; needs `make testing-docker-images`),
    `make unit-tests-race-core` (`token/core`), and `make unit-tests-race-rest` (everything else).
-   **Regression Unit Tests**:
    These tests ensure that recent changes have not reintroduced previously fixed bugs or broken existing functionality.
    ```bash
    make unit-tests-regression
    ```

## Fuzz Testing

Panurus uses Go's native fuzzing (`go test -fuzz`) to exercise the stateless token
validators with adversarial byte input. Fuzz targets assert that validation entry points
never panic on malformed or truncated data, and — where applicable — that structurally
valid encodings are still correctly accepted.

### Available Fuzz Targets

The tree currently holds **76** fuzz targets (70 distinct function names), grouped below by
subsystem. Function names are not unique across packages — for example
`FuzzActionDeserializerNoPanic` and `FuzzBulletProofVerifierNoPanic` exist in several —
so the `Package` column is what identifies a row. Paths are relative to the repository root.

All targets run in the nightly campaign except those marked with † below (see
[Nightly Fuzzing in CI](#nightly-fuzzing-in-ci)); `FuzzUnmarshalMetaNeverPanics` and
`FuzzCompositeRecipientValidationNeverPanics` in `token/services/ttx` are only exercised
through their seed corpus.

Targets in the separate `x/token/services/network/evm` Go module are listed in their own
section, and the target in `token/services/storage/db/kvs/hashicorp` is noted as a separate
module, because `go test` must be run from that module's root.

#### Token request and core drivers

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzVerifyTokenRequestFromRawNoPanic` | `token/core/common` | Raw `TokenRequest` bytes fed straight into request verification. |
| `FuzzStructuredTokenRequestSignatureEnvelope` | `token/core/common` | A structurally valid request with a mutated action-signature envelope (action ID, signature bytes, duplicate signatures). |
| `FuzzRequestResourceLimits` | `token/core/common` | Token requests shaped by action count, signature count and per-action/per-signature size; over-limit requests must be rejected with a typed error before signature verification. |
| `FuzzUnmarshallerNoPanic` | `token/core/common/encoding/asn1` | The unmarshaller used to deserialize every ZKAT-DLOG proof type, including the `Element.CurveID` bounds check. |
| `FuzzOwnerVerifierNoPanic` | `token/core/fabtoken/v1/driver` | Arbitrary owner-identity bytes through the FabToken owner-deserialization path reached once per input token before any signature check; it must neither panic nor recurse without bound. |
| `FuzzActionDeserializerNoPanic` | `token/core/fabtoken/v1/validator` | A single issue or transfer action's raw bytes through the FabToken `ActionDeserializer`. |
| `FuzzActionResourceLimits` | `token/core/fabtoken/v1/validator` | FabToken issue and transfer actions shaped by input/output counts; over-limit actions must be rejected with a typed error. |
| `FuzzTransferHTLCValidateNoPanic` | `token/core/fabtoken/v1/validator` | Attacker-controlled owner bytes of a transfer's inputs through `TransferHTLCValidate`; an HTLC-owned input must never be accepted alongside other inputs. |
| `FuzzTransferValidationStepsNoPanic` | `token/core/fabtoken/v1/validator` | A transfer action deserialized from fuzzed bytes and run through the four transfer validation steps in a fuzz-chosen order. |
| `FuzzUnmarshalTypedTokenNoPanic` | `token/services/tokens` | Untrusted, ledger-stored or peer-supplied token bytes through `UnmarshalTypedToken`. |
| `FuzzUnmarshalTypedMetadataNoPanic` | `token/services/tokens` | Arbitrary bytes through `UnmarshalTypedMetadata`. |
| `FuzzTokenRequestMetadataFromBytesNoPanic` | `token/driver` | Metadata bytes received from remote peers through `TokenRequestMetadata.FromBytes`. |
| `FuzzIdentityConfigurationUniqueIDInjective` | `token/driver` | Arbitrary `(ID, Type, URL)` tuples, checking that two identity configurations share a `conf_id` if and only if they are the same tuple. |
| `FuzzConfigKeyFieldRoundTrip` | `token/driver` | `escapeConfigKeyField`, checking that decoding its output reproduces the input byte for byte. |

#### ZKAT-DLOG validators and proofs

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzActionDeserializerNoPanic` | `token/core/zkatdlog/nogh/v1/validator` | A single issue or transfer action's raw bytes through the ZKAT-DLOG `ActionDeserializer`, including both Range and CSP proof-type encodings. |
| `FuzzActionDeserializerMultiActionNoPanic` | `token/core/zkatdlog/nogh/v1/validator` | Two independently typed, independently fuzzed actions in the same `TokenRequest`. |
| `FuzzActionResourceLimits` | `token/core/zkatdlog/nogh/v1/validator` | ZKAT-DLOG issue and transfer actions shaped by input/output counts and proof length; over-limit actions must be rejected before any cryptographic work. |
| `FuzzIssueActionValidateNoPanic` | `token/core/zkatdlog/nogh/v1/validator` | `issue.Action.Deserialize`, `Validate` and `GetCommitments` on arbitrary bytes — the calls `IssueValidate` makes before any issuer-authorization or signature check. |
| `FuzzBulletProofVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/issue` | Raw BulletProof issue-proof bytes through `BulletProofVerifier.Verify` — the call `IssueValidate` makes on unauthenticated wire bytes. |
| `FuzzCSPVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/issue` | Raw CSP issue-proof bytes through `CSPVerifier.Verify`. |
| `FuzzBulletProofVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/transfer` | Raw BulletProof transfer-proof bytes through `BulletProofVerifier.Verify` — the call `TransferValidate` makes on unauthenticated wire bytes. |
| `FuzzCSPVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/transfer` | Raw CSP transfer-proof bytes through `CSPVerifier.Verify`. |
| `FuzzRangeProofDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof` | Arbitrary bytes through BulletProof `RangeProof.Deserialize`/`Validate`/`Verify` against a fixed, valid public statement. |
| `FuzzIPADeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof` | The same chain for the standalone inner-product-argument (`IPA`) sub-proof. |
| `FuzzCSPRangeProofDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/crypto/rp/csp` | Arbitrary bytes through CSP `RangeProof.Deserialize`/`Validate`/`Verify` against a fixed, valid public statement. |
| `FuzzTokenDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/token` | Raw ledger-stored output bytes through `Token.Deserialize`. |
| `FuzzMetadataDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/token` | Raw ledger-stored metadata bytes through `Metadata.Deserialize`. |
| `FuzzPublicParamsDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/setup` | Raw ledger-stored public parameters through `NewPublicParamsFromBytes`, exercised by every validator/prover/verifier on startup and params update. |
| `FuzzProofDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/crypto/upgrade` | Raw untrusted token-upgrade request bytes through `Proof.Deserialize`. |
| `FuzzProcessCommTokenNoPanic` | `token/core/zkatdlog/nogh/v1/crypto/upgrade` | The commitment and opening carried by an upgrade request, through `Service.processCommToken` and the commitment arithmetic behind it. |

#### Identity and wallets

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzDecodeIdentityNoPanic` | `token/services/identity/marshal` | Malformed DER through `DecodeIdentity`, the base ASN.1 parser underneath every identity type. |
| `FuzzParseNoPanic` | `token/services/identity/boolpolicy` | Policy strings through `Parse`, which must terminate within its length, nesting-depth and node-count caps. |
| `FuzzPolicyIdentityDeserializeNoPanic` | `token/services/identity/boolpolicy` | Malformed ASN.1 through `PolicyIdentity.Deserialize`. |
| `FuzzPolicySignatureFromBytesNoPanic` | `token/services/identity/boolpolicy` | Peer-supplied signature bytes through `PolicySignature.FromBytes`. |
| `FuzzDeserializeAuditInfoNoPanic` | `token/services/identity/boolpolicy` | A policy identity and its wrapped audit info through the policy `AuditInfoDeserializer`, including nested policies and unknown member types. |
| `FuzzMultiIdentityDeserializeNoPanic` | `token/services/identity/multisig` | Malformed ASN.1 through `MultiIdentity.Deserialize`, including its canonicality contract. |
| `FuzzMultiSignatureFromBytesNoPanic` | `token/services/identity/multisig` | Peer-supplied signature bytes through `MultiSignature.FromBytes`. |
| `FuzzDeserializeAuditInfoNoPanic` | `token/services/identity/idemix/crypto` | Malformed JSON audit-info bytes through the Idemix `DeserializeAuditInfo`. |
| `FuzzDeserializeAuditInfoNoPanic` | `token/services/identity/idemixnym/nym` | Malformed JSON signer-info bytes through the Idemix pseudonym `DeserializeAuditInfo`. |
| `FuzzVerifyOwnerNoPanic` | `token/services/identity/interop/htlc` | The raw owner field of a token being spent through `VerifyOwner` (typed-identity unmarshaller and embedded `htlc.Script` JSON). |
| `FuzzMetadataClaimKeyCheckNoPanic` | `token/services/identity/interop/htlc` | An unlocking party's claim signature through `MetadataClaimKeyCheck`. |
| `FuzzLongTermOwnerWalletRegisterRecipientNoPanic` | `token/services/identity/role` | Recipient data (typed-identity envelope, boolpolicy ASN.1, audit-info JSON) through `LongTermOwnerWallet.RegisterRecipient`. |
| `FuzzRegisterRecipientIdentityStructuralGuard` | `token/services/identity/wallet` | A counterparty's identity and audit-info bytes through `Service.RegisterRecipientIdentity`, asserting the structural guard rejects exactly the out-of-bounds lengths and never panics. |

#### Transaction, interop and NFT services

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzClaimVerifierVerifyNoPanic` | `token/services/interop/htlc` | A claim signature and the hash function/encoding read from the spent token's script through `ClaimVerifier.Verify`. |
| `FuzzUnmarshalMetaNeverPanics` † | `token/services/ttx` | Arbitrary bytes through `UnmarshalMeta`. |
| `FuzzCompositeRecipientValidationNeverPanics` † | `token/services/ttx` | Multisig and policy recipient data, node and recipient lists, and policy strings through the composite recipient validators. |
| `FuzzStateAtNoPanic` | `token/services/nfttx` | Arbitrary output-type bytes through `OutputStream.StateAt`, which must never report a success it cannot back up. |

#### Storage

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzGetTableNamesNoPanic` | `token/services/storage/db/sql/common` | Arbitrary network/channel/namespace triples through table-name construction, which must neither panic nor produce unsafe names. |
| `FuzzKeysetFromRawNoPanic` | `token/services/storage/db/sql/query/pagination` | A serialized pagination token from an untrusted client through the keyset decoder and the pagination methods that follow. |
| `FuzzCheckStoredTokenRequest` | `token/services/storage/integrity` | A transaction ID and stored token-request bytes through `CheckStoredTokenRequest`, checking that success implies the anchor matches the ID. |
| `FuzzCheckTokenRequestActions` | `token/services/storage/integrity` | Stored token-request bytes through `CheckTokenRequestActions`. |
| `FuzzNormalizeIDRoundTrip` | `token/services/storage/db/kvs/hashicorp` (separate module) | Composite keys mapped to a Vault path and back, checking the mapping is injective. |

#### Network (Fabric and Chaguar)

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzQueryTokensNoPanic` | `token/services/network/fabric/tcc` | The raw, caller-controlled argument of the `queryTokens` chaincode function. |
| `FuzzQueryStatesLedgerReadsAreBounded` | `token/services/network/fabric/tcc` | Untrusted key lists through `QueryStates`, which must not panic or exceed `MaxQueryItems` ledger reads. |
| `FuzzAreTokensSpentLedgerReadsAreBounded` | `token/services/network/fabric/tcc` | The same bound for `AreTokensSpent`, including the key-translator path when graph hiding is on. |
| `FuzzQueryTokensLedgerReadsAreBounded` | `token/services/network/fabric/tcc` | The same bound for `QueryTokens`, which decodes an array of `token.ID` structs. |
| `FuzzEndorserForThresholdRuleNoPanic` | `token/services/network/fabricx/endorsement` | Malformed `ThresholdRule` public keys, as returned by the Chaguar `GetNamespacePolicies` RPC, through `endorserForThresholdRule`. |

#### EVM driver (separate module)

These targets live in the `x/token/services/network/evm` module, so run them from that
directory (for example `cd x/token/services/network/evm && go test ./abi -run='^$' -fuzz='^FuzzDecodeBytes$'`).
Package paths below are relative to that module root.

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzDecodeBytes` | `abi` | The ABI dynamic-bytes decoder over an EVM node's response. |
| `FuzzDecodeBoolArray` | `abi` | The `bool[]` decoder used to read `areTokensSpent`. |
| `FuzzDecodeUint64` | `abi` | The `uint64` decoder used for the public-parameters version; out-of-range values must be rejected, not truncated. |
| `FuzzDecodeBytes32` | `abi` | The fixed-width decoder; a short response must never be read past its end. |
| `FuzzHexToAddress` | `client` | The strict address parser. |
| `FuzzHexToHash` | `client` | The hash parser used for transaction hashes and anchors. |
| `FuzzBytesToAddress` | `client` | The right-aligning byte-to-address conversion on arbitrary-length input. |
| `FuzzJSONLogToLog` | `client` | The `eth_getLogs` record decoder. |
| `FuzzJSONReceiptToReceipt` | `client` | The `eth_getTransactionReceipt` record decoder, including nested logs. |
| `FuzzParseHexQuantities` | `client` | The two JSON-RPC hex-quantity decoders (chain ID, fees, gas, nonce, block number, receipt status). |
| `FuzzRecoverAddress` | `eip712` | The endorsement signature parser, fed by a remote endorser before it is trusted. |
| `FuzzNewSignerFromBytes` | `eip712` | The private-key parser; a corrupt key file must be an error, not a panic. |
| `FuzzHashStructNoPanic` | `eip712` | `HashStruct` (via `Digest`) over an arbitrary peer-supplied `StateDelta`. |
| `FuzzEndorseResponseDecode` | `endorsement` | The JSON decode of an `EndorseResponse` plus the initiator-side checks run on it. |
| `FuzzSetupDeltaBuild` | `endorsement` | `SetupDeltaFactory.Build` on public parameters and anchor taken from a remote `EndorseRequest`. |
| `FuzzEnvelopeFromBytes` | `.` (module root) | The envelope wire decoder. |
| `FuzzComputeAnchor` | `.` (module root) | Anchor derivation over a nonce and a peer-supplied creator identity. |
| `FuzzAnchorFromTxID` | `keys` | The anchor decoder applied to a token request's `TxId` before the request is validated. |
| `FuzzStateDeltaValidate` | `statedelta` | The JSON decode plus `Validate` of a peer's `StateDelta`. |

### Running the Seed Corpus

Every fuzz target ships with an `f.Add` seed corpus of deterministic valid and malformed
inputs. These seeds run automatically as ordinary subtests whenever the package's unit
tests run — no special flag is needed:

```bash
go test ./token/core/zkatdlog/nogh/v1/validator/...
# or, for the whole repo:
make unit-tests
```

### Running an Actual Fuzz Campaign

To mutate the seed corpus and search for new crashing or panicking inputs, run a specific
target with `-fuzz` and bound the run with `-fuzztime`:

```bash
go test ./token/core/zkatdlog/nogh/v1/validator/... \
  -fuzz=FuzzActionDeserializerNoPanic -fuzztime=30s
```

Omit `-fuzztime` to let the fuzzer run until you stop it (`Ctrl+C`). If it finds a
failing input, Go writes it to `testdata/fuzz/<FuzzTestName>/<hash>` in the target's
package — commit that file to add the regression permanently to the seed corpus, then
re-run the failing case as a normal test with `go test -run=<FuzzTestName>/<hash>`.

### Nightly Fuzzing in CI

`.github/workflows/nightly-fuzz.yml` runs all fuzz targets above not marked † for up to `4h`
each night (02:17 UTC) against `main`, as a `fail-fast: false` matrix so one crashing target
never stops the others. The EVM and Vault targets run from their own module roots, set by
the matrix entry's `dir` field. It can also be triggered manually via `workflow_dispatch`,
which accepts a `fuzztime` input to shorten or lengthen the campaign.

If a target finds a crashing or panicking input, the workflow:

- uploads the failing seed (`testdata/fuzz/<FuzzTestName>/<hash>`) and the full run log
  as a build artifact, retained for 30 days;
- auto-files a GitHub issue titled `Nightly fuzz failure: <target>` (labels `testing`,
  `bug`), or comments on the existing one if that target already has an open issue, with
  the run link and the local repro command.

Confirmed crashes should have their seed file committed into the target's
`testdata/fuzz/<FuzzTestName>/` directory (see above) to add the regression permanently
to the corpus, closing out the issue once fixed.

### Notes

- Fuzz harnesses cap the size of generated byte slices (see `maxFuzzActionBytes` /
  `maxFuzzRequestBytes` / `maxFuzzSignatureBytes` in each `*_fuzz_test.go` file) purely to
  keep local runs fast. These caps are a fuzzing-harness convenience, not protocol rules.
- Proof-verifier fuzz harnesses build their token commitments with a **fixed** blinding
  factor (`fuzzBlindingFactor` in `token/core/zkatdlog/nogh/v1/issue/verifier_fuzz_test.go`,
  via `GetTokensWithWitnessAndBF`), never the random `GetTokensWithWitness`. Some panics —
  e.g. the cross-curve `proof.u` panic in the CSP range verifier — are only reached after a
  proof's proof-of-knowledge check passes, and that check binds to the token commitments.
  With random per-run blinding factors a committed crash seed's proof fails the PoK check
  against freshly-randomised tokens and returns an error instead of panicking, so the seed
  cannot reproduce the crash and the regression corpus is worthless. Any harness that feeds
  a proof verifier must keep its tokens deterministic for committed seeds to be meaningful.

## Integration Tests

Integration tests are crucial for verifying the interaction between different components.

Run specific integration tests using the `integration-tests-<target>` pattern.

### Common Test Targets

Here are some common integration test targets (refer to `.github/workflows/tests.yml` or `integration/` folder for a full list):

-   `dlog-fabric-t1`
-   `fabtoken-fabric-t1`
-   `nft-dlog`
-   `nft-fabtoken`
-   `dvp-fabtoken`
-   `interop-fabtoken-t1`

### Example Usage

To run the `dlog-fabric-t1` test:

```bash
make integration-tests-dlog-fabric-t1
```

### Sharing One Network Across Specs

Most suites build a whole network for every spec with `BeforeEach(ts.Setup)` /
`AfterEach(ts.TearDown)`. Specs that leave the network healthy can instead share one network
by calling `token.ShareNetwork` (`integration/token/test_utils.go`) inside an
`Ordered, ContinueOnFailure` container, the Panurus counterpart of FSC's
`faultrecovery.ShareNetwork`:

```go
Describe("Multisig and Redeem to yourself", Ordered, ContinueOnFailure, t.Label, func() {
	ts, selector := newTestSuite(...)
	token2.ShareNetwork(ts)
	It("Multisig succeeded", Label("T12"), func() { fungible.TestMultiSig(ts.II, selector) })
	It("Test redeem", Label("T13"), func() { fungible.TestRedeem(ts.II, selector, "default") })
})
```

The lifecycle is:

- **Initial build**: the network is built in `BeforeAll`, before the first selected spec. The
  `DeferCleanup` that tears it down is registered *before* the build starts, so a network whose
  build failed part-way is still torn down and does not hold the ports that later containers of
  the suite reuse. If the initial build fails, Ginkgo fails that spec and skips the rest of the
  container.
- **Rebuild after a failure**: a spec that fails, including in a `DeferCleanup` it registered
  itself, may leave the network broken, so the network is torn down and rebuilt before the next
  spec runs. If that rebuild fails, the spec it was run for fails before its body runs (it is
  not skipped, so the run stays red), and every later spec of the container is skipped rather
  than retrying the rebuild.
- **Final teardown**: the network is torn down after the last spec of the container, after that
  spec's own cleanups. Under `--fail-fast`, Ginkgo skips every spec after the first failure and
  never runs that final cleanup, so the network is torn down right after the spec that failed.
- **Suite timeout**: if the Ginkgo suite timeout (default 1h) expires, the network of the
  timed-out container is not torn down, as with FSC's `faultrecovery.ShareNetwork`. A CI runner
  is discarded anyway; locally, run `make clean-all-containers`.

Because of this, the suite passed to `token.ShareNetwork` must implement `token.Network`
with a `TearDown` that is safe to call on a network whose `Setup` failed part-way (or never
ran) and that is idempotent. `token.TestSuite` does so; a suite embedding FSC's
`*integration.TestSuite` must override `TearDown`, for example with `token.StopInfrastructure`
(see the `testSuite` of `integration/token/fungible/dlogx`). `TearDown` clears `II` only once
`Stop` returns, so if `Stop` panics part-way the final cleanup retries it once. It does not stop
the build server or remove the test directory of an infrastructure whose `Generate` never ran;
those hold no ports in the start range and are cleaned up by `CleanupBuildArtifacts` and
`DeleteOnStart`.

Labels stay on the specs, so a label filter that selects one spec still runs it on a network of
its own; a CI job gains only when its filter selects several specs of the container
(e.g. `make integration-tests-dlog-fabric-t12-t13`).

Only group specs that:

- leave every node running and the public parameters and node configuration unchanged
  (a spec that updates the public parameters or revokes an identity may only run after every
  spec it would affect, see below);
- do not assert absolute balances, holdings or issued totals on a wallet and token type that an
  earlier spec of the container also touched, and stay together within the issuer's USD quota
  (241, enforced by the test issuer view).

#### Sharing a network with `TestAll`

`fungible.TestAll` asserts the whole audited and accepted transaction history, the issuer's
history and the issuer's USD quota, so it only passes on a fresh network. The dlog suite
(`integration/token/fungible/dlog`) still shares its network with the cheaper specs of the same
topology, in the containers "T1 Fungible with Auditor ne Issuer and extras" (T1, T17, T4, T5,
T3, T2.1, T2) and "Fungible with Auditor = Issuer" (T6, T7); the fabtoken suite does the same in
its "Fungible" container (T1, T17, T4, T5, T3, T2). The rules are:

- **`TestAll` runs first.** It checks this itself: it fails at once, with "TestAll must run on a
  fresh network (first spec of a ShareNetwork container)", if the auditor has already audited a
  transaction. The check only looks at that auditor's audit history; its point is a clear error,
  since `TestAll`'s own first history assertions would fail anyway.
- **Every later spec uses its own token type**, with `fungible.WithTokenType`, accepted by
  `TestRedeemUnauthorizedIssuer`, `TestRemoteOwnerWallet(WithWMP)`, `TestRevokeIdentity` and
  `TestPublicParamsUpdate` (the default stays `"USD"`; `TestPublicParamsUpdate` also takes
  `fungible.WithRestartAfterUpdate`). Balances, holdings, spending
  and the auditor's limits are per type, and the USD quota applies to the exact type `"USD"`
  only, so a spec with its own type does not see what `TestAll` left behind. Two specs that
  assert absolute balances of the same wallets (T4 and T5) need different types from each other
  too. The type must not contain `.`: the sherdlock selector keys its cache by wallet ID and type
  joined by `.`, and wallet IDs are dotted (`alice.id1`). The suites use `USDt17`, `USDt4`, and
  so on.
- **Specs that change the network for good go last**, ordered so that no change affects a spec
  that runs after it: T3 revokes bob, so it runs after T4 and T5, which move tokens to bob; T2.1
  and T2 change the public parameters, so they close the container.
- **Re-running a setup step must be harmless.** For example, T17 authorizes the issuer for
  redeem, which `TestAll` already did; that is a KVS overwrite, so it changes nothing. A spec that
  needs the *absence* of such state (T13 expects no authorized redeem issuer) cannot run after
  `TestAll`. Likewise T17 relies on `newIssuer` not being in the public parameters, so it runs
  before T2.1/T2, which add it.
- **Public parameters updates start from the genesis file.** `PrepareUpdatedPublicParams` reads
  the public parameters file written at network generation, which `UpdatePublicParams` does not
  rewrite, so T2.1 and T7 drop the `issuer.ExtraId` issuer that `TestAll` added. No later spec
  uses it; a spec that needs an earlier update to persist must not follow one.

To pick the order for a new container: start with `TestAll`, then the specs that only add state
under their own token type, then those that change shared state (revocations), then those that
change the public parameters, and check each against the ones before it with the rules above.
The make targets `integration-tests-dlog-fabric-aries-{websocket,libp2p,replicas}`,
`integration-tests-dlog-fabric-t6-t7-{websocket,libp2p,replicas}` and
`integration-tests-fabtoken-fabric-all-{websocket,libp2p,replicas}` run each container once per
infrastructure type; the single-label targets (e.g. `integration-tests-dlog-fabric-t17`) still
run one spec on a network of its own.

Co-locating `TestAll` with the small specs is a tradeoff: it cuts the dlog jobs for these specs
from 9 to 3, but each grouped job runs `TestAll` plus every small spec back to back, so the
`aries-replicas` job becomes the longest of them (estimated 22–28 minutes, well under Ginkgo's
1h default suite timeout). If it becomes the critical path of the itest workflow, split the
container into `TestAll` alone plus a shared container for the small specs.

#### Sharing a network on Chaguar

The Chaguar suite (`integration/token/fungible/dlogx`) shares networks too, with two
differences from the dlog suite:

- **No `TestAll`-first container for the small specs.** Its "Extras" container (T12, T13, T4,
  T5, T3) has no `TestAll` in front: `TestAll` authorizes the issuer for redeem, which T13
  needs not to have happened, and the Chaguar `TestAll` jobs (T1, T6, T16) are already the
  longest of the itest workflow, so adding specs behind them would lengthen the critical path.
  The container follows the rules above: T12 and T13 issue 220 USD between them, within the
  USD quota; T4, T5 and T3 each use their own token type; T3 runs after T4 and T5, and last.
  "Extras" runs on the `Aries|WithEndorsers|WebEnabled` topology, so T12 and T13 now also get
  the FSC web server, which they do not use.
- **The spec that replaces the issuers must be the last public parameters update.** Chaguar
  installs the public parameters through the FSC-endorsement setup path, where every update must
  be signed by a *current* issuer whose signer the `issuer` node holds
  (`signPublicParamsWithCurrentIssuer` in
  `token/services/network/fabric/endorsement/fsc/service.go`). T2 and T7 replace the issuers
  with `newIssuer`, so nothing can update the public parameters after them. T2.1 appends
  `newIssuer`, so T2 should be able to follow it, but that order has not been run on Chaguar.

T2.1, T2 and T7 failed locally on Chaguar, each on its own network (a `GetPublicParams`
timeout right after the update; see
[#1945](https://github.com/LFDT-Panurus/panurus/issues/1945)). T2.1 and T2 therefore stay in a
container with a network per spec ("Public params update"), and T6 and T7 do not share a
network; all three are disabled in CI anyway. Once they pass, add T2.1 then T2 after T3 in
"Extras", each with its own token type, and T7 after T6.

CI runs the specs of "Extras" it enabled before, T13, T4, T5 and T3, with
`make integration-tests-fabricx-dlog-extras`: all three infrastructure types in one job, one
network each; `-extras-{websocket,libp2p,replicas}` run one type each. To enable T12, T2.1 or
T2 in CI, widen `FABRICX_DLOG_EXTRAS_FILTER` in `fabricx.mk` rather than adding a job, and
split the job per infrastructure type if it then gets close to the longest itest jobs.

### Enabling the Token Platform Without a TMS

By default, an FSC node only gets `token: enabled: true` written into its generated
`core.yaml` if it belongs to at least one TMS (`Topology.AddTMS`). `Topology.EnableTokenPlatform`
(`integration/nwo/token/topology.go`) lets a topology install and start the token platform on
every node of an `fsc.Topology` with **no TMS defined at all**:

```go
tokenTopology := token.NewTopology()
tokenTopology.EnableTokenPlatform(fscTopology)
```

This only affects configuration generation — the base `token:` stanza from the extension
template, without any TMS-specific selector or driver configuration. It does not attach the
token Go SDK to the nodes; do that separately with `Topology.SetSDK` or
`fscTopology.AddSDK(...)`, as usual.

The token SDK starts up cleanly with zero TMS configs (all its dependencies are lazily
provided and `network.Provider.Connect()` is a no-op with no configured networks), but
`GetManagementService(...)` still returns "no token management service configs found" until a
TMS is registered. `EnableTokenPlatform` is meant as the config-generation groundwork for
bringing up nodes that will have a TMS registered dynamically at runtime, once that
capability exists — it is not itself a way to use tokens.

## Chaguar Tests

For tests involving Chaguar (starts with `fabricx`), you need additional setup:

```bash
make fxconfig configtxgen fabricx-docker-images
make integration-tests-fabricx-dlog-t1
```

## Cleaning Up

After running tests, especially integration tests that spin up Docker containers, you might want to clean up your environment.

```bash
# Clean up Docker artifacts (containers, volumes, networks) and generated files
make clean

# Remove all Docker containers (running and stopped)
make clean-all-containers

# Remove Fabric peer images
make clean-fabric-peer-images
```
