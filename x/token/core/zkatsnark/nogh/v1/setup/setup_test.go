/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package setup_test

import (
	"fmt"
	"math/big"
	"sync"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
)

// ── Shared setup (Setup is expensive; run once for all key tests) ────

var (
	setupOnce sync.Once
	sharedPP  *pp.PublicParams
	setupErr  error
)

func setupSharedPP(t *testing.T) *pp.PublicParams {
	t.Helper()
	setupOnce.Do(func() {
		p := pp.DefaultPublicParams()
		var err error
		p, err = setup.SetupAll(p)
		if err != nil {
			setupErr = fmt.Errorf("shared setup.SetupAll: %w", err)

			return
		}
		sharedPP = p
	})
	if setupErr != nil {
		t.Fatalf("shared setup failed: %v", setupErr)
	}

	return sharedPP
}

// dummyGens returns trivial non-nil big.Int coordinates suitable for
// compilation-only tests.
func dummyGens() setup.PedersenGeneratorCoords {
	one := new(big.Int).SetInt64(1)

	return setup.PedersenGeneratorCoords{
		G0X: one, G0Y: one,
		G1X: one, G1Y: one,
		G2X: one, G2Y: one,
	}
}

func TestCompileSpendCircuit_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	cs, err := setup.CompileSpendCircuit(p)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "SpendCircuit must have constraints")
}

func TestCompileOutputCircuit_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	cs, err := setup.CompileOutputCircuit(p)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "OutputCircuit must have constraints")
}

func TestCompileOutputCircuit_ZeroMaxBits_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = 0
	_, err := setup.CompileOutputCircuit(p)
	require.Error(t, err)
}

func TestCompileOutputCircuit_NegativeMaxBits_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = -1
	_, err := setup.CompileOutputCircuit(p)
	require.Error(t, err)
}

func TestCompileMigrationCircuit_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	cs, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "MigrationCircuit must have constraints")
}

func TestCompileMigrationCircuit_ZeroMaxBits_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = 0
	_, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.Error(t, err)
}

// ── SetupAll ──────────────────────────────────────────────────────────────────

func TestSetupAll_PopulatesKeys(t *testing.T) {
	p := setupSharedPP(t)
	require.NotEmpty(t, p.PKSpend, "PKSpend must be populated after SetupAll")
	require.NotEmpty(t, p.VKSpend, "VKSpend must be populated after SetupAll")
	require.NotEmpty(t, p.PKOutput, "PKOutput must be populated after SetupAll")
	require.NotEmpty(t, p.VKOutput, "VKOutput must be populated after SetupAll")
}

func TestSetupAll_UnsupportedProofSystem_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "invalid-proof-system"
	_, err := setup.SetupAll(p)
	require.Error(t, err)
}

// ── LoadProvingKeyBytes / LoadVerifyingKeyBytes ──────────────────────────────

func TestLoadProvingKeyBytes_SpendCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	pk, err := setup.LoadProvingKeyBytes(p, setup.CircuitSpend)
	require.NoError(t, err)
	require.NotEmpty(t, pk)
}

func TestLoadVerifyingKeyBytes_SpendCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	vk, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitSpend)
	require.NoError(t, err)
	require.NotEmpty(t, vk)
}

func TestLoadProvingKeyBytes_OutputCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	pk, err := setup.LoadProvingKeyBytes(p, setup.CircuitOutput)
	require.NoError(t, err)
	require.NotEmpty(t, pk)
}

func TestLoadVerifyingKeyBytes_OutputCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	vk, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitOutput)
	require.NoError(t, err)
	require.NotEmpty(t, vk)
}

func TestLoadProvingKeyBytes_MissingKey_Error(t *testing.T) {
	p := pp.DefaultPublicParams() // no keys set
	_, err := setup.LoadProvingKeyBytes(p, setup.CircuitSpend)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestLoadVerifyingKeyBytes_MissingKey_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	_, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitOutput)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestLoadProvingKeyBytes_UnknownCircuit_Error(t *testing.T) {
	p := setupSharedPP(t)
	_, err := setup.LoadProvingKeyBytes(p, "nonexistent-circuit")
	require.Error(t, err)
}

func TestLoadVerifyingKeyBytes_UnknownCircuit_Error(t *testing.T) {
	p := setupSharedPP(t)
	_, err := setup.LoadVerifyingKeyBytes(p, "nonexistent-circuit")
	require.Error(t, err)
}

