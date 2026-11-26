/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package regression_test

import (
	"encoding/base64"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	math "github.com/IBM/mathlib"
	pp3 "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	ppproto "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	testing2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/testutils"
	sbenchmark "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	idemixTestdataPath  = "../testdata"
	testGeneratorDomain = "regression.domain.v1"
)

// TestPublicParamsValidate checks the generators of the stored corpora and of the stored
// public-parameter sets under testdata/generated.
func TestPublicParamsValidate(t *testing.T) {
	t.Parallel()

	t.Run("stored", testStoredPublicParams)
	t.Run("external", testExternalPublicParams)
	t.Run("generated", testGeneratedPublicParams)
}

// testStoredPublicParams checks the stored public parameters: they declare no generator domain,
// so they validate as they are, and declaring a domain makes them fail. Their token requests are
// the stored vectors verified by TestRegression. They cannot be regenerated: the signer keys
// embedded in the stored parameters are not kept.
func testStoredPublicParams(t *testing.T) {
	t.Parallel()

	configurations := []string{
		"32-BLS12_381_BBS_GURVY",
		"32-BN254",
		"64-BLS12_381_BBS_GURVY",
		"64-BN254",
	}
	roots := []string{
		path.Join("testdata", "zero"),
		path.Join("testdata", "zero", "csp"),
		path.Join("testdata", "zero", "open-policy"),
	}

	for _, root := range roots {
		for _, config := range configurations {
			dir := path.Join(root, config)
			t.Run(dir, func(t *testing.T) {
				t.Parallel()
				paramsData, err := testDataFS.ReadFile(path.Join(dir, "params.txt"))
				require.NoError(t, err)
				raw, err := base64.StdEncoding.DecodeString(string(paramsData))
				require.NoError(t, err)

				pp, err := setup.NewPublicParamsFromBytes(raw, setup.DLogNoGHDriverName, setup.ProtocolV1)
				require.NoError(t, err)
				assert.Empty(t, pp.GeneratorDomain)
				require.NoError(t, pp.Validate())

				pp.GeneratorDomain = "other"
				require.Error(t, pp.Validate())
			})
		}
	}
}

// externalDir holds the external public parameters. It is not committed, so it may be missing.
var externalDir = path.Join("testdata", "external")

// loadExternal loads the external public parameters testdata/external/<name>.json. It returns
// false if the file is missing.
func loadExternal(t *testing.T, name string) (*setup.PublicParams, bool) {
	t.Helper()

	raw, err := testDataFS.ReadFile(path.Join(externalDir, name+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	require.NoError(t, err)
	pp, err := setup.NewPublicParamsFromBytes(raw, setup.DLogNoGHDriverName, setup.ProtocolV1)
	require.NoError(t, err)

	return pp, true
}

// testExternalPublicParams checks the external public parameters under testdata/external, produced
// outside this repository: they validate as they are, and declaring another domain makes them fail.
// No request can be signed against them, because their idemix issuer is not in the testdata. Their
// generators are reused by the sets under testdata/generated/custom/<name>. The directory is not committed:
// the test is skipped without it.
func testExternalPublicParams(t *testing.T) {
	t.Parallel()

	files, err := fs.ReadDir(testDataFS, externalDir)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s not found", externalDir)
	}
	require.NoError(t, err)
	for _, file := range files {
		name := strings.TrimSuffix(file.Name(), ".json")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pp, ok := loadExternal(t, name)
			require.True(t, ok)
			require.NoError(t, pp.Validate())

			pp.GeneratorDomain += "other"
			require.Error(t, pp.Validate())
		})
	}
}

// customDir is the directory under testdata/generated that holds the sets generated from external
// public parameters, one mode per external file.
const customDir = "custom"

// generatedSet is a stored public-parameter set under testdata/generated/<mode>/<proof>/<config>,
// or testdata/generated/custom/<mode>/<proof>/<config> for the external modes.
type generatedSet struct {
	dir    string
	mode   string
	proof  string
	config string
}

// listGeneratedSets returns the stored sets, found by walking testdata/generated.
func listGeneratedSets(t *testing.T) []generatedSet {
	t.Helper()

	root := path.Join("testdata", "generated")
	var sets []generatedSet
	modes, err := fs.ReadDir(testDataFS, root)
	require.NoError(t, err)
	for _, mode := range modes {
		if mode.Name() != customDir {
			sets = append(sets, listModeSets(t, path.Join(root, mode.Name()), mode.Name())...)

			continue
		}
		customModes, err := fs.ReadDir(testDataFS, path.Join(root, customDir))
		require.NoError(t, err)
		for _, customMode := range customModes {
			sets = append(sets, listModeSets(t, path.Join(root, customDir, customMode.Name()), customMode.Name())...)
		}
	}

	return sets
}

