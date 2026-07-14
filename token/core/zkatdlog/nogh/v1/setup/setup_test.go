/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	math3 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testingHelper is a test helper for creating test data
func testingHelper(t *testing.T) []byte {
	t.Helper()
	issuerPK, err := os.ReadFile("testdata/idemix/msp/IssuerPublicKey")
	require.NoError(t, err)
	require.NotEmpty(t, issuerPK)

	return issuerPK
}

func TestSerialization(t *testing.T) {
	for _, curve := range []math3.CurveID{math3.BN254, math3.BLS12_381_GURVY} {
		t.Run("extras is not empty", func(t *testing.T) {
			issuerPK := testingHelper(t)
			pp, err := Setup(32, issuerPK, curve)
			require.NoError(t, err)
			assert.NotNil(t, pp.Extras())
			require.NoError(t, pp.Validate())
			ser, err := pp.Serialize()
			require.NoError(t, err)
			pp2, err := NewPublicParamsFromBytes(ser, DLogNoGHDriverName, ProtocolV1)
			require.NoError(t, err)
			assert.NotNil(t, pp2.Extras())
			require.NoError(t, pp2.Validate())
		})

		t.Run("valid setup", func(t *testing.T) {
			// Use test helper instead of direct file read
			issuerPK := testingHelper(t)
			pp, err := Setup(32, issuerPK, curve)
			require.NoError(t, err)
			assert.NotNil(t, pp.Extras())
			pp.ExtraData = map[string][]byte{
				"key1": []byte("value1"),
				"key2": []byte("value2"),
			}
			require.NoError(t, err)
			ser, err := pp.Serialize()
			require.NoError(t, err)

			pp2, err := NewPublicParamsFromBytes(ser, DLogNoGHDriverName, ProtocolV1)
			require.NoError(t, err)
			assert.Equal(t, pp, pp2)
			assert.NotNil(t, pp2.Extras())
			require.NoError(t, pp.Validate())

			_, err = pp2.Serialize()
			require.NoError(t, err)

			// no issuers
			require.NoError(t, pp.Validate())

			// with issuers
			pp.IssuerIDs = []driver.Identity{[]byte("issuer")}
			require.NoError(t, pp.Validate())
		})

		t.Run("valid setup with CSPRangeProofType", func(t *testing.T) {
			// Use test helper instead of direct file read
			issuerPK := testingHelper(t)
			pp, err := NewWith(SetupParams{
				DriverName:     DLogNoGHDriverName,
				DriverVersion:  ProtocolV1,
				BitLength:      32,
				IdemixIssuerPK: issuerPK,
				CurveID:        curve,
				ProofType:      rp.CSPRangeProofType,
			})
			require.NoError(t, err)
			assert.NotNil(t, pp.Extras())
			pp.ExtraData = map[string][]byte{
				"key1": []byte("value1"),
				"key2": []byte("value2"),
			}
			require.NoError(t, err)
			header := pp.CSPRangeProofParams.RPTranscriptHeader
			assert.NotEmpty(t, header)

			ser, err := pp.Serialize()
			require.NoError(t, err)

			pp2, err := NewPublicParamsFromBytes(ser, DLogNoGHDriverName, ProtocolV1)
			require.NoError(t, err)
			require.NoError(t, pp2.Validate())
			assert.Equal(t, pp, pp2)
			assert.NotNil(t, pp2.Extras())
			header2 := pp2.CSPRangeProofParams.RPTranscriptHeader
			assert.NotEmpty(t, header)
			assert.Equal(t, header, header2)

			_, err = pp2.Serialize()
			require.NoError(t, err)

			// no issuers
			require.NoError(t, pp.Validate())

			// with issuers
			pp.IssuerIDs = []driver.Identity{[]byte("issuer")}
			require.NoError(t, pp.Validate())
		})
	}
}

