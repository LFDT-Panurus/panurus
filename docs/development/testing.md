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

| Target | Package | What it fuzzes |
| --- | --- | --- |
| `FuzzVerifyTokenRequestFromRawNoPanic` | `token/core/common` | Raw `TokenRequest` bytes fed straight into request verification. |
| `FuzzStructuredTokenRequestSignatureEnvelope` | `token/core/common` | A structurally valid request with a mutated action-signature envelope (action ID, signature bytes, duplicate signatures). |
| `FuzzActionDeserializerNoPanic` | `token/core/fabtoken/v1/validator` | A single issue or transfer action's raw bytes through the FabToken `ActionDeserializer`. |
| `FuzzActionDeserializerNoPanic` | `token/core/zkatdlog/nogh/v1/validator` | A single issue or transfer action's raw bytes through the ZKAT-DLOG `ActionDeserializer`, including both Range and CSP proof-type encodings. |
| `FuzzActionDeserializerMultiActionNoPanic` | `token/core/zkatdlog/nogh/v1/validator` | Two independently typed, independently fuzzed actions in the same `TokenRequest`. |
| `FuzzBulletProofVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/issue` | Raw BulletProof issue-proof bytes through `BulletProofVerifier.Verify` — the call `IssueValidate` makes on unauthenticated wire bytes. |
| `FuzzCSPVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/issue` | Raw CSP issue-proof bytes through `CSPVerifier.Verify`. |
| `FuzzBulletProofVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/transfer` | Raw BulletProof transfer-proof bytes through `BulletProofVerifier.Verify` — the call `TransferValidate` makes on unauthenticated wire bytes. |
| `FuzzCSPVerifierNoPanic` | `token/core/zkatdlog/nogh/v1/transfer` | Raw CSP transfer-proof bytes through `CSPVerifier.Verify`. |
| `FuzzIssueVerifierCommitmentNoPanic` | `token/core/zkatdlog/nogh/v1/issue` | An issue output's commitment wire bytes alongside the proof bytes, covering the attacker-controlled commitments the proof-only targets hold fixed. |
| `FuzzTransferVerifierCommitmentNoPanic` | `token/core/zkatdlog/nogh/v1/transfer` | A transfer's input and output commitment wire bytes alongside the proof bytes, through both proof types. |
| `FuzzTokenDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/token` | Raw ledger-stored output bytes through `Token.Deserialize`. |
| `FuzzMetadataDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/token` | Raw ledger-stored metadata bytes through `Metadata.Deserialize`. |
| `FuzzToClearNoPanic` | `token/core/zkatdlog/nogh/v1/token` | An output's and its metadata's wire bytes together through `Token.ToClear`, where the two meet in curve arithmetic — the pair `VerifyIssue`/`VerifyTransfer` check before any proof is verified. |
| `FuzzPublicParamsDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/setup` | Raw ledger-stored public parameters through `NewPublicParamsFromBytes`, exercised by every validator/prover/verifier on startup and params update. |
| `FuzzProofDeserializeNoPanic` | `token/core/zkatdlog/nogh/v1/crypto/upgrade` | Raw untrusted token-upgrade request bytes through `Proof.Deserialize`. |
| `FuzzRegisterRecipientIdentityStructuralGuard` | `token/services/identity/wallet` | A counterparty's identity and audit-info bytes through `Service.RegisterRecipientIdentity`, asserting the structural guard rejects exactly the out-of-bounds lengths and never panics. |

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

`.github/workflows/nightly-fuzz.yml` runs every fuzz target above for up to `4h` each
night (02:17 UTC) against `main`, as a `fail-fast: false` matrix so one crashing target
never stops the others. It can also be triggered manually via `workflow_dispatch`, which
accepts a `fuzztime` input to shorten or lengthen the campaign.

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

## Fabric-X Tests

For tests involving Fabric-X (starts with `fabricx`), you need additional setup:

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