func TestLoadProvingKeyBytes_MigrationMissing_Error(t *testing.T) {
	// SetupAll does NOT populate migration keys.
	p := setupSharedPP(t)
	_, err := setup.LoadProvingKeyBytes(p, setup.CircuitMigration)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestLoadVerifyingKeyBytes_MigrationMissing_Error(t *testing.T) {
	p := setupSharedPP(t)
	_, err := setup.LoadVerifyingKeyBytes(p, setup.CircuitMigration)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

// ── SetupMigration ────────────────────────────────────────────────────────────

func TestSetupMigration_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	_, err := dlogpp.Embed(p, dlogpp.DefaultCurve)
	require.NoError(t, err)
	p, err = setup.SetupMigration(p)
	require.NoError(t, err)
	require.NotEmpty(t, p.PKMigration)
	require.NotEmpty(t, p.VKMigration)
	require.NoError(t, p.Validate())
}

// TestSetupMigration_NoLegacyDLog checks that migration keys cannot be set up
// without the ZKAT-DLOG public parameters whose generators they bake in.
func TestSetupMigration_NoLegacyDLog(t *testing.T) {
	_, err := setup.SetupMigration(pp.DefaultPublicParams())
	require.ErrorIs(t, err, pp.ErrNoLegacyDLog)
}

// TestPedersenGeneratorCoordsFromDLog checks that the generators are read
// from the ZKAT-DLOG public parameters: the BBS curve's hash-to-curve yields
// other generators than the plain BLS12-381 one, so a re-derivation would
// produce a circuit that cannot open BBS-curve commitments.
func TestPedersenGeneratorCoordsFromDLog(t *testing.T) {
	bbs, _, err := dlogpp.New(mathlib.BLS12_381_BBS_GURVY)
	require.NoError(t, err)
	plain, _, err := dlogpp.New(mathlib.BLS12_381_GURVY)
	require.NoError(t, err)

	gBBS, err := setup.PedersenGeneratorCoordsFromDLog(bbs)
	require.NoError(t, err)
	gPlain, err := setup.PedersenGeneratorCoordsFromDLog(plain)
	require.NoError(t, err)
	require.NotZero(t, gBBS.G0X.Cmp(gPlain.G0X), "BBS and plain BLS12-381 generators must differ")

	var want bls12381.G1Affine
	_, err = want.SetBytes(bbs.PedersenGenerators[1].Bytes())
	require.NoError(t, err)
	require.Zero(t, gBBS.G1X.Cmp(want.X.BigInt(new(big.Int))))
	require.Zero(t, gBBS.G1Y.Cmp(want.Y.BigInt(new(big.Int))))

	p := pp.DefaultPublicParams()
	p.LegacyDLogPublicParams, err = bbs.Serialize()
	require.NoError(t, err)
	fromPP, err := setup.MigrationGenerators(p)
	require.NoError(t, err)
	require.Equal(t, gBBS, fromPP)

	_, err = setup.PedersenGeneratorCoordsFromDLog(nil)
	require.Error(t, err)

	bn, _, err := dlogpp.New(mathlib.BN254)
	require.NoError(t, err)
	_, err = setup.PedersenGeneratorCoordsFromDLog(bn)
	require.Error(t, err, "BN254 ZKAT-DLOG generators cannot be opened by the migration circuit")

	short := *bbs
	short.PedersenGenerators = bbs.PedersenGenerators[:2]
	_, err = setup.PedersenGeneratorCoordsFromDLog(&short)
	require.Error(t, err)
}

// TestCompileMigrationCircuit_RejectsBN254AndPlonkFRI checks that the
// migration circuit is only compiled over the BLS12-381 scalar field, and not
// with plonkfri.
func TestCompileMigrationCircuit_RejectsBN254AndPlonkFRI(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BN254
	_, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.Error(t, err)

	p = pp.DefaultPublicParams()
	p.ProofSystem = "plonkfri"
	_, err = setup.CompileMigrationCircuit(p, dummyGens())
	require.Error(t, err)

	p = pp.DefaultPublicParams()
	_, err = setup.CompileMigrationCircuit(p, setup.PedersenGeneratorCoords{})
	require.Error(t, err, "missing generators")
}

func TestSetupAll_OutputCircuitError(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = 0
	_, err := setup.SetupAll(p)
	require.Error(t, err)
}

// ── BN254 compile/setup tests ───────────────────────────────────────────────

func TestCompileSpendCircuit_BN254_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BN254
	cs, err := setup.CompileSpendCircuit(p)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "BN254 SpendCircuit must have constraints")
}

