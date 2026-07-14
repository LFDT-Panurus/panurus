/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bulletproof_test

import (
	"context"
	"math/bits"
	"math/rand"
	"strconv"
	"testing"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/math"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/hyperledger-labs/fabric-smart-client/node/start/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ipaSetup struct {
	left      []*math.Zr
	right     []*math.Zr
	Q         *math.G1
	leftGens  []*math.G1
	rightGens []*math.G1
	curve     *math.Curve
	com       *math.G1
	nr        uint64
}

func newIpaSetup(curveID math.CurveID) (*ipaSetup, error) {
	curve := math.Curves[curveID]
	l := uint64(64)
	nr := 63 - uint64(bits.LeadingZeros64(l)) // #nosec G115
	leftGens := make([]*math.G1, l)
	rightGens := make([]*math.G1, l)
	left := make([]*math.Zr, l)
	right := make([]*math.Zr, l)
	rand, err := curve.Rand()
	if err != nil {
		return nil, err
	}
	com := curve.NewG1()
	Q := curve.GenG1

	for i := range left {
		leftGens[i] = curve.HashToG1([]byte(strconv.Itoa(i)))
		rightGens[i] = curve.HashToG1([]byte(strconv.Itoa(i + 1)))
		left[i] = curve.NewRandomZr(rand)
		right[i] = curve.NewRandomZr(rand)
		com.Add(leftGens[i].Mul(left[i]))
		com.Add(rightGens[i].Mul(right[i]))
	}

	return &ipaSetup{
		left:      left,
		right:     right,
		Q:         Q,
		leftGens:  leftGens,
		rightGens: rightGens,
		curve:     curve,
		com:       com,
		nr:        nr,
	}, nil
}

func TestIPAProofVerify(t *testing.T) {
	setup, err := newIpaSetup(math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	prover := bulletproof.NewIPAProver(
		math2.InnerProduct(setup.left, setup.right, setup.curve),
		setup.left,
		setup.right,
		setup.Q,
		setup.leftGens,
		setup.rightGens,
		setup.com,
		setup.nr,
		setup.curve,
		nil,
	)
	proof, err := prover.Prove()
	require.NoError(t, err)
	assert.NotNil(t, proof)

	verifier := bulletproof.NewIPAVerifier(
		math2.InnerProduct(setup.left, setup.right, setup.curve),
		setup.Q,
		setup.leftGens,
		setup.rightGens,
		setup.com,
		setup.nr,
		setup.curve,
		nil,
	)
	err = verifier.Verify(proof)
	require.NoError(t, err)
}

func BenchmarkIPAProver(b *testing.B) {
	pp, err := profile.New(profile.WithAll(), profile.WithPath("./profile"))
	require.NoError(b, err)
	require.NoError(b, pp.Start())
	defer pp.Stop()
	envs := make([]*ipaSetup, 0, 128)
	for range 128 {
		setup, err := newIpaSetup(math.BLS12_381_BBS_GURVY)
		require.NoError(b, err)
		envs = append(envs, setup)
	}

	b.Run("bench", func(b *testing.B) {
		for b.Loop() {
			setup := envs[rand.Intn(len(envs))]
			prover := bulletproof.NewIPAProver(
				math2.InnerProduct(setup.left, setup.right, setup.curve),
				setup.left,
				setup.right,
				setup.Q,
				setup.leftGens,
				setup.rightGens,
				setup.com,
				setup.nr,
				setup.curve,
				nil,
			)
			proof, err := prover.Prove()
			require.NoError(b, err)
			assert.NotNil(b, proof)
		}
	})
}

func TestParallelIPAProver(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*ipaSetup](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*ipaSetup, error) {
			return newIpaSetup(c.CurveID)
		},
		func(ctx context.Context, setup *ipaSetup) error {
			prover := bulletproof.NewIPAProver(
				math2.InnerProduct(setup.left, setup.right, setup.curve),
				setup.left,
				setup.right,
				setup.Q,
				setup.leftGens,
				setup.rightGens,
				setup.com,
				setup.nr,
				setup.curve,
				nil,
			)
			_, err := prover.Prove()

			return err
		},
	)
}

// TestComputeSVector_SingleElement verifies that n=1 (zero rounds, one challenge)
// produces s = [1] and sInv = [1].
// With a single element there are zero bits to select from, so the product
// is empty — the identity element 1.
func TestComputeSVector_SingleElement(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]

	// n=1 means 2^0 = 1, so we need 0 challenges.
	challenges := []*math.Zr{}
	s, sInv := bulletproof.ComputeSVector(1, challenges, curve)

	require.Len(t, s, 1, "s should have exactly one element")
	require.Len(t, sInv, 1, "sInv should have exactly one element")

	one := math2.One(curve)
	assert.True(t, s[0].Equals(one), "s[0] should be 1 for n=1")
	assert.True(t, sInv[0].Equals(one), "sInv[0] should be 1 for n=1")

	// Also verify the inverse property holds: sInv[0] * s[0] == 1
	product := curve.ModMul(s[0], sInv[0], curve.GroupOrder)
	assert.True(t, product.Equals(one), "s[0]*sInv[0] should be 1")
}

