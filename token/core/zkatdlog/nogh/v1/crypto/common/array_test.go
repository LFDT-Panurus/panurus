/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"crypto/sha256"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/crypto"
	"github.com/stretchr/testify/require"
)

func randomG1(t *testing.T, c *math.Curve) *math.G1 {
	t.Helper()
	randReader, err := c.Rand()
	require.NoError(t, err)

	zr := c.NewRandomZr(randReader)
	g := c.NewG1()
	g.Add(c.GenG1.Mul(zr))

	return g
}

func TestGetG1ArrayAndBytes(t *testing.T) {
	require.NotEmpty(t, math.Curves)
	curve := math.Curves[0]

	g1 := randomG1(t, curve)
	g2 := randomG1(t, curve)

	a := GetG1Array([]*math.G1{g1}, []*math.G1{g2})
	b, err := a.Bytes()
	require.NoError(t, err)

	raw := [][]byte{g1.Bytes(), g2.Bytes()}
	expected := crypto.AppendFixed32([]byte{}, raw)
	require.Equal(t, expected, b)
}

func TestBytesToAndReuseBuffer(t *testing.T) {
	require.NotEmpty(t, math.Curves)
	curve := math.Curves[0]

	g1 := randomG1(t, curve)
	g2 := randomG1(t, curve)
	a := GetG1Array([]*math.G1{g1, g2})

	// produce canonical bytes
	canonical, err := a.Bytes()
	require.NoError(t, err)

	// provide a non-empty buffer (should be cleared by BytesTo)
	buf := []byte("prefix-data")
	out, err := a.BytesTo(buf)
	require.NoError(t, err)
	require.Equal(t, canonical, out)
}

func TestBytesWithNilElementErrors(t *testing.T) {
	require.NotEmpty(t, math.Curves)
	curve := math.Curves[0]

	g1 := randomG1(t, curve)
	a := GetG1Array([]*math.G1{g1, nil})
	_, err := a.Bytes()
	require.Error(t, err)

	_, err = a.BytesTo(nil)
	require.Error(t, err)
}

func TestHashG1Array(t *testing.T) {
	require.NotEmpty(t, math.Curves)
	curve := math.Curves[0]

	g1 := randomG1(t, curve)
	g2 := randomG1(t, curve)

	h := sha256.New()
	hRes := HashG1Array(h, g1, g2)

	// compute expected by hand
	h2 := sha256.New()
	h2.Reset()
	h2.Write(g1.Bytes())
	h2.Write(g2.Bytes())
	exp := h2.Sum(nil)
	require.Equal(t, exp, hRes)
}
