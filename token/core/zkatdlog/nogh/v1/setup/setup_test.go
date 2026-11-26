/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	math3 "github.com/IBM/mathlib"
	pp3 "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	utils2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/utils"
	ppproto "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driverpp "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
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
	require.NoError(t, pp.SetAuditors(newAuditors))
	assert.Equal(t, newAuditors, pp.Auditors())

	// zkatdlog supports a list of auditors, so an empty/nil slice clears them
	// without error (unlike fabtoken's single-auditor setter).
	require.NoError(t, pp.SetAuditors(nil))
	assert.Empty(t, pp.Auditors())

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

	// Test invalid driver identifier
	pp, err := NewWith(SetupParams{DriverName: "invalid", DriverVersion: ProtocolV1, BitLength: 32, IdemixIssuerPK: issuerPK, CurveID: math3.BN254, ProofType: rp.RangeProofType})
	require.NoError(t, err)
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
	err = recovered.FromProto(proto, curve)
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

func TestValidateRejectsNonDerivedGenerators(t *testing.T) {
	issuerPK := testingHelper(t)
	curve := math3.Curves[math3.BN254]

	newPP := func(t *testing.T, proofType rp.ProofType) *PublicParams {
		t.Helper()
		pp, err := NewWith(SetupParams{
			DriverName: DLogNoGHDriverName, DriverVersion: ProtocolV1, BitLength: 32,
			IdemixIssuerPK: issuerPK, CurveID: math3.BN254, ProofType: proofType,
			GeneratorDomain: "test.domain",
		})
		require.NoError(t, err)
		require.NoError(t, pp.Validate())

		return pp
	}
	rnd, err := curve.Rand()
	require.NoError(t, err)

	tests := []struct {
		name      string
		proofType rp.ProofType
		tamper    func(pp *PublicParams)
		wantErr   string
	}{
		{
			name:      "trapdoored pedersen g1 = g2^x",
			proofType: rp.RangeProofType,
			tamper: func(pp *PublicParams) {
				pp.PedersenGenerators[1] = pp.PedersenGenerators[2].Mul(curve.NewRandomZr(rnd))
			},
			wantErr: "invalid pedersen generators",
		},
		{
			name:      "pedersen generator replaced by generator point",
			proofType: rp.RangeProofType,
			tamper:    func(pp *PublicParams) { pp.PedersenGenerators[0] = curve.GenG1 },
			wantErr:   "invalid pedersen generators",
		},
		{
			name:      "range proof P trapdoored",
			proofType: rp.RangeProofType,
			tamper: func(pp *PublicParams) {
				pp.RangeProofParams.P = pp.RangeProofParams.Q.Mul(curve.NewRandomZr(rnd))
			},
			wantErr: "invalid range proof generators",
		},
		{
			name:      "range proof left generator trapdoored",
			proofType: rp.RangeProofType,
			tamper: func(pp *PublicParams) {
				pp.RangeProofParams.LeftGenerators[3] = pp.RangeProofParams.LeftGenerators[2].Mul(curve.NewRandomZr(rnd))
			},
			wantErr: "invalid range proof generators",
		},
		{
			name:      "csp right generator trapdoored",
			proofType: rp.CSPRangeProofType,
			tamper: func(pp *PublicParams) {
				pp.CSPRangeProofParams.RightGenerators[5] = pp.CSPRangeProofParams.RightGenerators[4].Mul(curve.NewRandomZr(rnd))
			},
			wantErr: "invalid csp range proof generators",
		},
		{
			name:      "range proof Q trapdoored",
			proofType: rp.RangeProofType,
			tamper: func(pp *PublicParams) {
				pp.RangeProofParams.Q = pp.RangeProofParams.P.Mul(curve.NewRandomZr(rnd))
			},
			wantErr: "invalid range proof generators",
		},
		{
			name:      "range proof right generator swapped",
			proofType: rp.RangeProofType,
			tamper: func(pp *PublicParams) {
				r := pp.RangeProofParams.RightGenerators
				r[0], r[1] = r[1], r[0]
			},
			wantErr: "invalid range proof generators",
		},
		{
			name:      "csp left generator trapdoored",
			proofType: rp.CSPRangeProofType,
			tamper: func(pp *PublicParams) {
				pp.CSPRangeProofParams.LeftGenerators[2] = pp.CSPRangeProofParams.LeftGenerators[1].Mul(curve.NewRandomZr(rnd))
			},
			wantErr: "invalid csp range proof generators",
		},
		{
			name:      "pedersen generator is nil",
			proofType: rp.RangeProofType,
			tamper:    func(pp *PublicParams) { pp.PedersenGenerators[1] = nil },
			wantErr:   "invalid pedersen generators",
		},
		{
			name:      "generator domain set without regenerating",
			proofType: rp.RangeProofType,
			tamper:    func(pp *PublicParams) { pp.GeneratorDomain = "rotated" },
			wantErr:   "invalid pedersen generators",
		},
		{
			name:      "generator domain too long",
			proofType: rp.RangeProofType,
			tamper:    func(pp *PublicParams) { pp.GeneratorDomain = strings.Repeat("a", MaxGeneratorDomainLength+1) },
			wantErr:   "invalid generator domain",
		},
		{
			name:      "generator domain not printable",
			proofType: rp.RangeProofType,
			tamper:    func(pp *PublicParams) { pp.GeneratorDomain = "a\x00b" },
			wantErr:   "invalid generator domain",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pp := newPP(t, tc.proofType)
			tc.tamper(pp)
			err := pp.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestDefaultGeneratorsArePinned pins the default generators to the v1 derivation strings.
// Changing them would make every token issued so far unusable.
func TestDefaultGeneratorsArePinned(t *testing.T) {
	curve := math3.Curves[math3.BN254]
	pp, err := WithVersionAndProofType(32, testingHelper(t), math3.BN254, ProtocolV1, rp.RangeProofType)
	require.NoError(t, err)
	csp, err := WithVersionAndProofType(32, testingHelper(t), math3.BN254, ProtocolV1, rp.CSPRangeProofType)
	require.NoError(t, err)

	for i, g := range pp.PedersenGenerators {
		assert.True(t, g.Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.PedersenGenerators."+strconv.Itoa(i)))), "pedersen %d", i)
	}
	assert.True(t, pp.RangeProofParams.P.Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.RangeProof.P"))))
	assert.True(t, pp.RangeProofParams.Q.Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.RangeProof.Q"))))
	for i := range pp.RangeProofParams.LeftGenerators {
		assert.True(t, pp.RangeProofParams.LeftGenerators[i].Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.RangeProof.L."+strconv.Itoa(i)))), "rp L %d", i)
		assert.True(t, pp.RangeProofParams.RightGenerators[i].Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.RangeProof.R."+strconv.Itoa(i)))), "rp R %d", i)
	}
	for i := range csp.CSPRangeProofParams.LeftGenerators {
		assert.True(t, csp.CSPRangeProofParams.LeftGenerators[i].Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.CSPRangeProof.L."+strconv.Itoa(i)))), "csp L %d", i)
		assert.True(t, csp.CSPRangeProofParams.RightGenerators[i].Equals(curve.HashToG1([]byte("lfdt-panurus.zkatdlognogh.1.CSPRangeProof.R."+strconv.Itoa(i)))), "csp R %d", i)
	}
}

// TestDriverVersionBumpKeepsGenerators checks that the generators do not depend on the driver version.
func TestDriverVersionBumpKeepsGenerators(t *testing.T) {
	v1, err := WithVersion(32, testingHelper(t), math3.BN254, ProtocolV1)
	require.NoError(t, err)
	v2, err := WithVersion(32, testingHelper(t), math3.BN254, driver.TokenDriverVersion(2))
	require.NoError(t, err)
	require.NoError(t, v2.Validate())

	require.Len(t, v2.PedersenGenerators, len(v1.PedersenGenerators))
	for i := range v1.PedersenGenerators {
		assert.True(t, v1.PedersenGenerators[i].Equals(v2.PedersenGenerators[i]))
	}
	assert.True(t, v1.RangeProofParams.P.Equals(v2.RangeProofParams.P))

	// v1 generators relabelled as v2 stay valid
	v1.DriverVersion = 2
	require.NoError(t, v1.Validate())
}

func TestGeneratorDomain(t *testing.T) {
	issuerPK := testingHelper(t)
	def, err := Setup(32, issuerPK, math3.BN254)
	require.NoError(t, err)

	for _, proofType := range []rp.ProofType{rp.RangeProofType, rp.CSPRangeProofType} {
		pp, err := NewWith(SetupParams{
			DriverName:      DLogNoGHDriverName,
			DriverVersion:   ProtocolV1,
			BitLength:       32,
			IdemixIssuerPK:  issuerPK,
			CurveID:         math3.BN254,
			ProofType:       proofType,
			GeneratorDomain: "acme.second-generation",
		})
		require.NoError(t, err)
		require.NoError(t, pp.Validate())
		for i := range pp.PedersenGenerators {
			assert.False(t, pp.PedersenGenerators[i].Equals(def.PedersenGenerators[i]), "pedersen %d not rotated", i)
		}

		// the domain survives serialization and the result still validates
		raw, err := pp.Serialize()
		require.NoError(t, err)
		back, err := NewPublicParamsFromBytes(raw, DLogNoGHDriverName, ProtocolV1)
		require.NoError(t, err)
		assert.Equal(t, "acme.second-generation", back.GeneratorDomain)
		require.NoError(t, back.Validate())

		// changing the domain without regenerating is rejected
		back.GeneratorDomain = "other"
		require.Error(t, back.Validate())
	}

	// a domain that spells the default label still yields different generators
	pp, err := NewWith(SetupParams{
		DriverName: DLogNoGHDriverName, DriverVersion: ProtocolV1, BitLength: 32,
		IdemixIssuerPK: issuerPK, CurveID: math3.BN254, ProofType: rp.RangeProofType,
		GeneratorDomain: "lfdt-panurus.zkatdlognogh.1.",
	})
	require.NoError(t, err)
	assert.False(t, pp.PedersenGenerators[0].Equals(def.PedersenGenerators[0]))

	for _, bad := range []string{strings.Repeat("a", MaxGeneratorDomainLength+1), "tab\there", "caf\xc3\xa9"} {
		_, err := NewWith(SetupParams{
			DriverName: DLogNoGHDriverName, DriverVersion: ProtocolV1, BitLength: 32,
			IdemixIssuerPK: issuerPK, CurveID: math3.BN254, ProofType: rp.RangeProofType,
			GeneratorDomain: bad,
		})
		require.Error(t, err, "domain %q", bad)
		assert.Contains(t, err.Error(), "invalid generator domain")
	}
}

// TestValidateAcceptsGeneratorsWithoutDomain checks that, without a GeneratorDomain, the generators
// are accepted as they are: choosing them is the issuer's responsibility.
func TestValidateAcceptsGeneratorsWithoutDomain(t *testing.T) {
	curve := math3.Curves[math3.BN254]
	rnd, err := curve.Rand()
	require.NoError(t, err)

	pp, err := Setup(32, testingHelper(t), math3.BN254)
	require.NoError(t, err)
	// random Pedersen generators and range proof generators from plain strings, as in releases up to v0.13.0
	for i := range pp.PedersenGenerators {
		pp.PedersenGenerators[i] = curve.GenG1.Mul(curve.NewRandomZr(rnd))
	}
	pp.RangeProofParams.P = curve.HashToG1([]byte("0"))
	pp.RangeProofParams.Q = curve.HashToG1([]byte("1"))
	for i := range pp.RangeProofParams.LeftGenerators {
		pp.RangeProofParams.LeftGenerators[i] = curve.HashToG1([]byte("RangeProof." + strconv.Itoa(2*(i+1))))
		pp.RangeProofParams.RightGenerators[i] = curve.HashToG1([]byte("RangeProof." + strconv.Itoa(2*(i+1)+1)))
	}
	require.NoError(t, pp.Validate())

	// the same parameters declare a domain: now the generators must derive from it
	pp.GeneratorDomain = "declared"
	require.Error(t, pp.Validate())
}

// TestLegacyGeneratorsRoundTrip checks that parameters with legacy non-derived generators and no
// GeneratorDomain survive Serialize -> NewPublicParamsFromBytes -> Validate, and that no domain is emitted.
func TestLegacyGeneratorsRoundTrip(t *testing.T) {
	curve := math3.Curves[math3.BN254]
	rnd, err := curve.Rand()
	require.NoError(t, err)

	pp, err := Setup(32, testingHelper(t), math3.BN254)
	require.NoError(t, err)
	for i := range pp.PedersenGenerators {
		pp.PedersenGenerators[i] = curve.GenG1.Mul(curve.NewRandomZr(rnd))
	}
	pp.RangeProofParams.P = curve.HashToG1([]byte("0"))
	pp.RangeProofParams.Q = curve.HashToG1([]byte("1"))
	for i := range pp.RangeProofParams.LeftGenerators {
		pp.RangeProofParams.LeftGenerators[i] = curve.HashToG1([]byte("RangeProof." + strconv.Itoa(2*(i+1))))
		pp.RangeProofParams.RightGenerators[i] = curve.HashToG1([]byte("RangeProof." + strconv.Itoa(2*(i+1)+1)))
	}
	require.Empty(t, pp.GeneratorDomain)

	raw, err := pp.Serialize()
	require.NoError(t, err)

	container, err := pp3.Unmarshal(raw)
	require.NoError(t, err)
	inner := &ppproto.PublicParameters{}
	require.NoError(t, proto.Unmarshal(container.Raw, inner))
	assert.Empty(t, inner.GeneratorDomain)
	// field 13 (generator_domain) must be absent from the wire bytes
	for b := container.Raw; len(b) > 0; {
		num, typ, n := protowire.ConsumeTag(b)
		require.GreaterOrEqual(t, n, 0)
		assert.NotEqual(t, protowire.Number(13), num)
		m := protowire.ConsumeFieldValue(num, typ, b[n:])
		require.GreaterOrEqual(t, m, 0)
		b = b[n+m:]
	}

	pp2, err := NewPublicParamsFromBytes(raw, DLogNoGHDriverName, ProtocolV1)
	require.NoError(t, err)
	require.NoError(t, pp2.Validate())
	assert.Empty(t, pp2.GeneratorDomain)
}

// TestValidateRejectsMalformedGenerators checks that structural errors are rejected also without a GeneratorDomain.
func TestValidateRejectsMalformedGenerators(t *testing.T) {
	tests := map[string]func(pp *PublicParams){
		"short pedersen":     func(pp *PublicParams) { pp.PedersenGenerators = pp.PedersenGenerators[:2] },
		"nil pedersen":       func(pp *PublicParams) { pp.PedersenGenerators[0] = nil },
		"short left":         func(pp *PublicParams) { pp.RangeProofParams.LeftGenerators = pp.RangeProofParams.LeftGenerators[:31] },
		"nil right":          func(pp *PublicParams) { pp.RangeProofParams.RightGenerators[7] = nil },
		"nil P":              func(pp *PublicParams) { pp.RangeProofParams.P = nil },
		"wrong rounds":       func(pp *PublicParams) { pp.RangeProofParams.NumberOfRounds++ },
		"unsupported length": func(pp *PublicParams) { pp.RangeProofParams.BitLength = 1 << 40 },
	}
	for name, tamper := range tests {
		t.Run(name, func(t *testing.T) {
			pp, err := Setup(32, testingHelper(t), math3.BN254)
			require.NoError(t, err)
			tamper(pp)
			require.NotPanics(t, func() { require.Error(t, pp.Validate()) })
		})
	}
	t.Run("csp nil", func(t *testing.T) {
		pp, err := WithVersionAndProofType(32, testingHelper(t), math3.BN254, ProtocolV1, rp.CSPRangeProofType)
		require.NoError(t, err)
		pp.CSPRangeProofParams.LeftGenerators[3] = nil
		require.NotPanics(t, func() { require.Error(t, pp.Validate()) })
	})
}

// reencodePP decodes serialized public parameters, applies mutate to the proto, and re-encodes them.
func reencodePP(tb testing.TB, raw []byte, mutate func(p *ppproto.PublicParameters)) []byte {
	tb.Helper()
	container, err := pp3.Unmarshal(raw)
	require.NoError(tb, err)
	p := &ppproto.PublicParameters{}
	require.NoError(tb, proto.Unmarshal(container.Raw, p))
	mutate(p)
	inner, err := proto.Marshal(p)
	require.NoError(tb, err)
	out, err := pp3.Marshal(&driverpp.PublicParameters{Identifier: container.Identifier, Raw: inner})
	require.NoError(tb, err)

	return out
}

// TestPublicParamsDeserializeRejectsGeneratorsOnOtherCurve checks that generators on a curve other
// than the declared one are rejected at decode time.
func TestPublicParamsDeserializeRejectsGeneratorsOnOtherCurve(t *testing.T) {
	foreign, err := utils2.ToProtoG1(math3.Curves[math3.BLS12_381_BBS].GenG1)
	require.NoError(t, err)
	cases := map[string]struct {
		proofType rp.ProofType
		mutate    func(p *ppproto.PublicParameters)
	}{
		"pedersen generator":     {rp.RangeProofType, func(p *ppproto.PublicParameters) { p.PedersenGenerators[0] = foreign }},
		"range proof P":          {rp.RangeProofType, func(p *ppproto.PublicParameters) { p.RangeProofParams.P = foreign }},
		"range proof Q":          {rp.RangeProofType, func(p *ppproto.PublicParameters) { p.RangeProofParams.Q = foreign }},
		"range proof left":       {rp.RangeProofType, func(p *ppproto.PublicParameters) { p.RangeProofParams.LeftGenerators[0] = foreign }},
		"range proof right":      {rp.RangeProofType, func(p *ppproto.PublicParameters) { p.RangeProofParams.RightGenerators[0] = foreign }},
		"csp left generator":     {rp.CSPRangeProofType, func(p *ppproto.PublicParameters) { p.CspRangeProofParams.LeftGenerators[0] = foreign }},
		"csp right generator":    {rp.CSPRangeProofType, func(p *ppproto.PublicParameters) { p.CspRangeProofParams.RightGenerators[0] = foreign }},
		"declared curve swapped": {rp.RangeProofType, func(p *ppproto.PublicParameters) { p.CurveId.Id = uint64(math3.FP256BN_AMCL) }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			params, err := NewWith(SetupParams{
				DriverName:     DLogNoGHDriverName,
				DriverVersion:  ProtocolV1,
				BitLength:      32,
				CurveID:        math3.BN254,
				IdemixIssuerPK: testingHelper(t),
				ProofType:      tc.proofType,
			})
			require.NoError(t, err)
			raw, err := params.Serialize()
			require.NoError(t, err)

			_, err = NewPublicParamsFromBytes(reencodePP(t, raw, tc.mutate), DLogNoGHDriverName, ProtocolV1)
			require.ErrorContains(t, err, "invalid G1: expected curve")
		})
	}
}
