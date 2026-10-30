/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp_test

import (
	"crypto/sha256"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils/dlogpp"
)

// withLegacyDLog returns default public parameters embedding ZKAT-DLOG public
// parameters on curve, with placeholder migration keys.
func withLegacyDLog(tb testing.TB, curve mathlib.CurveID) *pp.PublicParams {
	tb.Helper()
	p := pp.DefaultPublicParams()
	_, err := dlogpp.Embed(p, curve)
	require.NoError(tb, err)
	p.PKMigration, p.VKMigration = []byte("pk"), []byte("vk")

	return p
}

func TestPublicParams_LegacyDLog(t *testing.T) {
	_, err := pp.DefaultPublicParams().LegacyDLog()
	require.ErrorIs(t, err, pp.ErrNoLegacyDLog)

	p := withLegacyDLog(t, dlogpp.DefaultCurve)
	legacy, err := p.LegacyDLog()
	require.NoError(t, err)
	require.Equal(t, dlogpp.DefaultCurve, legacy.PP.Curve)
	digest := sha256.Sum256(p.LegacyDLogPublicParams)
	require.Equal(t, digest[:], []byte(legacy.Hash))

	p.LegacyDLogPublicParams = []byte("garbage")
	_, err = p.LegacyDLog()
	require.Error(t, err)
}

func TestPublicParams_ValidateMigration(t *testing.T) {
	require.NoError(t, withLegacyDLog(t, dlogpp.DefaultCurve).Validate())
	require.NoError(t, withLegacyDLog(t, mathlib.BLS12_381_GURVY).Validate())
	for _, tc := range []struct {
		name   string
		mutate func(p *pp.PublicParams)
	}{
		{"migration keys without ZKAT-DLOG public parameters", func(p *pp.PublicParams) { p.LegacyDLogPublicParams = nil }},
		{"ZKAT-DLOG public parameters without verifying key", func(p *pp.PublicParams) { p.VKMigration = nil }},
		{"malformed ZKAT-DLOG public parameters", func(p *pp.PublicParams) { p.LegacyDLogPublicParams = []byte("garbage") }},
		{"BN254 zkatsnark curve", func(p *pp.PublicParams) { p.Curve = ecc.BN254 }},
		{"plonkfri", func(p *pp.PublicParams) { p.ProofSystem = "plonkfri" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := withLegacyDLog(t, dlogpp.DefaultCurve)
			tc.mutate(p)
			require.Error(t, p.Validate())
		})
	}
	t.Run("BN254 ZKAT-DLOG deployment", func(t *testing.T) {
		require.Error(t, withLegacyDLog(t, mathlib.BN254).Validate())
	})
	t.Run("plonk", func(t *testing.T) {
		p := withLegacyDLog(t, dlogpp.DefaultCurve)
		p.ProofSystem = "plonk"
		require.NoError(t, p.Validate())
	})
}

func TestPublicParams_LegacyDLog_RoundTrip(t *testing.T) {
	p := withLegacyDLog(t, dlogpp.DefaultCurve)
	raw, err := p.Serialize()
	require.NoError(t, err)
	got, err := pp.DeserializePublicParams(raw)
	require.NoError(t, err)
	require.Equal(t, p.LegacyDLogPublicParams, got.LegacyDLogPublicParams)
	require.NoError(t, got.Validate())
}
