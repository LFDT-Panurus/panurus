/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package edwards_test

import (
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// suite exercises the generic API through a curve-independent surface.
func testCurve[F any, PF edwards.Field[F], P any](t *testing.T, c *edwards.Curve[F, PF, P]) {
	t.Helper()

	t.Run("generators are distinct subgroup points", func(t *testing.T) {
		id := c.Identity()
		for _, p := range []P{c.G, c.V, c.R} {
			assert.False(t, c.Equal(p, id))
			assert.True(t, c.Equal(c.ScalarMul(p, c.Order()), id))
		}
		assert.False(t, c.Equal(c.V, c.R))
		assert.False(t, c.Equal(c.V, c.G))
	})

	t.Run("commitment is homomorphic", func(t *testing.T) {
		r1, err := c.RandomScalar()
		require.NoError(t, err)
		r2, err := c.RandomScalar()
		require.NoError(t, err)
		var rsum F
		PF(&rsum).Add(&r1, &r2)

		c1, err := c.ValueCommit(7, r1)
		require.NoError(t, err)
		c2, err := c.ValueCommit(35, r2)
		require.NoError(t, err)
		sum, err := c.ValueCommit(42, rsum)
		require.NoError(t, err)

		assert.True(t, c.Equal(sum, c.Add(c1, c2)))
		assert.True(t, c.Equal(sum, c.ValueCommitSum([]P{c1, c2})))
		assert.True(t, c.Equal(c.Add(c1, c.NegatePoint(c1)), c.Identity()))
		assert.True(t, c.Equal(c.ValueCommitSum(nil), c.Identity()))
	})

	t.Run("schnorr sign verify", func(t *testing.T) {
		sk, err := c.RandomScalar()
		require.NoError(t, err)
		for _, base := range []P{c.G, c.R} {
			pk := c.DerivePublicKey(sk, base)
			sig, err := c.Sign(sk, []byte("msg"), base)
			require.NoError(t, err)
			require.NoError(t, c.Verify(pk, []byte("msg"), sig, base))
			assert.ErrorIs(t, c.Verify(pk, []byte("other"), sig, base), edwards.ErrInvalidSignature)
		}
	})

	t.Run("signature serialization", func(t *testing.T) {
		sk, err := c.RandomScalar()
		require.NoError(t, err)
		sig, err := c.Sign(sk, []byte("msg"), c.G)
		require.NoError(t, err)

		raw, err := c.SerializeSignature(sig)
		require.NoError(t, err)
		require.Len(t, raw, c.SignatureSize())
		got, err := c.DeserializeSignature(raw)
		require.NoError(t, err)
		require.NoError(t, c.Verify(c.DerivePublicKey(sk, c.G), []byte("msg"), got, c.G))

		_, err = c.DeserializeSignature(raw[:len(raw)-1])
		require.Error(t, err)
		_, err = c.DeserializeSignature(nil)
		require.Error(t, err)

		bad := append([]byte(nil), raw...)
		for i := range c.ElementSize() {
			bad[i] = 0xff // R.X >= modulus
		}
		_, err = c.DeserializeSignature(bad)
		require.Error(t, err)
	})
	t.Run("schnorr nonces are unique", func(t *testing.T) {
		sk, err := c.RandomScalar()
		require.NoError(t, err)
		s1, err := c.Sign(sk, []byte("msg"), c.G)
		require.NoError(t, err)
		s2, err := c.Sign(sk, []byte("msg"), c.G)
		require.NoError(t, err)
		assert.False(t, c.Equal(s1.R, s2.R))
	})

	t.Run("binding signature simulation", func(t *testing.T) {
		// bvk = cv_in - cv_out = (rcv_in - rcv_out)·R when values balance.
		rIn, err := c.RandomScalar()
		require.NoError(t, err)
		rOut, err := c.RandomScalar()
		require.NoError(t, err)
		cvIn, err := c.ValueCommit(100, rIn)
		require.NoError(t, err)
		cvOut, err := c.ValueCommit(100, rOut)
		require.NoError(t, err)
		bvk := c.ValueCommitSum([]P{cvIn, c.NegatePoint(cvOut)})

		bsk := new(big.Int).Sub(PF(&rIn).BigInt(new(big.Int)), PF(&rOut).BigInt(new(big.Int)))
		bsk.Mod(bsk, c.Order())
		var sk F
		PF(&sk).SetBigInt(bsk)

		sig, err := c.Sign(sk, []byte("action"), c.R)
		require.NoError(t, err)
		require.NoError(t, c.Verify(bvk, []byte("action"), sig, c.R))

		// Unbalanced values must not verify.
		cvBad, err := c.ValueCommit(101, rOut)
		require.NoError(t, err)
		badBvk := c.ValueCommitSum([]P{cvIn, c.NegatePoint(cvBad)})
		assert.ErrorIs(t, c.Verify(badBvk, []byte("action"), sig, c.R), edwards.ErrInvalidSignature)
	})
}

func TestJubjub(t *testing.T)     { testCurve(t, edwards.Jubjub()) }
func TestBabyJubjub(t *testing.T) { testCurve(t, edwards.BabyJubjub()) }

// Generator coordinates are frozen: changing them invalidates every existing
// token commitment. They were captured from the pre-generic jubjub and
// babyjubjub packages.
func TestGeneratorVectors(t *testing.T) {
	check := func(t *testing.T, name string, gotX, gotY *big.Int, wantX, wantY string) {
		t.Helper()
		assert.Equal(t, wantX, gotX.String(), "%s.X", name)
		assert.Equal(t, wantY, gotY.String(), "%s.Y", name)
	}

	j := edwards.Jubjub()
	check(t, "jubjub.V", j.V.X.BigInt(new(big.Int)), j.V.Y.BigInt(new(big.Int)),
		"18955113602438449591542643116897633044716435604316294307957756681119459652281",
		"49600227710820047791359142373396645431280967898553252281565448718924404466246")
	check(t, "jubjub.R", j.R.X.BigInt(new(big.Int)), j.R.Y.BigInt(new(big.Int)),
		"49647968873187265745563060490388662308533136858349115331068450585330066656273",
		"32890154870528567639755663365437113250601413960557662457861827098657101994344")

	b := edwards.BabyJubjub()
	check(t, "babyjubjub.V", b.V.X.BigInt(new(big.Int)), b.V.Y.BigInt(new(big.Int)),
		"2414823977351907978799482954791503628826897208319605264180126545755923085536",
		"4047414257371987378882004648248111198963817083806289603211040455602207409438")
	check(t, "babyjubjub.R", b.R.X.BigInt(new(big.Int)), b.R.Y.BigInt(new(big.Int)),
		"7200769120081648145528256323932764713592699151809163987861572361418551369696",
		"21343041816133509405379287289965761053375209993030812606491807276326656152098")

	assert.Equal(t, 96, j.SignatureSize())
	assert.Equal(t, 96, b.SignatureSize())
}

func TestNewRejectsOversizedOrder(t *testing.T) {
	_, err := edwards.New[edwardsFr, *edwardsFr, int](edwards.Group[edwardsFr, int]{Name: "bad"}, edwards.Config{})
	require.Error(t, err)
}

func FuzzDeserializeSignature(f *testing.F) {
	c := edwards.Jubjub()
	sk, _ := c.RandomScalar()
	sig, _ := c.Sign(sk, []byte("seed"), c.G)
	valid, _ := c.SerializeSignature(sig)
	f.Add(valid)
	f.Add([]byte{})
	f.Add(valid[:95])
	f.Add(make([]byte, 96))
	f.Add(append(make([]byte, 0, 96), repeat(0xff, 96)...))
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := c.DeserializeSignature(raw)
		if err != nil {
			return
		}
		out, err := c.SerializeSignature(got)
		require.NoError(t, err)
		require.Equal(t, raw, out)
	})
}

func repeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}

	return out
}

func TestIsOnCurve(t *testing.T) {
	c := edwards.Jubjub()
	assert.True(t, c.IsOnCurve(c.V))
	assert.True(t, c.IsOnCurve(c.Identity()))
	bad, err := c.PointFromBytes(make([]byte, 32), append(make([]byte, 31), 2))
	require.NoError(t, err)
	assert.False(t, c.IsOnCurve(bad))
}

func TestForCurve(t *testing.T) {
	for id, name := range map[ecc.ID]string{ecc.BLS12_381: "jubjub", ecc.BN254: "babyjubjub"} {
		s, err := edwards.ForCurve(id)
		require.NoError(t, err)
		assert.Equal(t, name, s.Name())
	}
	_, err := edwards.ForCurve(ecc.BW6_761)
	require.Error(t, err)

	// Round trip through the byte-oriented API on both curves.
	c := edwards.BabyJubjub()
	s, err := edwards.ForCurve(ecc.BN254)
	require.NoError(t, err)

	r1, err := c.RandomScalar()
	require.NoError(t, err)
	cv, err := c.ValueCommit(5, r1)
	require.NoError(t, err)
	co := edwards.Coords{X: cv.X.Marshal(), Y: cv.Y.Marshal()}
	require.NoError(t, s.CheckPoint(co))
	require.Error(t, s.CheckPoint(edwards.Coords{X: co.X, Y: co.X}))
	require.Error(t, s.CheckPoint(edwards.Coords{X: co.X[1:], Y: co.Y}))

	bvk, err := s.BindingKey(nil, []edwards.Coords{co}, 5) // 5·V − cv = −r1·R
	require.NoError(t, err)
	var sk fr.Element
	sk.SetBigInt(new(big.Int).Sub(c.Order(), r1.BigInt(new(big.Int))))
	sig, err := c.Sign(sk, []byte("m"), c.R)
	require.NoError(t, err)
	raw, err := c.SerializeSignature(sig)
	require.NoError(t, err)
	require.NoError(t, s.VerifyBinding(bvk, []byte("m"), raw))
	require.Error(t, s.VerifyBinding(bvk, []byte("x"), raw))
	require.Error(t, s.VerifyBinding(bvk, []byte("m"), raw[1:]))
}
