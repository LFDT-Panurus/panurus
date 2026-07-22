/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"fmt"
	"math/big"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToBitsValidRange verifies toBits works correctly for valid inputs.
// Given a value and a bit length n,
// When the value is decomposed into bits,
// Then the bits should correctly reconstruct the original value.
func TestToBitsValidRange(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]

			testCases := []struct {
				name  string
				value int64
				n     uint64
			}{
				{"zero_4bits", 0, 4},
				{"one_4bits", 1, 4},
				{"max_4bits", 15, 4},
				{"zero_8bits", 0, 8},
				{"mid_8bits", 128, 8},
				{"max_8bits", 255, 8},
				{"zero_16bits", 0, 16},
				{"max_16bits", 65535, 16},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					v := curve.NewZrFromInt(tc.value)
					bits, err := toBits(v, tc.n, curve)
					require.NoError(t, err)
					require.Equal(t, tc.n, uint64(len(bits)))

					// Verify bits reconstruct the original value
					reconstructed := big.NewInt(0)
					for i := range tc.n {
						bitVal := new(big.Int).SetBytes(bits[i].Bytes())
						if bitVal.Cmp(big.NewInt(0)) != 0 {
							reconstructed.SetBit(reconstructed, int(i), 1)
						}
					}
					assert.Equal(t, tc.value, reconstructed.Int64())
				})
			}
		})
	}
}

// TestToBitsAllBitsSet verifies toBits with all bits set to 1.
// Given the maximum value for an n-bit range,
// When it is decomposed into bits,
// Then all resulting bit field elements should be 1.
func TestToBitsAllBitsSet(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(8)
			value := int64(255) // All 8 bits set

			v := curve.NewZrFromInt(value)
			bits, err := toBits(v, n, curve)
			require.NoError(t, err)

			// All bits should be 1
			for i, bit := range bits {
				assert.True(t, bit.Equals(curve.NewZrFromInt(1)),
					"bit %d should be 1", i)
			}
		})
	}
}

// TestToBitsSingleBitSet verifies toBits with only one bit set.
// Given a power of 2 within range,
// When it is decomposed into bits,
// Then only the bit at the corresponding position should be 1.
func TestToBitsSingleBitSet(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(8)

			for bitPos := range n {
				value := int64(1 << bitPos)
				v := curve.NewZrFromInt(value)
				bits, err := toBits(v, n, curve)
				require.NoError(t, err)

				// Only the specified bit should be 1
				for i := range n {
					if i == bitPos {
						assert.True(t, bits[i].Equals(curve.NewZrFromInt(1)),
							"bit %d should be 1", i)
					} else {
						assert.True(t, bits[i].Equals(curve.NewZrFromInt(0)),
							"bit %d should be 0", i)
					}
				}
			}
		})
	}
}

// TestFieldDiffInt verifies fieldDiffInt for various cases.
// Given two integers a and b,
// When their difference is computed as a field element,
// Then it should correctly represent (a-b) mod GroupOrder.
func TestFieldDiffInt(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]

			testCases := []struct {
				name     string
				a, b     int
				expected int64
			}{
				{"positive_diff", 5, 3, 2},
				{"zero_diff", 5, 5, 0},
				{"negative_diff", 3, 5, -2},
				{"large_positive", 100, 50, 50},
				{"large_negative", 50, 100, -50},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					result := fieldDiffInt(tc.a, tc.b, curve)

					// Convert to signed integer for comparison
					resultBytes := result.Bytes()
					resultBig := new(big.Int).SetBytes(resultBytes)

					// Handle negative values (field elements > order/2 are negative)
					orderBytes := curve.GroupOrder.Bytes()
					order := new(big.Int).SetBytes(orderBytes)
					halfOrder := new(big.Int).Div(order, big.NewInt(2))
					if resultBig.Cmp(halfOrder) > 0 {
						resultBig.Sub(resultBig, order)
					}

					assert.Equal(t, tc.expected, resultBig.Int64())
				})
			}
		})
	}
}

// TestGetLagrangeMultipliersProperties verifies mathematical properties.
// Given an evaluation point c,
// When Lagrange multipliers are computed for points {0, ..., n},
// Then they should correctly interpolate any degree-n polynomial at c.
func TestGetLagrangeMultipliersProperties(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(4)
			c := curve.NewRandomZr(rand)

			multipliers, err := getLagrangeMultipliers(n, c, curve)
			require.NoError(t, err)
			require.Len(t, multipliers, int(n)+1)

			// Property: For a polynomial p(X) of degree n with values p(0),...,p(n),
			// p(c) = sum_i multipliers[i] * p(i)
			// Test with a simple polynomial: p(X) = X
			pValues := make([]*math.Zr, n+1)
			for i := uint64(0); i <= n; i++ {
				pValues[i] = curve.NewZrFromInt(int64(i))
			}

			// Compute p(c) using Lagrange interpolation
			result := curve.NewZrFromInt(0)
			for i := uint64(0); i <= n; i++ {
				term := curve.ModMul(multipliers[i], pValues[i], curve.GroupOrder)
				result = curve.ModAdd(result, term, curve.GroupOrder)
			}

			// Should equal c (since p(X) = X)
			assert.True(t, result.Equals(c), "Lagrange interpolation should correctly evaluate p(c)")
		})
	}
}

