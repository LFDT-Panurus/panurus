/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package pp_test

import (
	"bytes"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	bls12381tw "github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
)

// defaultValidPP returns a valid, fully-initialised PublicParams that passes
// Validate() without running a real trusted setup (no circuit keys required).
func defaultValidPP() *pp.PublicParams {
	return pp.DefaultPublicParams()
}

func TestPublicParams_TokenDriverName(t *testing.T) {
	p := defaultValidPP()
	require.Equal(t, driver.TokenDriverName("zkatsnark"), p.TokenDriverName())
}

func TestPublicParams_TokenDriverVersion(t *testing.T) {
	p := defaultValidPP()
	require.Equal(t, driver.TokenDriverVersion(1), p.TokenDriverVersion())
}

func TestPublicParams_TokenDataHiding_AlwaysTrue(t *testing.T) {
	p := defaultValidPP()
	require.True(t, p.TokenDataHiding(), "zkatsnark always hides token data by construction")
}

func TestPublicParams_GraphHiding_ReflectsField(t *testing.T) {
	p := defaultValidPP()
	p.GraphHidingEnabled = false
	require.False(t, p.GraphHiding())

	p.GraphHidingEnabled = true
	require.True(t, p.GraphHiding())
}

func TestPublicParams_MaxTokenValue_MaxBits32(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = 32
	require.Equal(t, uint64((1<<32)-1), p.MaxTokenValue())
}

func TestPublicParams_MaxTokenValue_MaxBits1(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = 1
	require.Equal(t, uint64(1), p.MaxTokenValue())
}

func TestPublicParams_MaxTokenValue_MaxBits64_FallsBackToMax(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = 64 // degenerate: falls back to ^uint64(0)
	require.Equal(t, ^uint64(0), p.MaxTokenValue())
}

func TestPublicParams_MaxTokenValue_MaxBitsZero_FallsBackToMax(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = 0
	require.Equal(t, ^uint64(0), p.MaxTokenValue())
}

func TestPublicParams_MaxTokenValue_NegativeMaxBits_FallsBackToMax(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = -1
	require.Equal(t, ^uint64(0), p.MaxTokenValue())
}

func TestPublicParams_CertificationDriver(t *testing.T) {
	p := defaultValidPP()
	p.CertifierID = "some-certifier"
	require.Equal(t, "some-certifier", p.CertificationDriver())
}

func TestPublicParams_Auditors(t *testing.T) {
	p := defaultValidPP()
	p.AuditorIdentities = []driver.Identity{[]byte("auditor1"), []byte("auditor2")}
	require.Len(t, p.Auditors(), 2)
}

func TestPublicParams_Issuers(t *testing.T) {
	p := defaultValidPP()
	p.IssuerIdentities = []driver.Identity{[]byte("issuer1")}
	require.Len(t, p.Issuers(), 1)
}

func TestPublicParams_Precision(t *testing.T) {
	p := defaultValidPP()
	p.PrecisionValue = 8
	require.Equal(t, uint64(8), p.Precision())
}

func TestPublicParams_Extras(t *testing.T) {
	p := defaultValidPP()
	require.Nil(t, p.Extras()) // DefaultPublicParams does not set extras
}

// ── Mutation helpers ──────────────────────────────────────────────────────────

func TestPublicParams_AddIssuer(t *testing.T) {
	p := defaultValidPP()
	initial := len(p.Issuers())
	p.AddIssuer([]byte("new-issuer"))
	require.Len(t, p.Issuers(), initial+1)
	require.Equal(t, driver.Identity([]byte("new-issuer")), p.Issuers()[initial])
}

func TestPublicParams_AddAuditor(t *testing.T) {
	p := defaultValidPP()
	p.AddAuditor([]byte("new-auditor"))
	require.Len(t, p.Auditors(), 1)
	require.Equal(t, driver.Identity([]byte("new-auditor")), p.Auditors()[0])
}

func TestPublicParams_SetIssuers(t *testing.T) {
	p := defaultValidPP()
	p.AddIssuer([]byte("old"))
	newList := []driver.Identity{[]byte("a"), []byte("b")}
	p.SetIssuers(newList)
	require.Equal(t, newList, p.Issuers())
}

func TestPublicParams_SetAuditors(t *testing.T) {
	p := defaultValidPP()
	p.AddAuditor([]byte("old"))
	newList := []driver.Identity{[]byte("x")}
	p.SetAuditors(newList)
	require.Equal(t, newList, p.Auditors())
}

