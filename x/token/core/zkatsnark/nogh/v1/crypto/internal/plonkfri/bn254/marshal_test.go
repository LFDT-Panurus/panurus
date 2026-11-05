/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri_test

import (
	"bytes"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	cs "github.com/consensys/gnark/constraint/bn254"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/stretchr/testify/require"

	plonkfri "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/internal/plonkfri/bn254"
)

type marshalFixture struct {
	pkBytes, vkBytes, proofBytes []byte
}

func newMarshalFixture(tb testing.TB) (*marshalFixture, func(proof *plonkfri.Proof, vk *plonkfri.VerifyingKey) error) {
	tb.Helper()
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareMulCircuit{})
	require.NoError(tb, err)
	pk, vk, err := plonkfri.Setup(ccs.(*cs.SparseR1CS))
	require.NoError(tb, err)

	w, err := frontend.NewWitness(&squareMulCircuit{X: 3, Y: 2, Out: 18}, ecc.BN254.ScalarField())
	require.NoError(tb, err)
	pub, err := w.Public()
	require.NoError(tb, err)
	proof, err := plonkfri.Prove(ccs.(*cs.SparseR1CS), pk, w)
	require.NoError(tb, err)

	var pkB, vkB, prB bytes.Buffer
	_, err = pk.WriteTo(&pkB)
	require.NoError(tb, err)
	_, err = vk.WriteTo(&vkB)
	require.NoError(tb, err)
	_, err = proof.WriteTo(&prB)
	require.NoError(tb, err)

	f := &marshalFixture{pkBytes: pkB.Bytes(), vkBytes: vkB.Bytes(), proofBytes: prB.Bytes()}
	verify := func(proof *plonkfri.Proof, vk *plonkfri.VerifyingKey) error {
		return plonkfri.Verify(proof, vk, pub.Vector().(fr.Vector))
	}

	return f, verify
}

// TestMarshalRoundTrip checks that keys and proof survive serialization: a
// deserialized proving key proves, and the proof verifies under a
// deserialized verifying key after a proof round trip.
func TestMarshalRoundTrip(t *testing.T) {
	f, verify := newMarshalFixture(t)

	vk := &plonkfri.VerifyingKey{}
	n, err := vk.ReadFrom(bytes.NewReader(f.vkBytes))
	require.NoError(t, err)
	require.EqualValues(t, len(f.vkBytes), n)

	proof := &plonkfri.Proof{}
	_, err = proof.ReadFrom(bytes.NewReader(f.proofBytes))
	require.NoError(t, err)
	require.NoError(t, verify(proof, vk), "proof must verify after round trip")

	// deterministic encoding
	var again bytes.Buffer
	_, err = vk.WriteTo(&again)
	require.NoError(t, err)
	require.Equal(t, f.vkBytes, again.Bytes())

	// a deserialized proving key must produce proofs the original vk accepts
	pk := &plonkfri.ProvingKey{}
	_, err = pk.ReadFrom(bytes.NewReader(f.pkBytes))
	require.NoError(t, err)
	var pkAgain bytes.Buffer
	_, err = pk.WriteTo(&pkAgain)
	require.NoError(t, err)
	require.Equal(t, f.pkBytes, pkAgain.Bytes())

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareMulCircuit{})
	require.NoError(t, err)
	w, err := frontend.NewWitness(&squareMulCircuit{X: 5, Y: 2, Out: 50}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	pub, err := w.Public()
	require.NoError(t, err)
	p2, err := plonkfri.Prove(ccs.(*cs.SparseR1CS), pk, w)
	require.NoError(t, err)
	require.NoError(t, plonkfri.Verify(p2, vk, pub.Vector().(fr.Vector)))
}

