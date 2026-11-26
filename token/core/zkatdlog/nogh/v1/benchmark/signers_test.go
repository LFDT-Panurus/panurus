/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveSigners(t *testing.T, registerIssuer bool) (SetupParams, string, *SetupConfiguration) {
	t.Helper()

	params := SetupParams{
		IdemixTestdataPath: "../testdata",
		Bits:               []uint64{32},
		CurveIDs:           []math.CurveID{math.BN254},
		OwnerIdentityType:  idemixnym.IdentityType,
		ProofType:          rp.RangeProofType,
		GeneratorDomain:    "test.domain",
	}
	newConfigurations := NewSetupConfigurationsWithParams
	if !registerIssuer {
		newConfigurations = NewOpenIssuerPolicySetupConfigurations
	}
	configurations, err := newConfigurations(params)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, configurations.SaveWithSignersTo(dir))
	cfg, err := configurations.GetSetupConfiguration(32, math.BN254)
	require.NoError(t, err)

	return params, dir, cfg
}

func TestLoadSetupConfigurations(t *testing.T) {
	for _, registerIssuer := range []bool{true, false} {
		params, dir, cfg := saveSigners(t, registerIssuer)

		loaded, err := LoadSetupConfigurations(os.DirFS(dir), key(32, math.BN254), params)
		require.NoError(t, err)
		got, err := loaded.GetSetupConfiguration(32, math.BN254)
		require.NoError(t, err)
		require.NotNil(t, got.OwnerIdentity)
		assert.Equal(t, uint64(32), got.Bits)
		assert.Equal(t, math.BN254, got.CurveID)
		assert.NotNil(t, got.ExecutorProvider)

		want, err := cfg.PP.Serialize()
		require.NoError(t, err)
		raw, err := got.PP.Serialize()
		require.NoError(t, err)
		assert.Equal(t, want, raw)
		assert.Equal(t, registerIssuer, len(got.PP.IssuerIDs) == 1)

		// the loaded signers match the identities registered in the parameters
		assert.Equal(t, []byte(cfg.AuditorSigner.ID), []byte(got.PP.AuditorIDs[0]))
		assert.Equal(t, []byte(cfg.SecondAuditorSigner.ID), []byte(got.PP.AuditorIDs[1]))
		if registerIssuer {
			assert.Equal(t, []byte(cfg.IssuerSigner.ID), []byte(got.PP.IssuerIDs[0]))
		}
		for _, s := range []*Signer{got.AuditorSigner, got.SecondAuditorSigner, got.IssuerSigner} {
			msg := []byte("message")
			sig, err := s.Sign(msg)
			require.NoError(t, err)
			assert.NotEmpty(t, sig)
			assert.NotEmpty(t, s.AuditInfo)
		}
		sig, err := got.AuditorSigner.Sign([]byte("m"))
		require.NoError(t, err)
		// the signature verifies against the original public key
		assert.True(t, ecdsaVerify(t, cfg.AuditorSigner, []byte("m"), sig))
	}
}

func TestLoadSetupConfigurationsFailures(t *testing.T) {
	params, dir, _ := saveSigners(t, true)
	sub := key(32, math.BN254)

	for _, name := range []string{"params.txt", "pp.json", "signers.json"} {
		t.Run("missing "+name, func(t *testing.T) {
			tmp := t.TempDir()
			require.NoError(t, os.CopyFS(tmp, os.DirFS(dir)))
			require.NoError(t, os.Remove(filepath.Join(tmp, sub, name)))
			_, err := LoadSetupConfigurations(os.DirFS(tmp), sub, params)
			require.Error(t, err)
		})
	}

	for name, meta := range map[string]string{
		"bits mismatch":  `{"pp":"","bits":64,"curveID":1}`,
		"curve mismatch": `{"pp":"","bits":32,"curveID":2}`,
	} {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			require.NoError(t, os.CopyFS(tmp, os.DirFS(dir)))
			require.NoError(t, os.WriteFile(filepath.Join(tmp, sub, "pp.json"), []byte(meta), 0o600))
			_, err := LoadSetupConfigurations(os.DirFS(tmp), sub, params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "does not match the public parameters")
		})
	}

	t.Run("corrupt key", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.CopyFS(tmp, os.DirFS(dir)))
		require.NoError(t, os.WriteFile(filepath.Join(tmp, sub, "signers.json"),
			[]byte(`{"auditor":{"sk":"AAAA"},"second_auditor":{},"issuer":{}}`), 0o600))
		_, err := LoadSetupConfigurations(os.DirFS(tmp), sub, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "private key")
	})
}

func ecdsaVerify(t *testing.T, s *Signer, msg, sig []byte) bool {
	t.Helper()

	digest := sha256.Sum256(msg)

	return ecdsa.VerifyASN1(&s.SK.PublicKey, digest[:], sig)
}

// TestUseLegacyGenerators checks that UseLegacyGenerators really replaces every generator.
func TestUseLegacyGenerators(t *testing.T) {
	curve := math.Curves[math.BN254]
	eq := func(a, b []*math.G1) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if !a[i].Equals(b[i]) {
				return false
			}
		}

		return true
	}
	for name, proofType := range map[string]rp.ProofType{"bulletproof": rp.RangeProofType, "csp": rp.CSPRangeProofType} {
		t.Run(name, func(t *testing.T) {
			cfgs, err := NewSetupConfigurationsWithParams(SetupParams{
				IdemixTestdataPath: "../testdata",
				Bits:               []uint64{32},
				CurveIDs:           []math.CurveID{math.BN254},
				OwnerIdentityType:  idemixnym.IdentityType,
				ProofType:          proofType,
			})
			require.NoError(t, err)
			pp, err := cfgs.GetPublicParams(32, math.BN254)
			require.NoError(t, err)

			// snapshot of the derived defaults
			defPedersen := append([]*math.G1(nil), pp.PedersenGenerators...)
			var defLeft, defRight []*math.G1
			var defP, defQ *math.G1
			if rpp := pp.RangeProofParams; rpp != nil {
				defLeft, defRight = append(defLeft, rpp.LeftGenerators...), append(defRight, rpp.RightGenerators...)
				defP, defQ = rpp.P, rpp.Q
			}
			if cpp := pp.CSPRangeProofParams; cpp != nil {
				defLeft, defRight = append(defLeft, cpp.LeftGenerators...), append(defRight, cpp.RightGenerators...)
			}

			pp.GeneratorDomain = "set"
			require.NoError(t, UseLegacyGenerators(pp))

			assert.Empty(t, pp.GeneratorDomain)
			assert.False(t, eq(pp.PedersenGenerators, defPedersen), "pedersen generators are still derived")
			if rpp := pp.RangeProofParams; rpp != nil {
				assert.False(t, eq(rpp.LeftGenerators, defLeft), "left generators are still derived")
				assert.False(t, eq(rpp.RightGenerators, defRight), "right generators are still derived")
				assert.False(t, rpp.P.Equals(defP), "P is still derived")
				assert.False(t, rpp.Q.Equals(defQ), "Q is still derived")
				assert.True(t, rpp.P.Equals(curve.HashToG1([]byte("0"))))
				assert.True(t, rpp.Q.Equals(curve.HashToG1([]byte("1"))))
			}
			if cpp := pp.CSPRangeProofParams; cpp != nil {
				assert.False(t, eq(cpp.LeftGenerators, defLeft), "csp left generators are still derived")
				assert.False(t, eq(cpp.RightGenerators, defRight), "csp right generators are still derived")
			}
			require.NoError(t, pp.Validate())
		})
	}
}