// listModeSets returns the sets under dir/<proof>/<config>, with the given mode.
func listModeSets(t *testing.T, dir, mode string) []generatedSet {
	t.Helper()

	var sets []generatedSet
	proofs, err := fs.ReadDir(testDataFS, dir)
	require.NoError(t, err)
	for _, proof := range proofs {
		configs, err := fs.ReadDir(testDataFS, path.Join(dir, proof.Name()))
		require.NoError(t, err)
		for _, config := range configs {
			sets = append(sets, generatedSet{
				dir:    path.Join(dir, proof.Name(), config.Name()),
				mode:   mode,
				proof:  proof.Name(),
				config: config.Name(),
			})
		}
	}

	return sets
}

func loadSet(t *testing.T, set generatedSet) (*benchmark.SetupConfigurations, *setup.PublicParams) {
	t.Helper()

	configurations, err := benchmark.LoadSetupConfigurations(testDataFS, set.dir, benchmark.SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		OwnerIdentityType:  idemixnym.IdentityType,
	})
	require.NoError(t, err)
	require.Len(t, configurations.Configurations, 1)
	for _, cfg := range configurations.Configurations {
		return configurations, cfg.PP
	}

	return nil, nil
}

// testGeneratedPublicParams loads the stored public-parameter sets (testdata/generated), with the
// signers registered in them, generates requests of every kind against them, and verifies them
// with the same code as TestRegression. The sets are produced by testdata/zero/generator.
func testGeneratedPublicParams(t *testing.T) {
	t.Parallel()

	sets := listGeneratedSets(t)
	// domain and legacy modes: (ipa, csp, open-policy) x 4 configurations each; the other modes
	// come from external parameters
	internal := 0
	for _, set := range sets {
		if set.mode == "domain" || set.mode == "legacy" {
			internal++
		}
	}
	require.Equal(t, 24, internal)

	for _, set := range sets {
		t.Run(set.dir, func(t *testing.T) {
			t.Parallel()
			testGenerated(t, set)
		})
	}

	t.Run("domain/changed-after-generation", testDomainChangedAfterGeneration)
}

func testGenerated(t *testing.T, set generatedSet) {
	t.Helper()

	configurations, pp := loadSet(t, set)
	checkGeneratorProperty(t, set, pp)
	bits, curveID := bitLength(pp), pp.Curve

	ppRaw, err := pp.Serialize()
	require.NoError(t, err)

	if set.proof == "open-policy" {
		openEnv, err := testing2.NewOpenPolicyEnv(&sbenchmark.Case{Bits: bits, CurveID: curveID, NumInputs: 1, NumOutputs: 2}, configurations)
		require.NoError(t, err)
		openCases := map[string]testing2.TestCase{}
		addCase(t, openCases, "open_policy_issue_0", openEnv.OpenPolicyIssueToTestCase)
		addCase(t, openCases, "open_policy_redeem_0", openEnv.OpenPolicyRedeemToTestCase)
		verifyTestCases(t, ppRaw, openCases)

		return
	}

	cases := map[string]testing2.TestCase{}
	// 1x1 for every kind
	env, err := testing2.NewEnv(&sbenchmark.Case{Bits: bits, CurveID: curveID, NumInputs: 1, NumOutputs: 1}, configurations)
	require.NoError(t, err)
	addCase(t, cases, "transfers_i1_o1_0", env.TransferToTestCase)
	addCase(t, cases, "issues_i1_o1_0", env.IssueToTestCase)
	addCase(t, cases, "redeems_i1_o1_0", env.RedeemToTestCase)
	addCase(t, cases, "swaps_i1_o1_0", env.SwapToTestCase)

	// 2x2 for transfers and the fixed-shape scenarios
	env, err = testing2.NewEnv(&sbenchmark.Case{Bits: bits, CurveID: curveID, NumInputs: 2, NumOutputs: 2}, configurations)
	require.NoError(t, err)
	addCase(t, cases, "transfers_i2_o2_0", env.TransferToTestCase)
	addCase(t, cases, "upgrade_witness_0", env.UpgradeWitnessTransferToTestCase)
	addCase(t, cases, "pub_metadata_issue_0", env.PublicMetadataIssueToTestCase)
	addCase(t, cases, "pub_metadata_transfer_0", env.PublicMetadataTransferToTestCase)
	addCase(t, cases, "unclaimed_metadata_0", env.UnclaimedMetadataToTestCase)
	addCase(t, cases, "multi_auditor_0", env.MultiAuditorTransferToTestCase)
	addCase(t, cases, "extra_signature_0", env.ExtraSignatureToTestCase)
	verifyTestCases(t, ppRaw, cases)
}

