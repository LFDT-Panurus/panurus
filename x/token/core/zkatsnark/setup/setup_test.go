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
	"github.com/consensys/gnark/backend/groth16"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
)

// ── Shared setup (groth16.Setup is expensive; run once for all key tests) ────

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
// compilation-only tests. The values are not valid Pedersen generators (they
// don't correspond to any real curve point), but gnark only reads them at
// compile time to wire constants into the R1CS, so any non-nil value works
// for compilation tests. Do not use these for proof generation.
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

func TestSetupAll_NonGroth16_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	_, err := setup.SetupAll(p)
	require.ErrorIs(t, err, setup.ErrNotImplemented)
}

// ── LoadProvingKey / LoadVerifyingKey ─────────────────────────────────────────

func TestLoadProvingKey_SpendCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	pk, err := setup.LoadProvingKey(p, setup.CircuitSpend)
	require.NoError(t, err)
	require.NotNil(t, pk)
}

func TestLoadVerifyingKey_SpendCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	vk, err := setup.LoadVerifyingKey(p, setup.CircuitSpend)
	require.NoError(t, err)
	require.NotNil(t, vk)
}

func TestLoadProvingKey_OutputCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	pk, err := setup.LoadProvingKey(p, setup.CircuitOutput)
	require.NoError(t, err)
	require.NotNil(t, pk)
}

func TestLoadVerifyingKey_OutputCircuit_Success(t *testing.T) {
	p := setupSharedPP(t)
	vk, err := setup.LoadVerifyingKey(p, setup.CircuitOutput)
	require.NoError(t, err)
	require.NotNil(t, vk)
}

func TestLoadProvingKey_MissingKey_Error(t *testing.T) {
	p := pp.DefaultPublicParams() // no keys set
	_, err := setup.LoadProvingKey(p, setup.CircuitSpend)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestLoadVerifyingKey_MissingKey_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	_, err := setup.LoadVerifyingKey(p, setup.CircuitOutput)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestLoadProvingKey_UnknownCircuit_Error(t *testing.T) {
	p := setupSharedPP(t)
	_, err := setup.LoadProvingKey(p, "nonexistent-circuit")
	require.Error(t, err)
}

func TestLoadVerifyingKey_UnknownCircuit_Error(t *testing.T) {
	p := setupSharedPP(t)
	_, err := setup.LoadVerifyingKey(p, "nonexistent-circuit")
	require.Error(t, err)
}

func TestLoadProvingKey_MigrationMissing_Error(t *testing.T) {
	// SetupAll does NOT populate migration keys.
	p := setupSharedPP(t)
	_, err := setup.LoadProvingKey(p, setup.CircuitMigration)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

func TestLoadVerifyingKey_MigrationMissing_Error(t *testing.T) {
	p := setupSharedPP(t)
	_, err := setup.LoadVerifyingKey(p, setup.CircuitMigration)
	require.ErrorIs(t, err, setup.ErrMissingKey)
}

// ── Key serialization round-trips ─────────────────────────────────────────────

func TestSerializeDeserializeVerifyingKey_RoundTrip(t *testing.T) {
	p := setupSharedPP(t)

	vk, err := setup.LoadVerifyingKey(p, setup.CircuitSpend)
	require.NoError(t, err)

	raw, err := setup.SerializeVerifyingKey(vk)
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	vk2, err := setup.DeserializeVerifyingKey(raw, ecc.BLS12_381)
	require.NoError(t, err)
	require.NotNil(t, vk2)
}

func TestSerializeDeserializeProvingKey_RoundTrip(t *testing.T) {
	p := setupSharedPP(t)

	pk, err := setup.LoadProvingKey(p, setup.CircuitSpend)
	require.NoError(t, err)

	raw, err := setup.SerializeProvingKey(pk)
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	pk2, err := setup.DeserializeProvingKey(raw, ecc.BLS12_381)
	require.NoError(t, err)
	require.NotNil(t, pk2)
}

// ── Proof serialization ────────────────────────────────────────────────────────

func TestDeserializeProof_BadBytes_Error(t *testing.T) {
	_, err := setup.DeserializeProof([]byte("garbage proof bytes"), ecc.BLS12_381)
	require.Error(t, err)
}

func TestDeserializeProof_EmptyBytes_Error(t *testing.T) {
	_, err := setup.DeserializeProof([]byte{}, ecc.BLS12_381)
	require.Error(t, err)
}

// ── SetupMigration ────────────────────────────────────────────────────────────

func TestSetupMigration_NonGroth16_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.ProofSystem = "plonk"
	_, err := setup.SetupMigration(p, dummyGens())
	require.ErrorIs(t, err, setup.ErrNotImplemented)
}

func TestSetupMigration_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p, err := setup.SetupMigration(p, dummyGens())
	require.NoError(t, err)
	require.NotEmpty(t, p.PKMigration)
	require.NotEmpty(t, p.VKMigration)
}

func TestSerializeProof_Success(t *testing.T) {
	proof := groth16.NewProof(ecc.BLS12_381)
	raw, err := setup.SerializeProof(proof)
	require.NoError(t, err)
	require.NotEmpty(t, raw)
}

func TestDeserializeProvingKey_BadBytes_Error(t *testing.T) {
	_, err := setup.DeserializeProvingKey([]byte("garbage"), ecc.BLS12_381)
	require.Error(t, err)
}

func TestDeserializeVerifyingKey_BadBytes_Error(t *testing.T) {
	_, err := setup.DeserializeVerifyingKey([]byte("garbage"), ecc.BLS12_381)
	require.Error(t, err)
}

func TestSetupAll_OutputCircuitError(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.MaxBits = 0
	_, err := setup.SetupAll(p)
	require.Error(t, err)
}