func TestPublicParams_String_NonEmpty(t *testing.T) {
	s := defaultValidPP().String()
	require.NotEmpty(t, s)
	require.Contains(t, s, "zkatsnark")
}

func TestPublicParams_Validate_DefaultParams_OK(t *testing.T) {
	require.NoError(t, defaultValidPP().Validate())
}

func TestPublicParams_Validate_EmptyLabel(t *testing.T) {
	p := defaultValidPP()
	p.Label = ""
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_ZeroSchemeVersion(t *testing.T) {
	p := defaultValidPP()
	p.SchemeVersion = 0
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_BadProofSystem(t *testing.T) {
	p := defaultValidPP()
	p.ProofSystem = "badvalue"
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_GrothAndPlonkBothAccepted(t *testing.T) {
	p := defaultValidPP()
	p.ProofSystem = "groth16"
	require.NoError(t, p.Validate())
	p.ProofSystem = "plonk"
	require.NoError(t, p.Validate())
}

func TestPublicParams_Validate_UnsupportedCurve(t *testing.T) {
	p := defaultValidPP()
	p.Curve = ecc.BN254 // BN254 is actually accepted, so use something else
	require.NoError(t, p.Validate()) // BN254 is valid per code

	p.Curve = ecc.BLS12_377 // not in the allowed set
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_ZeroMaxBits(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = 0
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_NegativeMaxBits(t *testing.T) {
	p := defaultValidPP()
	p.MaxBits = -5
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_VNotOnCurve(t *testing.T) {
	p := defaultValidPP()
	// Set V to origin (0,0) which is not a valid Jubjub point
	p.V = bls12381tw.PointAffine{}
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_RNotOnCurve(t *testing.T) {
	p := defaultValidPP()
	p.R = bls12381tw.PointAffine{}
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_EmptyIdemixKeys(t *testing.T) {
	p := defaultValidPP()
	p.IdemixIssuerPublicKeys = nil
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_NilIdemixKeyInSlice(t *testing.T) {
	p := defaultValidPP()
	p.IdemixIssuerPublicKeys = []*pp.IdemixIssuerPublicKey{nil}
	require.Error(t, p.Validate())
}

func TestPublicParams_Validate_EmptyIdemixPublicKey(t *testing.T) {
	p := defaultValidPP()
	p.IdemixIssuerPublicKeys = []*pp.IdemixIssuerPublicKey{
		{PublicKey: nil, Curve: mathlib.BN254},
	}
	require.Error(t, p.Validate())
}

// ── Serialize / Deserialize round-trip ────────────────────────────────────────

func TestPublicParams_SerializeDeserialize_RoundTrip(t *testing.T) {
	orig := defaultValidPP()

	raw, err := orig.Serialize()
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	got, err := pp.DeserializePublicParams(raw)
	require.NoError(t, err)

	// Spot-check key fields rather than require.Equal on the whole struct
	// (circuit keys are empty in DefaultPublicParams, so the comparison is safe).
	require.Equal(t, orig.Label, got.Label)
	require.Equal(t, orig.SchemeVersion, got.SchemeVersion)
	require.Equal(t, orig.ProofSystem, got.ProofSystem)
	require.Equal(t, orig.MaxBits, got.MaxBits)
	require.Equal(t, orig.PrecisionValue, got.PrecisionValue)
}

func TestPublicParams_DeserializePublicParams_GarbageBytes_Error(t *testing.T) {
	_, err := pp.DeserializePublicParams([]byte("this is not valid protobuf"))
	require.Error(t, err)
}

func TestPublicParams_DeserializePublicParams_EmptyBytes_Error(t *testing.T) {
	_, err := pp.DeserializePublicParams([]byte{})
	require.Error(t, err)
}

// ── Hash ──────────────────────────────────────────────────────────────────────

func TestPublicParams_Hash_Deterministic(t *testing.T) {
	p := defaultValidPP()
	h1, err := p.Hash()
	require.NoError(t, err)
	h2, err := p.Hash()
	require.NoError(t, err)
	require.Equal(t, h1, h2, "Hash must be deterministic for identical params")
}

func TestPublicParams_Hash_ChangesWithMutation(t *testing.T) {
	p1 := defaultValidPP()
	p2 := defaultValidPP()
	p2.Label = "zkatsnark-mutated"

	h1, err := p1.Hash()
	require.NoError(t, err)
	h2, err := p2.Hash()
	require.NoError(t, err)

	require.False(t, bytes.Equal(h1[:], h2[:]), "Hash must differ after mutating Label")
}