func TestCompileOutputCircuit_BN254_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BN254
	cs, err := setup.CompileOutputCircuit(p)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "BN254 OutputCircuit must have constraints")
}

func TestSetupAll_BN254_PopulatesKeys(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BN254
	p, err := setup.SetupAll(p)
	require.NoError(t, err)
	require.NotEmpty(t, p.PKSpend, "BN254 PKSpend must be populated")
	require.NotEmpty(t, p.VKSpend, "BN254 VKSpend must be populated")
	require.NotEmpty(t, p.PKOutput, "BN254 PKOutput must be populated")
	require.NotEmpty(t, p.VKOutput, "BN254 VKOutput must be populated")
}

// ── Plonk compile/setup tests ───────────────────────────────────────────────

func TestCompileSpendCircuit_Plonk_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	cs, err := setup.CompileSpendCircuit(p)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "Plonk SpendCircuit must have constraints")
}

func TestCompileOutputCircuit_Plonk_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	cs, err := setup.CompileOutputCircuit(p)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "Plonk OutputCircuit must have constraints")
}

func TestSetupAll_Plonk_PopulatesKeys(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	p, err := setup.SetupAll(p)
	require.NoError(t, err)
	require.NotEmpty(t, p.PKSpend, "Plonk PKSpend must be populated")
	require.NotEmpty(t, p.VKSpend, "Plonk VKSpend must be populated")
	require.NotEmpty(t, p.PKOutput, "Plonk PKOutput must be populated")
	require.NotEmpty(t, p.VKOutput, "Plonk VKOutput must be populated")
}

// ── PlonkFRI compile/setup tests ────────────────────────────────────────────

func plonkFRIPP() *pp.PublicParams {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonkfri"
	p.Curve = ecc.BN254

	return p
}

func TestCompileCircuits_PlonkFRI_Success(t *testing.T) {
	spend, err := setup.CompileSpendCircuit(plonkFRIPP())
	require.NoError(t, err)
	require.Positive(t, spend.GetNbConstraints(), "PlonkFRI SpendCircuit must have constraints")

	output, err := setup.CompileOutputCircuit(plonkFRIPP())
	require.NoError(t, err)
	require.Positive(t, output.GetNbConstraints(), "PlonkFRI OutputCircuit must have constraints")
}

func TestSetupAll_PlonkFRI_PopulatesKeys(t *testing.T) {
	p, err := setup.SetupAll(plonkFRIPP())
	require.NoError(t, err)
	require.NotEmpty(t, p.PKSpend, "PlonkFRI PKSpend must be populated")
	require.NotEmpty(t, p.VKSpend, "PlonkFRI VKSpend must be populated")
	require.NotEmpty(t, p.PKOutput, "PlonkFRI PKOutput must be populated")
	require.NotEmpty(t, p.VKOutput, "PlonkFRI VKOutput must be populated")
}

func TestSetupAll_PlonkFRI_UnsupportedCurve_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonkfri"
	p.Curve = ecc.BLS12_381
	_, err := setup.SetupAll(p)
	require.Error(t, err)
}

// ── Unsupported curve error path ────────────────────────────────────────────

func TestCompileSpendCircuit_UnsupportedCurve_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BW6_761
	_, err := setup.CompileSpendCircuit(p)
	require.Error(t, err)
}

func TestCompileOutputCircuit_UnsupportedCurve_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BW6_761
	_, err := setup.CompileOutputCircuit(p)
	require.Error(t, err)
}

func TestCompileMigrationCircuit_UnsupportedCurve_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BW6_761
	_, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.Error(t, err)
}

func TestCompileSpendCircuit_UnsupportedProofSystem_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "invalid"
	_, err := setup.CompileSpendCircuit(p)
	require.Error(t, err)
}

func TestCompileOutputCircuit_UnsupportedProofSystem_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "invalid"
	_, err := setup.CompileOutputCircuit(p)
	require.Error(t, err)
}

func TestCompileMigrationCircuit_UnsupportedProofSystem_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "invalid"
	_, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.Error(t, err)
}

func TestSetupMigration_UnsupportedProofSystem_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "invalid"
	_, err := dlogpp.Embed(p, dlogpp.DefaultCurve)
	require.NoError(t, err)
	_, err = setup.SetupMigration(p)
	require.Error(t, err)
}

func TestCompileMigrationCircuit_Plonk_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	cs, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "Plonk MigrationCircuit must have constraints")
}