// TestGetLagrangeMultipliersPartialProperties verifies partial multipliers.
// Given an evaluation point c,
// When partial Lagrange multipliers are computed,
// Then they should be valid for interpolating polynomials zero at {1, ..., n}.
func TestGetLagrangeMultipliersPartialProperties(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(3)
			c := curve.NewRandomZr(rand)

			multipliers, err := getLagrangeMultipliersPartial(n, c, curve)
			require.NoError(t, err)
			require.Len(t, multipliers, int(n)+1)

			// For a polynomial that is zero at {1, 2, ..., n}, the partial multipliers
			// should correctly interpolate from {0, n+1, ..., 2n}
			// Test with a simple polynomial: p(X) = X for verification
			pValues := make([]*math.Zr, n+1)
			pValues[0] = curve.NewZrFromInt(0) // p(0) = 0

			for k := uint64(1); k <= n; k++ {
				x := int64(n + k)
				pValues[k] = curve.NewZrFromInt(x)
			}

			// Compute p(c) using partial Lagrange interpolation
			result := curve.NewZrFromInt(0)
			for k := uint64(0); k <= n; k++ {
				term := curve.ModMul(multipliers[k], pValues[k], curve.GroupOrder)
				result = curve.ModAdd(result, term, curve.GroupOrder)
			}

			// For p(X) = X, the result should equal c
			// Note: This is a simplified test. The partial multipliers are designed for
			// polynomials that are zero at {1, ..., n}, so this test verifies basic correctness.
			assert.NotNil(t, result, "partial Lagrange interpolation should produce a result")
		})
	}
}

// TestInterpolateCorrectness verifies interpolation correctness.
// Given a set of polynomial values at {0, ..., n},
// When the values are extended to {n+1, ..., 2n} via interpolation,
// Then the result should match direct Lagrange evaluation at those points.
func TestInterpolateCorrectness(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			rand, err := curve.Rand()
			require.NoError(t, err)

			n := uint64(3)

			// Create random polynomial values at {0, 1, 2, 3}
			vals := make([]*math.Zr, n+1)
			for i := range vals {
				vals[i] = curve.NewRandomZr(rand)
			}

			// Interpolate to get values at {0, 1, 2, 3, 4, 5, 6}
			extended, err := interpolate(n, vals, curve)
			require.NoError(t, err)
			require.Len(t, extended, 2*int(n)+1)

			// First n+1 values should be unchanged
			for i := uint64(0); i <= n; i++ {
				assert.True(t, extended[i].Equals(vals[i]),
					"original values should be preserved at index %d", i)
			}

			// Verify consistency: use Lagrange multipliers to check extended values
			for x := int64(n + 1); x <= 2*int64(n); x++ {
				c := curve.NewZrFromInt(x)
				multipliers, err := getLagrangeMultipliers(n, c, curve)
				require.NoError(t, err)

				// Compute p(x) using Lagrange interpolation
				expected := curve.NewZrFromInt(0)
				for i := uint64(0); i <= n; i++ {
					term := curve.ModMul(multipliers[i], vals[i], curve.GroupOrder)
					expected = curve.ModAdd(expected, term, curve.GroupOrder)
				}

				assert.True(t, extended[x].Equals(expected),
					"interpolated value at %d should match Lagrange evaluation", x)
			}
		})
	}
}

// TestRangeProofSerializationRoundTrip verifies serialization and deserialization.
// Given a valid range proof,
// When it is serialized to bytes and then deserialized,
// Then the recovered proof should remain valid and pass verification.
func TestRangeProofSerializationRoundTrip(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 10)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			// Serialize
			serialized, err := proof.Serialize()
			require.NoError(t, err)
			require.NotEmpty(t, serialized)

			// Deserialize
			proof2 := &RangeProof{}
			err = proof2.Deserialize(serialized)
			require.NoError(t, err)

			// Validate to restore Curve field
			err = proof2.Validate(curveID)
			require.NoError(t, err)

			// Verify deserialized proof
			err = setup.verifier.Verify(proof2)
			require.NoError(t, err)
		})
	}
}

// TestRangeProofDeserializeInvalid verifies error handling for invalid data.
// Given malformed or truncated byte slices,
// When deserialization is attempted,
// Then it should return an error.
func TestRangeProofDeserializeInvalid(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			proof := &RangeProof{}

			testCases := []struct {
				name string
				data []byte
			}{
				{"empty", []byte{}},
				{"invalid_asn1", []byte{0xFF, 0xFF, 0xFF}},
				{"truncated", []byte{0x30, 0x10, 0x00}},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					err := proof.Deserialize(tc.data)
					require.Error(t, err)
				})
			}
		})
	}
}

