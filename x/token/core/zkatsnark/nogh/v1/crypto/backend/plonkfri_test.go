/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package backend_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
)

// plonkFRIFixture holds a plonkfri backend with keys set up for trivialCircuit.
type plonkFRIFixture struct {
	b                backend.ProofSystemBackend
	cs               constraint.ConstraintSystem
	pkBytes, vkBytes []byte
	pk               *backend.ProvingKey
	vk               *backend.VerifyingKey
}

func newPlonkFRIFixture(tb testing.TB) *plonkFRIFixture {
	tb.Helper()

	b, err := backend.NewProofSystemBackend("plonkfri", ecc.BN254)
	require.NoError(tb, err)
	cs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(tb, err)
	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(tb, err)
	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(tb, err)
	vk, err := b.NewVerifyingKey(vkBytes)
	require.NoError(tb, err)

	return &plonkFRIFixture{b: b, cs: cs, pkBytes: pkBytes, vkBytes: vkBytes, pk: pk, vk: vk}
}

func TestNewProofSystemBackend_PlonkFRI(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonkfri", ecc.BN254)
	require.NoError(t, err)
	require.NotNil(t, b)
}

func TestNewProofSystemBackend_PlonkFRI_RejectsOtherCurves(t *testing.T) {
	_, err := backend.NewProofSystemBackend("plonkfri", ecc.BLS12_381)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supports only curve")
}

