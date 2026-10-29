/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup_test

import (
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/params"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
)

func testIdemixKeys() []*pp.IdemixIssuerPublicKey {
	return []*pp.IdemixIssuerPublicKey{{PublicKey: []byte("ipk"), Curve: mathlib.BN254}}
}

func TestGeneratePublicParams(t *testing.T) {
	for _, ps := range []string{setup.ProofSystemGroth16, setup.ProofSystemPlonk, setup.ProofSystemPlonkFRI} {
		t.Run("success_"+ps, func(t *testing.T) {
			p, err := setup.GeneratePublicParams(setup.GenerateOptions{
				Version:                2,
				ProofSystem:            ps,
				Curve:                  ecc.BN254,
				MaxBits:                8,
				Precision:              4,
				IdemixIssuerPublicKeys: testIdemixKeys(),
				Issuers:                []driver.Identity{driver.Identity("issuer")},
				Auditors:               []driver.Identity{driver.Identity("auditor")},
				Extras:                 driver.Extras{"k": []byte("v")},
			})
			require.NoError(t, err)
			assert.Equal(t, ps, p.ProofSystem)
			assert.Equal(t, ecc.BN254, p.Curve)
			assert.Equal(t, 8, p.MaxBits)
			assert.Equal(t, uint64(4), p.PrecisionValue)
			assert.Equal(t, driver.TokenDriverVersion(2), p.SchemeVersion)
			assert.Equal(t, []driver.Identity{driver.Identity("issuer")}, p.Issuers())
			assert.Equal(t, []driver.Identity{driver.Identity("auditor")}, p.Auditors())
			assert.Equal(t, []byte("v"), p.Extras()["k"])
			assert.NotEmpty(t, p.PKSpend)
			assert.NotEmpty(t, p.PKOutput)
			assert.NotEmpty(t, p.VKSpend)
			assert.NotEmpty(t, p.VKOutput)

			raw, err := p.Serialize()
			require.NoError(t, err)
			p2, err := pp.DeserializePublicParams(raw)
			require.NoError(t, err)
			assert.Equal(t, p.VKSpend, p2.VKSpend)
			assert.Equal(t, p.PKOutput, p2.PKOutput)
		})
	}

	t.Run("precision_defaults_to_maxbits", func(t *testing.T) {
		p, err := setup.GeneratePublicParams(setup.GenerateOptions{
			ProofSystem:            setup.ProofSystemPlonk,
			Curve:                  ecc.BN254,
			MaxBits:                8,
			IdemixIssuerPublicKeys: testIdemixKeys(),
		})
		require.NoError(t, err)
		assert.Equal(t, uint64(8), p.PrecisionValue)
		assert.Equal(t, driver.TokenDriverVersion(1), p.SchemeVersion)
		assert.NotNil(t, p.Extras())
		assert.Empty(t, p.Issuers())
		require.Error(t, p.ValidateForDeployment())
	})

	negatives := []struct {
		name string
		opts setup.GenerateOptions
		msg  string
	}{
		{"no_idemix_key", setup.GenerateOptions{Curve: ecc.BN254, MaxBits: 8}, "at least one idemix issuer public key"},
		{"nil_idemix_key", setup.GenerateOptions{Curve: ecc.BN254, MaxBits: 8, IdemixIssuerPublicKeys: []*pp.IdemixIssuerPublicKey{nil}}, "is nil"},
		{"empty_idemix_key", setup.GenerateOptions{Curve: ecc.BN254, MaxBits: 8, IdemixIssuerPublicKeys: []*pp.IdemixIssuerPublicKey{{Curve: mathlib.BN254}}}, "empty public key"},
		{"unknown_proof_system", setup.GenerateOptions{ProofSystem: "stark", Curve: ecc.BN254, MaxBits: 8, IdemixIssuerPublicKeys: testIdemixKeys()}, "unsupported proof system"},
		{"plonkfri_bls12_381", setup.GenerateOptions{ProofSystem: setup.ProofSystemPlonkFRI, Curve: ecc.BLS12_381, MaxBits: 8, IdemixIssuerPublicKeys: testIdemixKeys()}, "supports only Curve"},
		{"unsupported_curve", setup.GenerateOptions{Curve: ecc.BW6_761, MaxBits: 8, IdemixIssuerPublicKeys: testIdemixKeys()}, "unsupported Curve"},
		{"maxbits_too_large", setup.GenerateOptions{Curve: ecc.BN254, MaxBits: params.MaxAllowedBits + 1, IdemixIssuerPublicKeys: testIdemixKeys()}, "at most"},
		{"maxbits_negative", setup.GenerateOptions{Curve: ecc.BN254, MaxBits: -1, IdemixIssuerPublicKeys: testIdemixKeys()}, "must be positive"},
		{"precision_exceeds_maxbits", setup.GenerateOptions{Curve: ecc.BN254, MaxBits: 8, Precision: 9, IdemixIssuerPublicKeys: testIdemixKeys()}, "must not exceed MaxBits"},
		{"precision_exceeds_default_maxbits", setup.GenerateOptions{Curve: ecc.BN254, Precision: 65, IdemixIssuerPublicKeys: testIdemixKeys()}, "must not exceed MaxBits"},
	}
	for _, tc := range negatives {
		t.Run(tc.name, func(t *testing.T) {
			_, err := setup.GeneratePublicParams(tc.opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
		})
	}
}

func TestParseProofSystem(t *testing.T) {
	for in, want := range map[string]string{"groth16": "groth16", "PLONK": "plonk", " PlonkFRI ": "plonkfri"} {
		got, err := setup.ParseProofSystem(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got)
	}
	for _, in := range []string{"", "stark", "groth"} {
		_, err := setup.ParseProofSystem(in)
		require.Error(t, err, in)
	}
}

func TestParseCurve(t *testing.T) {
	for in, want := range map[string]ecc.ID{"bn254": ecc.BN254, "BN254": ecc.BN254, "bls12_381": ecc.BLS12_381, "BLS12_381": ecc.BLS12_381} {
		got, err := setup.ParseCurve(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got)
	}
	_, err := setup.ParseCurve("foo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid curve")
	_, err = setup.ParseCurve("bls12_377")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported curve")
}