// TestComputeSVector_InverseProperty checks that sInv[i] * s[i] == 1
// for all i across various vector sizes.
func TestComputeSVector_InverseProperty(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	rand, err := curve.Rand()
	require.NoError(t, err)

	one := math2.One(curve)

	for _, rounds := range []int{1, 2, 3, 4, 5, 6} {
		n := 1 << rounds
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			challenges := make([]*math.Zr, rounds)
			for j := range challenges {
				challenges[j] = curve.NewRandomZr(rand)
			}

			s, sInv := bulletproof.ComputeSVector(n, challenges, curve)
			require.Len(t, s, n)
			require.Len(t, sInv, n)

			for i := range n {
				product := curve.ModMul(s[i], sInv[i], curve.GroupOrder)
				assert.True(t, product.Equals(one),
					"s[%d]*sInv[%d] should be 1 (got %s)", i, i, product)
			}
		})
	}
}

// TestComputeSVector_PanicOnBadN verifies that ComputeSVector panics when
// n != 2^(len(challenges)), which indicates a programming error.
func TestComputeSVector_PanicOnBadN(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	rand, err := curve.Rand()
	require.NoError(t, err)

	challenges := []*math.Zr{curve.NewRandomZr(rand), curve.NewRandomZr(rand)}

	// 2 challenges ⇒ n must be 4, but we pass 3 and 5
	for _, badN := range []int{3, 5, 6, 7} {
		t.Run(strconv.Itoa(badN), func(t *testing.T) {
			assert.Panics(t, func() {
				bulletproof.ComputeSVector(badN, challenges, curve)
			}, "ComputeSVector should panic for n=%d with 2 challenges", badN)
		})
	}
}

// TestComputeSVector_DefinitionConsistency verifies each entry of the s vector
// against its mathematical definition:
//
//	s[i] = ∏_{r=0}^{k-1} (bit(i, k-1-r) == 1 ? x_r : x_r^{-1})
//
// This is an independent, naive O(n·log n) computation used purely
// as a test oracle.
func TestComputeSVector_DefinitionConsistency(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	rand, err := curve.Rand()
	require.NoError(t, err)

	for _, rounds := range []int{1, 2, 3, 4, 5} {
		n := 1 << rounds
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			challenges := make([]*math.Zr, rounds)
			challengeInvs := make([]*math.Zr, rounds)
			for j := range challenges {
				challenges[j] = curve.NewRandomZr(rand)
				challengeInvs[j] = challenges[j].Copy()
				challengeInvs[j].InvModOrder()
			}

			s, sInv := bulletproof.ComputeSVector(n, challenges, curve)

			// Check each entry against the definition.
			for i := range n {
				expected := math2.One(curve)
				expectedInv := math2.One(curve)
				for r := range rounds {
					bitPos := rounds - 1 - r
					if (i>>bitPos)&1 == 1 {
						expected = curve.ModMul(expected, challenges[r], curve.GroupOrder)
						expectedInv = curve.ModMul(expectedInv, challengeInvs[r], curve.GroupOrder)
					} else {
						expected = curve.ModMul(expected, challengeInvs[r], curve.GroupOrder)
						expectedInv = curve.ModMul(expectedInv, challenges[r], curve.GroupOrder)
					}
				}
				assert.True(t, s[i].Equals(expected),
					"s[%d] mismatch for n=%d", i, n)
				assert.True(t, sInv[i].Equals(expectedInv),
					"sInv[%d] mismatch for n=%d", i, n)
			}
		})
	}
}

// TestComputeSVector_ConsistencyWithFoldReduction verifies that the s-vector
// based generator reduction yields the same result as the iterative fold-based
// reduceGenerators approach.
//
// For a set of generators G = {G_0, …, G_{n-1}} and challenge list
// x = {x_0, …, x_{k-1}}, the final reduced generator should be:
//
//	G' = ∑ s[i] · G_i
//
// This must equal the result of iteratively halving the generators
// using the fold recurrence:
//
//	G_i' = G_i · x^{-1} + G_{i+n/2} · x   (for each round)
func TestComputeSVector_ConsistencyWithFoldReduction(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	rand, err := curve.Rand()
	require.NoError(t, err)

	for _, rounds := range []int{1, 2, 3, 4} {
		n := 1 << rounds
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			// Generate random generators and challenges.
			generators := make([]*math.G1, n)
			for i := range generators {
				generators[i] = curve.HashToG1([]byte(strconv.Itoa(i * 17)))
			}
			challenges := make([]*math.Zr, rounds)
			for j := range challenges {
				challenges[j] = curve.NewRandomZr(rand)
			}

			// Method 1: MSM with s-vector
			s, _ := bulletproof.ComputeSVector(n, challenges, curve)
			resultMSM := curve.MultiScalarMul(generators, s)

			// Method 2: iterative fold reduction
			gens := make([]*math.G1, n)
			for i := range gens {
				gens[i] = generators[i].Copy()
			}
			for r := range rounds {
				half := len(gens) / 2
				x := challenges[r]
				xInv := x.Copy()
				xInv.InvModOrder()
				folded := make([]*math.G1, half)
				for i := range half {
					// G_i' = G_i · xInv + G_{i+half} · x
					folded[i] = gens[i].Mul2(xInv, gens[i+half], x)
				}
				gens = folded
			}
			require.Len(t, gens, 1, "fold should reduce to a single generator")
			resultFold := gens[0]

			assert.True(t, resultMSM.Equals(resultFold),
				"MSM(s, G) must equal the iteratively folded generator for n=%d", n)
		})
	}
}