// TestMarshalRejectsMalformed checks truncation, tampering and trailing-free
// behaviour of the decoders: they must return errors, never panic.
func TestMarshalRejectsMalformed(t *testing.T) {
	f, verify := newMarshalFixture(t)

	for cut := range len(f.vkBytes) {
		vk := &plonkfri.VerifyingKey{}
		_, err := vk.ReadFrom(bytes.NewReader(f.vkBytes[:cut]))
		require.Error(t, err, "truncated vk at %d", cut)
	}
	for cut := 0; cut < len(f.proofBytes); cut += 97 {
		p := &plonkfri.Proof{}
		_, err := p.ReadFrom(bytes.NewReader(f.proofBytes[:cut]))
		require.Error(t, err, "truncated proof at %d", cut)
	}
	for cut := 0; cut < len(f.pkBytes); cut += 211 {
		pk := &plonkfri.ProvingKey{}
		_, err := pk.ReadFrom(bytes.NewReader(f.pkBytes[:cut]))
		require.Error(t, err, "truncated pk at %d", cut)
	}

	// flipping any byte of the vk must be caught at decode or verification
	vkGood := &plonkfri.VerifyingKey{}
	_, err := vkGood.ReadFrom(bytes.NewReader(f.vkBytes))
	require.NoError(t, err)
	proof := &plonkfri.Proof{}
	_, err = proof.ReadFrom(bytes.NewReader(f.proofBytes))
	require.NoError(t, err)
	for i := range f.vkBytes {
		bad := bytes.Clone(f.vkBytes)
		bad[i] ^= 0x01
		vk := &plonkfri.VerifyingKey{}
		if _, err := vk.ReadFrom(bytes.NewReader(bad)); err != nil {
			continue
		}
		require.Error(t, verify(proof, vk), "tampered vk byte %d accepted", i)
	}

	// flipping a byte of the proof must never verify (nor panic)
	for i := 0; i < len(f.proofBytes); i += 53 {
		bad := bytes.Clone(f.proofBytes)
		bad[i] ^= 0x01
		p := &plonkfri.Proof{}
		if _, err := p.ReadFrom(bytes.NewReader(bad)); err != nil {
			continue
		}
		require.Error(t, verify(p, vkGood), "tampered proof byte %d accepted", i)
	}

	// tampered proving key: the recomputed commitment must not match the vk
	for _, i := range []int{len(f.vkBytes) + 5, len(f.pkBytes) / 2, len(f.pkBytes) - 1} {
		bad := bytes.Clone(f.pkBytes)
		bad[i] ^= 0x01
		pk := &plonkfri.ProvingKey{}
		_, err := pk.ReadFrom(bytes.NewReader(bad))
		require.Error(t, err, "tampered pk byte %d accepted", i)
	}
}

// FuzzReadFrom feeds arbitrary bytes to the three decoders; none may panic,
// and anything accepted must re-encode to exactly the consumed bytes.
func FuzzReadFrom(f *testing.F) {
	fx, _ := newMarshalFixture(f)
	f.Add(fx.vkBytes, fx.proofBytes)
	f.Add([]byte{}, []byte{})
	f.Add(fx.vkBytes[:len(fx.vkBytes)/2], fx.proofBytes[:len(fx.proofBytes)/2])
	f.Add(bytes.Repeat([]byte{0xff}, 64), bytes.Repeat([]byte{0xff}, 64))
	f.Fuzz(func(t *testing.T, vkData, proofData []byte) {
		vk := &plonkfri.VerifyingKey{}
		if n, err := vk.ReadFrom(bytes.NewReader(vkData)); err == nil {
			var out bytes.Buffer
			_, werr := vk.WriteTo(&out)
			require.NoError(t, werr)
			require.Equal(t, vkData[:n], out.Bytes())
		}
		p := &plonkfri.Proof{}
		if n, err := p.ReadFrom(bytes.NewReader(proofData)); err == nil {
			var out bytes.Buffer
			_, werr := p.WriteTo(&out)
			require.NoError(t, werr)
			require.Equal(t, proofData[:n], out.Bytes())
		}
		pk := &plonkfri.ProvingKey{}
		_, _ = pk.ReadFrom(bytes.NewReader(vkData))
	})
}

// TestVerifyingKeyRejectsOversizedDomain: a tiny header claiming a huge circuit
// must be rejected before the FRI domain (gigabytes at 2^20) is allocated.
func TestVerifyingKeyRejectsOversizedDomain(t *testing.T) {
	f, _ := newMarshalFixture(t)

	// The first 8 bytes of the encoding are Size, big-endian.
	for _, logSize := range []uint{18, 20, 40} {
		bad := bytes.Clone(f.vkBytes)
		for i := range 8 {
			bad[i] = 0
		}
		bad[7-logSize/8] = 1 << (logSize % 8)

		vk := &plonkfri.VerifyingKey{}
		_, err := vk.ReadFrom(bytes.NewReader(bad))
		require.Error(t, err, "Size 2^%d must be rejected", logSize)
		// the size check runs before every other consistency check, so this
		// pins the rejection on the cap and not on a later mismatch
		require.ErrorContains(t, err, "bad domain size", "Size 2^%d", logSize)
	}
}