// testDomainChangedAfterGeneration checks that requests generated with a generator domain are
// refused when the parameters declare another domain.
func testDomainChangedAfterGeneration(t *testing.T) {
	t.Parallel()

	set := generatedSet{
		dir:    path.Join("testdata", "generated", "domain", "ipa", "32-BN254"),
		mode:   "domain",
		proof:  "ipa",
		config: "32-BN254",
	}
	configurations, pp := loadSet(t, set)
	env, err := testing2.NewEnv(&sbenchmark.Case{Bits: bitLength(pp), CurveID: pp.Curve, NumInputs: 1, NumOutputs: 1}, configurations)
	require.NoError(t, err)
	cases := map[string]testing2.TestCase{}
	addCase(t, cases, "transfers_i1_o1_0", env.TransferToTestCase)

	ppRaw, err := pp.Serialize()
	require.NoError(t, err)
	verifyTestCases(t, ppRaw, cases)

	_, _, err = tokenServicesFactory(withGeneratorDomain(t, ppRaw, "other"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not match the publicly derived generators")
}

// bitLength returns the range-proof bit length of pp.
func bitLength(pp *setup.PublicParams) uint64 {
	if pp.RangeProofParams != nil {
		return pp.RangeProofParams.BitLength
	}

	return pp.CSPRangeProofParams.BitLength
}

// withGeneratorDomain returns the serialized public parameters with the generator domain replaced,
// bypassing the validation done by Serialize.
func withGeneratorDomain(t *testing.T, raw []byte, domain string) []byte {
	t.Helper()

	container, err := pp3.Unmarshal(raw)
	require.NoError(t, err)
	inner := &ppproto.PublicParameters{}
	require.NoError(t, proto.Unmarshal(container.Raw, inner))
	inner.GeneratorDomain = domain
	container.Raw, err = proto.Marshal(inner)
	require.NoError(t, err)
	out, err := pp3.Marshal(container)
	require.NoError(t, err)

	return out
}

// derivedGenerators returns the generators that setup derives with the default domain for the
// parameters of pp.
func derivedGenerators(t *testing.T, pp *setup.PublicParams) *setup.PublicParams {
	t.Helper()

	proofType := rp.RangeProofType
	if pp.CSPRangeProofParams != nil {
		proofType = rp.CSPRangeProofType
	}
	def, err := setup.NewWith(setup.SetupParams{
		DriverName:     setup.DLogNoGHDriverName,
		DriverVersion:  setup.ProtocolV1,
		BitLength:      bitLength(pp),
		IdemixIssuerPK: pp.IdemixIssuerPublicKeys[0].PublicKey,
		ProofType:      proofType,
		CurveID:        pp.Curve,
	})
	require.NoError(t, err)

	return def
}

func addCase(t *testing.T, cases map[string]testing2.TestCase, key string, gen func() (*testing2.TestCase, error)) {
	t.Helper()

	tc, err := gen()
	require.NoError(t, err, "failed generating test case %s", key)
	cases[key] = *tc
}

func equalAny(a, b []*math.G1) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equals(b[i]) {
			return false
		}
	}

	return true
}

// checkGeneratorProperty asserts the generator domain and that the generators are derived (domain
// mode), are not (legacy mode), or are those of testdata/external/<mode>.json (any other mode,
// checked only if the file is present).
func checkGeneratorProperty(t *testing.T, set generatedSet, pp *setup.PublicParams) {
	t.Helper()

	require.NoError(t, pp.Validate())

	switch set.mode {
	case "domain", "legacy":
	default:
		src, ok := loadExternal(t, set.mode)
		if !ok {
			return
		}
		assert.Equal(t, src.GeneratorDomain, pp.GeneratorDomain)
		assert.Equal(t, src.Curve, pp.Curve)
		assert.True(t, equalAny(pp.PedersenGenerators, src.PedersenGenerators), "pedersen generators differ from the external ones")
		require.NotNil(t, pp.RangeProofParams)
		require.NotNil(t, src.RangeProofParams)
		assert.Equal(t, src.RangeProofParams.BitLength, pp.RangeProofParams.BitLength)
		assert.Equal(t, src.RangeProofParams.NumberOfRounds, pp.RangeProofParams.NumberOfRounds)
		assert.True(t, src.RangeProofParams.P.Equals(pp.RangeProofParams.P), "range proof P differs from the external one")
		assert.True(t, src.RangeProofParams.Q.Equals(pp.RangeProofParams.Q), "range proof Q differs from the external one")
		assert.True(t, equalAny(pp.RangeProofParams.LeftGenerators, src.RangeProofParams.LeftGenerators), "range proof left generators differ from the external ones")
		assert.True(t, equalAny(pp.RangeProofParams.RightGenerators, src.RangeProofParams.RightGenerators), "range proof right generators differ from the external ones")

		return
	}

	if set.mode == "domain" {
		assert.Equal(t, testGeneratorDomain, pp.GeneratorDomain)
		wrong := *pp
		wrong.GeneratorDomain = "other"
		require.Error(t, wrong.Validate())

		return
	}

	assert.Empty(t, pp.GeneratorDomain)
	def := derivedGenerators(t, pp)
	assert.False(t, equalAny(pp.PedersenGenerators, def.PedersenGenerators), "pedersen generators are still derived")
	if pp.RangeProofParams != nil {
		assert.False(t, equalAny(pp.RangeProofParams.LeftGenerators, def.RangeProofParams.LeftGenerators), "range proof generators are still derived")
	}
	if pp.CSPRangeProofParams != nil {
		assert.False(t, equalAny(pp.CSPRangeProofParams.LeftGenerators, def.CSPRangeProofParams.LeftGenerators), "csp range proof generators are still derived")
	}
}