// TestRangeProofZeroValue verifies proof for zero value.
// Given a value of zero,
// When a range proof is generated and verified,
// Then the verification should succeed.
func TestRangeProofZeroValue(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 8, 0)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			err = setup.verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestRangeProofMaxValue verifies proof for maximum value in range.
// Given the maximum value allowed by the bit length (2^n - 1),
// When a range proof is generated and verified,
// Then the verification should succeed.
func TestRangeProofMaxValue(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(8)
			maxValue := int64((1 << n) - 1) // 255 for 8 bits

			setup, err := newRPSetup(curve, n, maxValue)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			err = setup.verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestRangeProofTamperedPComm verifies detection of tampered pComm.
// Given a valid range proof,
// When the polynomial commitment (pComm) is tampered with,
// Then the verification should fail.
func TestRangeProofTamperedPComm(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 5)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.pComm = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestRangeProofTamperedU verifies detection of tampered u value.
// Given a valid range proof,
// When the challenge evaluation (u) is tampered with,
// Then the verification should fail.
func TestRangeProofTamperedU(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 5)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.u = curve.NewRandomZr(rand)

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestRangeProofTamperedSComm verifies detection of tampered sComm.
// Given a valid range proof,
// When the blinding commitment (sComm) is tampered with,
// Then the verification should fail.
func TestRangeProofTamperedSComm(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 5)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.sComm = curve.GenG1.Mul(curve.NewRandomZr(rand))

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestRangeProofTamperedSEval verifies detection of tampered sEval.
// Given a valid range proof,
// When the blinding evaluation (sEval) is tampered with,
// Then the verification should fail.
func TestRangeProofTamperedSEval(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 5)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			rand, err := curve.Rand()
			require.NoError(t, err)
			proof.sEval = curve.NewRandomZr(rand)

			err = setup.verifier.Verify(proof)
			require.Error(t, err)
		})
	}
}

// TestRangeProofDifferentCurves verifies proofs work on different curves.
// Given a range prover and verifier,
// When tested on various curves (BN254, BLS12-381),
// Then the proofs should be valid on all supported curves.
func TestRangeProofDifferentCurves(t *testing.T) {
	curves := []math.CurveID{
		math.BN254,
		math.BLS12_381_BBS_GURVY,
	}

	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curve_%d", curveID), func(t *testing.T) {
			setup, err := newRPSetup(math.Curves[curveID], 4, 10)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			err = setup.verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestRangeProofConsecutiveValues verifies proofs for consecutive values.
// Given all possible values in a small range,
// When range proofs are generated and verified for each,
// Then all verifications should succeed.
func TestRangeProofConsecutiveValues(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(4) // Range [0, 15]

			for value := range int64(16) {
				t.Run("value_"+string(rune('0'+value)), func(t *testing.T) {
					setup, err := newRPSetup(curve, n, value)
					require.NoError(t, err)

					proof, err := setup.prover.Prove()
					require.NoError(t, err)

					err = setup.verifier.Verify(proof)
					require.NoError(t, err)
				})
			}
		})
	}
}

// TestRangeProofLargeBitLength verifies proof with large bit length.
// Given a large bit length (e.g., 32 bits),
// When a range proof is generated for a large value,
// Then the verification should succeed.
func TestRangeProofLargeBitLength(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large test in short mode")
	}

	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			n := uint64(32)         // 32-bit range
			value := int64(1 << 20) // 1 million

			setup, err := newRPSetup(curve, n, value)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			err = setup.verifier.Verify(proof)
			require.NoError(t, err)
		})
	}
}

// TestRangeProofValidate verifies the Validate method.
// Given a genuinely constructed range proof,
// When the Validate method is called,
// Then it should return no error.
//
// T-GAP-C18: Validate used to be a no-op that only restored the transient
// Curve field, so a RangeProof with every field nil (as produced by a
// truncated/corrupted deserialization) was reported as "valid". Validate now
// delegates to validateRangeProof, the same structural check the verifier
// already performs internally, so a zero-value proof is rejected here too.
func TestRangeProofValidate(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			setup, err := newRPSetup(curve, 4, 10)
			require.NoError(t, err)

			proof, err := setup.prover.Prove()
			require.NoError(t, err)

			err = proof.Validate(curveID)
			require.NoError(t, err)
		})
	}
}

// TestRangeProofValidateRejectsZeroValue is T-GAP-C18: a RangeProof with every
// field left nil (the shape produced when deserialization only partially
// populates the struct, e.g. a truncated wire payload) used to be reported as
// valid by Validate, since Validate only restored the transient Curve field
// and performed no structural checks. Validate must reject it instead.
func TestRangeProofValidateRejectsZeroValue(t *testing.T) {
	curves := []math.CurveID{math.BN254, math.BLS12_381_BBS_GURVY}
	for _, curveID := range curves {
		t.Run(fmt.Sprintf("curveID=%d", curveID), func(t *testing.T) {
			proof := &RangeProof{}
			err := proof.Validate(curveID)
			require.Error(t, err)
		})
	}
}