// TestCloneGenerators_EmptySlices verifies CloneGenerators works correctly
// when given empty slices as input, returning empty (non-nil) slices.
func TestCloneGenerators_EmptySlices(t *testing.T) {
	leftGen, rightGen := bulletproof.CloneGenerators([]*math.G1{}, []*math.G1{})

	require.NotNil(t, leftGen, "left generators should not be nil")
	require.NotNil(t, rightGen, "right generators should not be nil")
	assert.Empty(t, leftGen, "left generators should be empty")
	assert.Empty(t, rightGen, "right generators should be empty")
}

// TestCloneGenerators_Independence verifies that cloned generators are
// independent copies: mutating a clone does not affect the original.
func TestCloneGenerators_Independence(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]

	original := []*math.G1{
		curve.HashToG1([]byte("g0")),
		curve.HashToG1([]byte("g1")),
	}
	originalCopy := []*math.G1{
		original[0].Copy(),
		original[1].Copy(),
	}

	left, _ := bulletproof.CloneGenerators(original, []*math.G1{})

	// Mutate the cloned generators
	rand, err := curve.Rand()
	require.NoError(t, err)
	scalar := curve.NewRandomZr(rand)
	left[0] = left[0].Mul(scalar)

	// Originals should be unchanged
	assert.True(t, original[0].Equals(originalCopy[0]),
		"mutating cloned generator should not affect original")
}

// TestCloneGenerators_NilSlices verifies CloneGenerators works with nil slices,
// returning empty (non-nil) slices.
func TestCloneGenerators_NilSlices(t *testing.T) {
	leftGen, rightGen := bulletproof.CloneGenerators(nil, nil)

	require.NotNil(t, leftGen, "left generators should not be nil")
	require.NotNil(t, rightGen, "right generators should not be nil")
	assert.Empty(t, leftGen)
	assert.Empty(t, rightGen)
}

// TestComputeSVector_KnownValues verifies ComputeSVector against a manually
// computed example with a single challenge.
//
// For k=1, challenge = [x]:
//
//	s[0] = x^{-1},  s[1] = x
//	sInv[0] = x,    sInv[1] = x^{-1}
func TestComputeSVector_KnownValues(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	rand, err := curve.Rand()
	require.NoError(t, err)

	x := curve.NewRandomZr(rand)
	xInv := x.Copy()
	xInv.InvModOrder()

	s, sInv := bulletproof.ComputeSVector(2, []*math.Zr{x}, curve)

	require.Len(t, s, 2)
	require.Len(t, sInv, 2)

	// s[0] = x^{-1} (bit 0 for the single challenge → inverse)
	assert.True(t, s[0].Equals(xInv), "s[0] should be x^{-1}")
	// s[1] = x (bit 1 for the single challenge → challenge)
	assert.True(t, s[1].Equals(x), "s[1] should be x")
	// sInv is entry-wise inverse of s
	assert.True(t, sInv[0].Equals(x), "sInv[0] should be x")
	assert.True(t, sInv[1].Equals(xInv), "sInv[1] should be x^{-1}")
}

// TestComputeSVector_DeterministicOutput verifies that calling ComputeSVector
// twice with the same inputs produces identical results.
func TestComputeSVector_DeterministicOutput(t *testing.T) {
	curve := math.Curves[math.BLS12_381_BBS_GURVY]
	rand, err := curve.Rand()
	require.NoError(t, err)

	rounds := 4
	n := 1 << rounds
	challenges := make([]*math.Zr, rounds)
	for j := range challenges {
		challenges[j] = curve.NewRandomZr(rand)
	}

	s1, sInv1 := bulletproof.ComputeSVector(n, challenges, curve)
	s2, sInv2 := bulletproof.ComputeSVector(n, challenges, curve)

	for i := range n {
		assert.True(t, s1[i].Equals(s2[i]), "s[%d] should be deterministic", i)
		assert.True(t, sInv1[i].Equals(sInv2[i]), "sInv[%d] should be deterministic", i)
	}
}
