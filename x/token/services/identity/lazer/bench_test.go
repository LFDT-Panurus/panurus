/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkLazer(b *testing.B) {
	w := newTestWallet(b, "alice")
	ctx := context.Background()
	des, err := NewDeserializer(w.ipk)
	require.NoError(b, err)
	b.Cleanup(des.Close)

	var d any
	b.Run("Identity", func(b *testing.B) {
		for range b.N {
			desc, err := w.km.Identity(ctx, nil)
			require.NoError(b, err)
			d = desc
		}
	})
	desc, err := w.km.Identity(ctx, nil)
	require.NoError(b, err)
	_ = d
	msg := []byte("m")
	var sigma []byte
	b.Run("Sign", func(b *testing.B) {
		for range b.N {
			sigma, err = desc.Signer.Sign(msg)
			require.NoError(b, err)
		}
	})
	b.Logf("sizes: identity=%dB signature=%dB auditinfo=%dB signerinfo=%dB", len(desc.Identity), len(sigma), len(desc.AuditInfo), len(desc.SignerInfo))
	b.Run("VerifyUncached", func(b *testing.B) {
		for range b.N {
			d2, err := w.km.Identity(ctx, nil)
			require.NoError(b, err)
			s, err := d2.Signer.Sign(msg)
			require.NoError(b, err)
			b.StartTimer()
			v, _ := des.DeserializeVerifier(ctx, d2.Identity)
			require.NoError(b, v.Verify(msg, s))
			b.StopTimer()
		}
	})
	b.Run("VerifyCachedPres", func(b *testing.B) {
		v, _ := des.DeserializeVerifier(ctx, desc.Identity)
		require.NoError(b, v.Verify(msg, sigma))
		b.ResetTimer()
		for range b.N {
			require.NoError(b, v.Verify(msg, sigma))
		}
	})
}
