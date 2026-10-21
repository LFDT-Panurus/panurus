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

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
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
	p, err := setup.SetupMigration(p, dummyGens())
	require.NoError(t, err)
	require.NotEmpty(t, p.PKMigration)
	require.NotEmpty(t, p.VKMigration)
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
	_, err := setup.SetupMigration(p, dummyGens())
	require.Error(t, err)
}

func TestCompileMigrationCircuit_Plonk_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	cs, err := setup.CompileMigrationCircuit(p, dummyGens())
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "Plonk MigrationCircuit must have constraints")
}