func TestPlonkFRI_SetupProveVerify(t *testing.T) {
	f := newPlonkFRIFixture(t)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	proof, err := f.b.Prove(f.cs, f.pk, w)
	require.NoError(t, err)
	require.NotEmpty(t, proof)

	pubW, err := w.Public()
	require.NoError(t, err)
	require.NoError(t, f.b.Verify(f.vk, proof, pubW), "valid plonkfri proof must verify")

	// the proof is bound to the public input
	wrong, err := frontend.NewWitness(&trivialCircuit{X: 1, Y: 43}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	wrongPub, err := wrong.Public()
	require.NoError(t, err)
	require.Error(t, f.b.Verify(f.vk, proof, wrongPub))
}

// TestPlonkFRI_SetupIsDeterministic: plonkfri is transparent, so the same
// circuit must yield byte-identical keys on every setup.
func TestPlonkFRI_SetupIsDeterministic(t *testing.T) {
	f := newPlonkFRIFixture(t)

	pkBytes, vkBytes, err := f.b.Setup(f.cs)
	require.NoError(t, err)
	assert.Equal(t, f.pkBytes, pkBytes)
	assert.Equal(t, f.vkBytes, vkBytes)
}

func TestPlonkFRI_Prove_InvalidWitness(t *testing.T) {
	f := newPlonkFRIFixture(t)

	w, err := frontend.NewWitness(&trivialCircuit{X: 1, Y: 2}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	_, err = f.b.Prove(f.cs, f.pk, w)
	require.Error(t, err, "unsatisfied witness must not yield a proof")
}

func TestPlonkFRI_Verify_TamperedAndTruncatedProof(t *testing.T) {
	f := newPlonkFRIFixture(t)

	w, err := frontend.NewWitness(&trivialCircuit{X: 7, Y: 7}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	proof, err := f.b.Prove(f.cs, f.pk, w)
	require.NoError(t, err)
	pubW, err := w.Public()
	require.NoError(t, err)

	for i := 0; i < len(proof); i += 61 {
		bad := append([]byte(nil), proof...)
		bad[i] ^= 0x01
		require.Error(t, f.b.Verify(f.vk, bad, pubW), "tampered byte %d accepted", i)
	}
	require.Error(t, f.b.Verify(f.vk, proof[:len(proof)/2], pubW))
	require.Error(t, f.b.Verify(f.vk, nil, pubW))
	require.Error(t, f.b.Verify(f.vk, []byte("garbage"), pubW))
}

// TestPlonkFRI_Verify_RejectsForeignPublicWitness checks that a public witness
// over a field other than bn254's is rejected with an error, not a panic, and
// before the proof is even decoded.
func TestPlonkFRI_Verify_RejectsForeignPublicWitness(t *testing.T) {
	fx := newPlonkFRIFixture(t)
	w, err := frontend.NewWitness(&trivialCircuit{X: 3, Y: 3}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	proof, err := fx.b.Prove(fx.cs, fx.pk, w)
	require.NoError(t, err)

	foreign, err := frontend.NewWitness(&trivialCircuit{X: 3, Y: 3}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)
	err = fx.b.Verify(fx.vk, proof, foreign)
	require.ErrorIs(t, err, witness.ErrInvalidWitness)

	require.Error(t, fx.b.Verify(fx.vk, proof, nil))
}

// TestPlonkFRI_Verify_RejectsFullWitness checks that a full witness (public
// and secret values) passed as the public witness is rejected.
func TestPlonkFRI_Verify_RejectsFullWitness(t *testing.T) {
	fx := newPlonkFRIFixture(t)
	w, err := frontend.NewWitness(&trivialCircuit{X: 3, Y: 3}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	proof, err := fx.b.Prove(fx.cs, fx.pk, w)
	require.NoError(t, err)

	err = fx.b.Verify(fx.vk, proof, w)
	require.ErrorContains(t, err, "witness length is invalid")
}

func TestPlonkFRI_Keys_CorruptBytes(t *testing.T) {
	f := newPlonkFRIFixture(t)

	_, err := f.b.NewVerifyingKey([]byte("garbage-vk"))
	require.Error(t, err)
	_, err = f.b.NewProvingKey([]byte("garbage-pk"))
	require.Error(t, err)

	bad := append([]byte(nil), f.pkBytes...)
	bad[len(bad)-1] ^= 0x01
	_, err = f.b.NewProvingKey(bad)
	require.Error(t, err, "a tampered proving key must not match its verifying key")
}

// TestPlonkFRI_RejectsForeignKeysAndConstraintSystems: mismatches must be
// errors, never gnark panics.
func TestPlonkFRI_RejectsForeignKeysAndConstraintSystems(t *testing.T) {
	f := newPlonkFRIFixture(t)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	pubW, err := w.Public()
	require.NoError(t, err)

	// a plonk key handed to the plonkfri backend
	p, err := backend.NewProofSystemBackend("plonk", ecc.BN254)
	require.NoError(t, err)
	plonkPKBytes, plonkVKBytes, err := p.Setup(f.cs)
	require.NoError(t, err)
	plonkPK, err := p.NewProvingKey(plonkPKBytes)
	require.NoError(t, err)
	plonkVK, err := p.NewVerifyingKey(plonkVKBytes)
	require.NoError(t, err)

	_, err = f.b.Prove(f.cs, plonkPK, w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plonk")
	require.Error(t, f.b.Verify(plonkVK, []byte("x"), pubW))

	// a plonkfri key handed to the plonk backend
	_, err = p.Prove(f.cs, f.pk, w)
	require.Error(t, err)
	require.Error(t, p.Verify(f.vk, []byte("x"), pubW))

	// nil keys
	_, err = f.b.Prove(f.cs, nil, w)
	require.Error(t, err)
	require.Error(t, f.b.Verify(nil, []byte("x"), pubW))

	// an R1CS (groth16-shaped) constraint system
	r1csCS, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)
	_, _, err = f.b.Setup(r1csCS)
	require.Error(t, err)
	_, err = f.b.Prove(r1csCS, f.pk, w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SparseR1CS")
}

// FuzzPlonkFRIVerifyNoPanic fuzzes the plonkfri key and proof decoders, which
// parse attacker-controlled bytes (public parameters and proofs on the wire).
// Anything accepted as a verifying key must also survive Verify.
func FuzzPlonkFRIVerifyNoPanic(f *testing.F) {
	fx := newPlonkFRIFixture(f)
	w, err := frontend.NewWitness(&trivialCircuit{X: 3, Y: 3}, ecc.BN254.ScalarField())
	require.NoError(f, err)
	proof, err := fx.b.Prove(fx.cs, fx.pk, w)
	require.NoError(f, err)
	pubW, err := w.Public()
	require.NoError(f, err)

	f.Add(fx.vkBytes, proof)
	f.Add([]byte{}, []byte{})
	f.Add(fx.vkBytes[:len(fx.vkBytes)/2], proof[:len(proof)/2])
	f.Add([]byte("garbage"), []byte("garbage"))

	f.Fuzz(func(t *testing.T, vkRaw, proofRaw []byte) {
		if vk, err := fx.b.NewVerifyingKey(vkRaw); err == nil {
			require.NotNil(t, vk)
			_ = fx.b.Verify(vk, proofRaw, pubW)
		}
		_ = fx.b.Verify(fx.vk, proofRaw, pubW)
		if pk, err := fx.b.NewProvingKey(vkRaw); err == nil {
			require.NotNil(t, pk)
		}
	})
}

// TestTrailingBytesRejected checks that, for every proof system, a key or proof
// followed by extra bytes is rejected instead of silently accepted: gnark's
// ReadFrom stops at the end of the object, so without an explicit check the
// encoding would be malleable.
func TestTrailingBytesRejected(t *testing.T) {
	cases := []struct {
		system string
		curve  ecc.ID
		sparse bool // scs (plonk, plonkfri) vs r1cs (groth16)
	}{
		{"groth16", ecc.BN254, false},
		{"plonk", ecc.BN254, true},
		{"plonkfri", ecc.BN254, true},
	}
	for _, tc := range cases {
		t.Run(tc.system, func(t *testing.T) {
			b, err := backend.NewProofSystemBackend(tc.system, tc.curve)
			require.NoError(t, err)
			var cs constraint.ConstraintSystem
			if tc.sparse {
				cs, err = frontend.Compile(tc.curve.ScalarField(), scs.NewBuilder, &trivialCircuit{})
			} else {
				cs, err = frontend.Compile(tc.curve.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
			}
			require.NoError(t, err)
			pkBytes, vkBytes, err := b.Setup(cs)
			require.NoError(t, err)
			pk, err := b.NewProvingKey(pkBytes)
			require.NoError(t, err)
			vk, err := b.NewVerifyingKey(vkBytes)
			require.NoError(t, err)

			w, err := frontend.NewWitness(&trivialCircuit{X: 9, Y: 9}, tc.curve.ScalarField())
			require.NoError(t, err)
			pubW, err := w.Public()
			require.NoError(t, err)
			proof, err := b.Prove(cs, pk, w)
			require.NoError(t, err)
			require.NoError(t, b.Verify(vk, proof, pubW), "the untouched proof must verify")

			garbage := []byte{1, 2, 3}
			_, err = b.NewProvingKey(append(append([]byte(nil), pkBytes...), garbage...))
			require.ErrorIs(t, err, backend.ErrTrailingBytes, "proving key with trailing bytes")
			_, err = b.NewVerifyingKey(append(append([]byte(nil), vkBytes...), garbage...))
			require.ErrorIs(t, err, backend.ErrTrailingBytes, "verifying key with trailing bytes")
			err = b.Verify(vk, append(append([]byte(nil), proof...), garbage...), pubW)
			require.ErrorIs(t, err, backend.ErrTrailingBytes, "proof with trailing bytes")
		})
	}
}