func TestComputeMaxTokenValue(t *testing.T) {
	pp := PublicParams{
		RangeProofParams: &RangeProofParams{
			BitLength: 64,
		},
	}
	max := pp.ComputeMaxTokenValue()
	assert.Equal(t, uint64(18446744073709551615), max)

	pp = PublicParams{
		RangeProofParams: &RangeProofParams{
			BitLength: 16,
		},
	}
	max = pp.ComputeMaxTokenValue()
	assert.Equal(t, uint64(65535), max)
}

func TestNewG1(t *testing.T) {
	for i := range len(math3.Curves) {
		c := math3.Curves[i]
		assert.True(t, c.NewG1().IsInfinity())
	}
}

func TestRangeProofParamsValidation(t *testing.T) {
	curve := math3.BN254
	curveInst := math3.Curves[curve]

	// Create generators of the correct length
	makeGenerators := func(length uint64) []*math3.G1 {
		gen := make([]*math3.G1, length)
		for i := range gen {
			gen[i] = curveInst.GenG1
		}

		return gen
	}

	tests := []struct {
		name          string
		params        *RangeProofParams
		expectedError string
	}{
		{
			name: "valid params",
			params: &RangeProofParams{
				BitLength:       16,
				NumberOfRounds:  4,
				P:               curveInst.GenG1,
				Q:               curveInst.GenG1,
				LeftGenerators:  makeGenerators(16),
				RightGenerators: makeGenerators(16),
			},
			expectedError: "",
		},
		{
			name: "zero bit length",
			params: &RangeProofParams{
				BitLength:      0,
				NumberOfRounds: 4,
				P:              curveInst.GenG1,
				Q:              curveInst.GenG1,
			},
			expectedError: "invalid range proof parameters: bit length is zero",
		},
		{
			name: "zero rounds",
			params: &RangeProofParams{
				BitLength:      16,
				NumberOfRounds: 0,
				P:              curveInst.GenG1,
				Q:              curveInst.GenG1,
			},
			expectedError: "invalid range proof parameters: number of rounds is zero",
		},
		{
			name: "too many rounds",
			params: &RangeProofParams{
				BitLength:      16,
				NumberOfRounds: 65,
				P:              curveInst.GenG1,
				Q:              curveInst.GenG1,
			},
			expectedError: "invalid range proof parameters: number of rounds must be smaller or equal to 64",
		},
		{
			name: "mismatched generator lengths",
			params: &RangeProofParams{
				BitLength:       16,
				NumberOfRounds:  4,
				P:               curveInst.GenG1,
				Q:               curveInst.GenG1,
				LeftGenerators:  makeGenerators(15),
				RightGenerators: makeGenerators(16),
			},
			expectedError: "invalid range proof parameters: the size of the left generators does not match the size of the right generators",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate(curve)
			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestIdemixIssuerPublicKeyConversion(t *testing.T) {
	original := &IdemixIssuerPublicKey{
		PublicKey: []byte("test-key"),
		Curve:     math3.BN254,
	}

	proto, err := original.ToProtos()
	require.NoError(t, err)

	recovered := &IdemixIssuerPublicKey{}
	err = recovered.FromProtos(proto)
	require.NoError(t, err)

	assert.Equal(t, original.PublicKey, recovered.PublicKey)
	assert.Equal(t, original.Curve, recovered.Curve)
}

func TestSetupWithInvalidParameters(t *testing.T) {
	tests := []struct {
		name          string
		bitLength     uint64
		expectedError string
	}{
		{
			name:          "bit length too large",
			bitLength:     65,
			expectedError: "invalid bit length [65], should be smaller than 64",
		},
		{
			name:          "zero bit length",
			bitLength:     0,
			expectedError: "invalid bit length, should be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Setup(tt.bitLength, []byte("test-key"), math3.BN254)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestPublicParamsModification(t *testing.T) {
	pp, err := Setup(32, testingHelper(t), math3.BN254)
	require.NoError(t, err)

	// Test AddAuditor
	auditor1 := driver.Identity("auditor1")
	auditor2 := driver.Identity("auditor2")
	pp.AddAuditor(auditor1)
	pp.AddAuditor(auditor2)
	assert.Equal(t, []driver.Identity{auditor1, auditor2}, pp.Auditors())

	// Test SetAuditors
	newAuditors := []driver.Identity{driver.Identity("newAuditor")}
	pp.SetAuditors(newAuditors)
	assert.Equal(t, newAuditors, pp.Auditors())

	// Test AddIssuer
	issuer1 := driver.Identity("issuer1")
	issuer2 := driver.Identity("issuer2")
	pp.AddIssuer(issuer1)
	pp.AddIssuer(issuer2)
	assert.Equal(t, []driver.Identity{issuer1, issuer2}, pp.Issuers())

	// Test SetIssuers
	newIssuers := []driver.Identity{driver.Identity("newIssuer")}
	pp.SetIssuers(newIssuers)
	assert.Equal(t, newIssuers, pp.Issuers())
}

func TestPublicParamsValidation(t *testing.T) {
	validIssuerPK := &IdemixIssuerPublicKey{
		PublicKey: []byte("valid-key"),
		Curve:     math3.BN254,
	}

	tests := []struct {
		name          string
		setupParams   func() *PublicParams
		expectedError string
	}{
		{
			name: "valid params",
			setupParams: func() *PublicParams {
				issuerPK := testingHelper(t)
				pp, err := Setup(32, issuerPK, math3.BN254)
				require.NoError(t, err)

				return pp
			},
			expectedError: "",
		},
		{
			name: "invalid curve ID",
			setupParams: func() *PublicParams {
				pp := &PublicParams{
					Curve:                  math3.CurveID(999), // Invalid curve ID
					IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{validIssuerPK},
				}

				return pp
			},
			expectedError: "invalid public parameters: invalid curveID",
		},
		{
			name: "no idemix issuer public keys",
			setupParams: func() *PublicParams {
				pp := &PublicParams{
					Curve:                  math3.BN254,
					IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{},
				}

				return pp
			},
			expectedError: "expected at least one idemix issuer public key",
		},
		{
			name: "nil idemix issuer public key",
			setupParams: func() *PublicParams {
				pp := &PublicParams{
					Curve:                  math3.BN254,
					IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{nil},
				}

				return pp
			},
			expectedError: "invalid idemix issuer public key, it is nil",
		},
		{
			name: "empty idemix issuer public key",
			setupParams: func() *PublicParams {
				pp := &PublicParams{
					Curve: math3.BN254,
					IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{
						{
							PublicKey: []byte{},
							Curve:     math3.BN254,
						},
					},
				}

				return pp
			},
			expectedError: "expected idemix issuer public key to be non-empty",
		},
		{
			name: "invalid idemix curve ID",
			setupParams: func() *PublicParams {
				issuerPK := testingHelper(t)
				pp, err := Setup(32, issuerPK, math3.BN254)
				require.NoError(t, err)
				pp.Curve = math3.CurveID(999) // Invalid curve ID

				return pp
			},
			expectedError: "invalid public parameters: invalid curveID [999 > 8]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := tt.setupParams()
			err := pp.Validate()
			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestComputeHash(t *testing.T) {
	pp, err := Setup(32, testingHelper(t), math3.BN254)
	require.NoError(t, err)

	hash1, err := pp.ComputeHash()
	require.NoError(t, err)
	assert.NotEmpty(t, hash1)

	// Modify parameters and check hash changes
	pp.AddIssuer(driver.Identity("newIssuer"))
	hash2, err := pp.ComputeHash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash2)
}

func TestWithVersion(t *testing.T) {
	version := driver.TokenDriverVersion(2)
	pp, err := WithVersion(32, testingHelper(t), math3.BN254, version)
	require.NoError(t, err)

	assert.Equal(t, version, pp.TokenDriverVersion())
	assert.Equal(t, driver.TokenDriverName(DLogNoGHDriverName), pp.TokenDriverName())
}

func TestSerializationEdgeCases(t *testing.T) {
	issuerPK := testingHelper(t)
	pp, err := Setup(32, issuerPK, math3.BN254)
	require.NoError(t, err)

	// Test invalid driver identifier
	pp.DriverName = "invalid"
	raw, err := pp.Serialize()
	require.NoError(t, err)
	_, err = NewPublicParamsFromBytes(raw, DLogNoGHDriverName, ProtocolV1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid identifier")

	// Test corrupted data
	_, err = NewPublicParamsFromBytes([]byte("corrupted"), DLogNoGHDriverName, ProtocolV1)
	require.Error(t, err)

	// Test deserialize with invalid proto
	_, err = NewPublicParamsFromBytes([]byte{0, 1, 2, 3}, DLogNoGHDriverName, ProtocolV1)
	require.Error(t, err)
}

func TestPublicParamsString(t *testing.T) {
	pp := &PublicParams{
		DriverName:    DLogNoGHDriverName,
		DriverVersion: ProtocolV1,
		Curve:         math3.BN254,
		IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{
			{
				PublicKey: []byte("test-key"),
				Curve:     math3.BN254,
			},
		},
		AuditorIDs: []driver.Identity{[]byte("auditor1")},
		IssuerIDs:  []driver.Identity{[]byte("issuer1")},
		ExtraData: map[string][]byte{
			"key": []byte("value"),
		},
	}

	str := pp.String()
	res, err := json.MarshalIndent(pp, " ", "  ")
	require.NoError(t, err)
	assert.Equal(t, string(res), str)
}

func TestRangeProofParamsGenerators(t *testing.T) {
	curve := math3.BN254
	curveInst := math3.Curves[curve]

	// Test validation with nil generators
	params := &RangeProofParams{
		BitLength:      16,
		NumberOfRounds: 4,
		P:              curveInst.GenG1,
		Q:              curveInst.GenG1,
	}
	err := params.Validate(curve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range proof parameters")

	// Test validation with nil P
	params = &RangeProofParams{
		BitLength:       16,
		NumberOfRounds:  4,
		Q:               curveInst.GenG1,
		LeftGenerators:  make([]*math3.G1, 16),
		RightGenerators: make([]*math3.G1, 16),
	}
	err = params.Validate(curve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range proof parameters")

	// Test validation with nil Q
	params = &RangeProofParams{
		BitLength:       16,
		NumberOfRounds:  4,
		P:               curveInst.GenG1,
		LeftGenerators:  make([]*math3.G1, 16),
		RightGenerators: make([]*math3.G1, 16),
	}
	err = params.Validate(curve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range proof parameters")
}

func TestRangeProofParamsConversion(t *testing.T) {
	curve := math3.BN254
	curveInst := math3.Curves[curve]

	original := &RangeProofParams{
		BitLength:       16,
		NumberOfRounds:  4,
		P:               curveInst.GenG1,
		Q:               curveInst.GenG1,
		LeftGenerators:  []*math3.G1{curveInst.GenG1},
		RightGenerators: []*math3.G1{curveInst.GenG1},
	}

	proto, err := original.ToProtos()
	require.NoError(t, err)

	recovered := &RangeProofParams{}
	err = recovered.FromProto(proto)
	require.NoError(t, err)

	assert.Equal(t, original.BitLength, recovered.BitLength)
	assert.Equal(t, original.NumberOfRounds, recovered.NumberOfRounds)
	assert.Len(t, recovered.LeftGenerators, len(original.LeftGenerators))
	assert.Len(t, recovered.RightGenerators, len(original.RightGenerators))
}

func TestGeneratePedersenParameters(t *testing.T) {
	pp := &PublicParams{
		Curve:         math3.BN254,
		DriverName:    DLogNoGHDriverName,
		DriverVersion: ProtocolV1,
	}

	err := pp.GeneratePedersenParameters()
	require.NoError(t, err)
	assert.Len(t, pp.PedersenGenerators, 3)

	// Generators must be distinct (different domain-separation strings → different points)
	for i := range len(pp.PedersenGenerators) {
		for j := i + 1; j < len(pp.PedersenGenerators); j++ {
			assert.False(t, pp.PedersenGenerators[i].Equals(pp.PedersenGenerators[j]))
		}
	}

	// Generators must be deterministic: a second call with identical params must
	// produce byte-equal points, proving no randomness is involved.
	pp2 := &PublicParams{Curve: math3.BN254, DriverName: DLogNoGHDriverName, DriverVersion: ProtocolV1}
	require.NoError(t, pp2.GeneratePedersenParameters())
	for i := range len(pp.PedersenGenerators) {
		assert.True(t, pp.PedersenGenerators[i].Equals(pp2.PedersenGenerators[i]),
			"generator %d must be deterministic (nothing-up-my-sleeve)", i)
	}
}

func TestGenerateRangeProofParameters(t *testing.T) {
	pp := &PublicParams{
		Curve: math3.BN254,
	}

	err := pp.GenerateRangeProofParameters(16)
	require.NoError(t, err)

	assert.NotNil(t, pp.RangeProofParams)
	assert.Equal(t, uint64(16), pp.RangeProofParams.BitLength)
	assert.Equal(t, uint64(4), pp.RangeProofParams.NumberOfRounds) // log2(16)
	assert.Len(t, pp.RangeProofParams.LeftGenerators, 16)
	assert.Len(t, pp.RangeProofParams.RightGenerators, 16)
	assert.NotNil(t, pp.RangeProofParams.P)
	assert.NotNil(t, pp.RangeProofParams.Q)

	// Test that P and Q are different
	assert.False(t, pp.RangeProofParams.P.Equals(pp.RangeProofParams.Q))

	// Test all generators are different
	for i, left := range pp.RangeProofParams.LeftGenerators {
		for j, right := range pp.RangeProofParams.RightGenerators {
			assert.False(t, left.Equals(right), "Left generator %d equals right generator %d", i, j)
		}
	}
}

func TestFailedSerialization(t *testing.T) {
	pp := &PublicParams{
		Curve:              math3.CurveID(999), // Invalid curve
		PedersenGenerators: []*math3.G1{nil},   // Invalid generator
	}

	_, err := pp.Serialize()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to serialize public parameters")
}

func TestLog2(t *testing.T) {
	tests := []struct {
		input    uint64
		expected uint64
	}{
		{1, 0},
		{2, 1},
		{4, 2},
		{8, 3},
		{16, 4},
		{32, 5},
		{64, 6},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("log2(%d)", tt.input), func(t *testing.T) {
			result := log2(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRangeProofParamsValidationWithMismatchedBitLength(t *testing.T) {
	curve := math3.BN254
	curveInst := math3.Curves[curve]

	// Test when BitLength doesn't match the expected value based on NumberOfRounds
	params := &RangeProofParams{
		BitLength:       32, // This should be 16 for NumberOfRounds=4
		NumberOfRounds:  4,
		P:               curveInst.GenG1,
		Q:               curveInst.GenG1,
		LeftGenerators:  make([]*math3.G1, 32),
		RightGenerators: make([]*math3.G1, 32),
	}
	err := params.Validate(curve)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range proof parameters: bit length should be")
}

func TestRangeProofParamsInvalidPoints(t *testing.T) {
	curve := math3.BN254
	curveInst := math3.Curves[curve]

	// Create a point from another curve
	invalidPoint := math3.Curves[math3.BLS12_381_BBS_GURVY].GenG1

	issuerPK := testingHelper(t)
	pp, err := Setup(32, issuerPK, math3.BN254)
	require.NoError(t, err)

	tests := []struct {
		name          string
		params        *RangeProofParams
		expectedError string
	}{
		{
			name: "invalid P point",
			params: &RangeProofParams{
				BitLength:      pp.RangeProofParams.BitLength,
				NumberOfRounds: pp.RangeProofParams.NumberOfRounds,
				P:              invalidPoint,
				Q:              pp.RangeProofParams.Q,
			},
			expectedError: "invalid range proof parameters: generator P is invalid",
		},
		{
			name: "invalid Q point",
			params: &RangeProofParams{
				BitLength:      pp.RangeProofParams.BitLength,
				NumberOfRounds: pp.RangeProofParams.NumberOfRounds,
				P:              pp.RangeProofParams.P,
				Q:              invalidPoint,
			},
			expectedError: "invalid range proof parameters: generator Q is invalid",
		},
		{
			name: "invalid left generator",
			params: &RangeProofParams{
				BitLength:      pp.RangeProofParams.BitLength,
				NumberOfRounds: pp.RangeProofParams.NumberOfRounds,
				P:              pp.RangeProofParams.P,
				Q:              pp.RangeProofParams.Q,
				LeftGenerators: []*math3.G1{
					curveInst.GenG1,
					pp.RangeProofParams.LeftGenerators[1],
				},
				RightGenerators: make([]*math3.G1, 2),
			},
			expectedError: "invalid range proof parameters, left generators is invalid",
		},
		{
			name: "invalid right generator",
			params: &RangeProofParams{
				BitLength:       pp.RangeProofParams.BitLength,
				NumberOfRounds:  pp.RangeProofParams.NumberOfRounds,
				P:               curveInst.GenG1,
				Q:               curveInst.GenG1,
				LeftGenerators:  pp.RangeProofParams.LeftGenerators,
				RightGenerators: make([]*math3.G1, len(pp.RangeProofParams.RightGenerators)),
			},
			expectedError: "invalid range proof parameters, right generators is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate(curve)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestExtras(t *testing.T) {
	pp := &PublicParams{
		ExtraData: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	extras := pp.Extras()
	assert.Equal(t, pp.ExtraData, extras)

	// Verify nil case
	pp = &PublicParams{}
	assert.Nil(t, pp.Extras())

	// Verify empty map case
	pp.ExtraData = make(map[string][]byte)
	assert.Empty(t, pp.Extras())
}

// TestCSPPublicParamsValidation exercises PublicParams.Validate for CSP-only
// configurations (CSPRangeProofParams set, RangeProofParams nil).
//
// Regression: line 733 previously read p.RangeProofParams.BitLength inside the
// `if p.CSPRangeProofParams != nil` branch; in a CSP-only config that is a
// guaranteed nil-pointer dereference.  Every case below that reaches the
// QuantityPrecision mismatch path must return an error, never panic.
func TestCSPPublicParamsValidation(t *testing.T) {
	issuerPK := testingHelper(t)

	// cspPP builds a fully-valid CSP-only PublicParams for BN254 with the
	// given bitLength, then lets the caller mutate it.
	cspPP := func(t *testing.T, bitLength uint64) *PublicParams {
		t.Helper()
		pp, err := NewWith(SetupParams{
			DriverName:     DLogNoGHDriverName,
			DriverVersion:  ProtocolV1,
			BitLength:      bitLength,
			IdemixIssuerPK: issuerPK,
			CurveID:        math3.BN254,
			ProofType:      rp.CSPRangeProofType,
		})
		require.NoError(t, err)
		require.Nil(t, pp.RangeProofParams, "CSP-only setup must leave RangeProofParams nil")
		require.NotNil(t, pp.CSPRangeProofParams)

		return pp
	}

	tests := []struct {
		name          string
		setupParams   func() *PublicParams
		expectedError string
	}{
		{
			// Baseline: a well-formed CSP-only config must pass Validate without
			// panicking and without any error.
			name: "valid CSP-only params",
			setupParams: func() *PublicParams {
				return cspPP(t, 32)
			},
			expectedError: "",
		},
		{
			// Regression test for the nil-dereference bug (CVE-equivalent):
			// CSPRangeProofParams set, RangeProofParams nil,
			// QuantityPrecision deliberately mismatched to reach line 733.
			// Must return an error string, never panic.
			name: "CSP-only: QuantityPrecision mismatch must not panic (regression)",
			setupParams: func() *PublicParams {
				pp := cspPP(t, 32)
				pp.QuantityPrecision = 16 // mismatch: CSPRangeProofParams.BitLength is 32

				return pp
			},
			expectedError: "quantity precision should be [32] instead it is [16]",
		},
		{
			// The error message must reference the CSP bit-length (32), not a
			// value that would only be available from the nil RangeProofParams.
			name: "CSP-only: error message references CSP bit length",
			setupParams: func() *PublicParams {
				pp := cspPP(t, 64)
				pp.QuantityPrecision = 32 // mismatch; CSP bit-length is 64

				return pp
			},
			expectedError: "quantity precision should be [64] instead it is [32]",
		},
		{
			// CSPRangeProofParams.BitLength not in SupportedPrecisions triggers
			// the earlier unsupported-precision guard, also inside the CSP branch.
			name: "CSP-only: unsupported bit length",
			setupParams: func() *PublicParams {
				pp := cspPP(t, 32)
				// Overwrite BitLength with a value not in SupportedPrecisions.
				pp.CSPRangeProofParams.BitLength = 7

				return pp
			},
			expectedError: "invalid bit length [7]",
		},
		{
			// Both RangeProofParams and CSPRangeProofParams nil must be rejected
			// before reaching either branch.
			name: "both range proof params nil",
			setupParams: func() *PublicParams {
				pp := cspPP(t, 32)
				pp.CSPRangeProofParams = nil

				return pp
			},
			expectedError: "nil range proof parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pp := tt.setupParams()
			// Call Validate via a helper that catches panics so a regression
			// surfaces as a test failure rather than a process crash.
			err := func() (retErr error) {
				defer func() {
					if r := recover(); r != nil {
						retErr = fmt.Errorf("Validate panicked: %v", r)
					}
				}()

				return pp.Validate()
			}()

			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

// TestCSPRangeProofParamsValidation exercises CSPRangeProofParams.Validate
// directly, mirroring the coverage that TestRangeProofParamsValidation
// provides for RangeProofParams.
func TestCSPRangeProofParamsValidation(t *testing.T) {
	curve := math3.BN254
	curveInst := math3.Curves[curve]

	makeGenerators := func(length uint64) []*math3.G1 {
		gen := make([]*math3.G1, length)
		for i := range gen {
			gen[i] = curveInst.GenG1
		}

		return gen
	}

	const bitLength uint64 = 32

	tests := []struct {
		name          string
		params        *CSPRangeProofParams
		expectedError string
	}{
		{
			name: "valid params",
			params: &CSPRangeProofParams{
				BitLength:       bitLength,
				LeftGenerators:  makeGenerators(bitLength + 1),
				RightGenerators: makeGenerators(bitLength + 1),
			},
			expectedError: "",
		},
		{
			name: "zero bit length",
			params: &CSPRangeProofParams{
				BitLength:       0,
				LeftGenerators:  makeGenerators(1),
				RightGenerators: makeGenerators(1),
			},
			expectedError: "invalid range proof parameters: bit length is zero",
		},
		{
			name: "mismatched generator lengths",
			params: &CSPRangeProofParams{
				BitLength:       bitLength,
				LeftGenerators:  makeGenerators(bitLength),
				RightGenerators: makeGenerators(bitLength + 1),
			},
			expectedError: "the size of the left generators does not match the size of the right generators",
		},
		{
			name: "wrong number of left generators",
			params: &CSPRangeProofParams{
				BitLength:       bitLength,
				LeftGenerators:  makeGenerators(bitLength), // needs bitLength+1
				RightGenerators: makeGenerators(bitLength),
			},
			expectedError: "invalid range proof parameters, left generators is invalid",
		},
		{
			// Both slices have the same length so the size-mismatch check passes,
			// but RightGenerators has bitLength entries (one short) — which causes
			// the CheckElements call for the right generators to fail.
			name: "wrong number of right generators",
			params: &CSPRangeProofParams{
				BitLength: bitLength,
				// Left has the correct bitLength+1 entries.
				LeftGenerators: makeGenerators(bitLength + 1),
				// Right also has bitLength+1 entries, but the last one is nil so
				// the curve element check rejects it.
				RightGenerators: func() []*math3.G1 {
					gen := makeGenerators(bitLength + 1)
					gen[bitLength] = nil // inject an invalid element

					return gen
				}(),
			},
			expectedError: "invalid range proof parameters, right generators is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate(curve)
			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}
