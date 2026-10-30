/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idleVerifiers returns the number of idle verifier states pooled by p.
func idleVerifiers(p *presVerifier) int {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()

	return len(p.idle)
}

// TestVerifierPoolKeepsAtMostFourIdle checks that a presVerifier keeps at
// most verifierPoolSize (4) idle verifier states: more concurrent
// verifications than that create extra states, which are released instead of
// pooled when they are given back.
func TestVerifierPoolKeepsAtMostFourIdle(t *testing.T) {
	require.Equal(t, 4, verifierPoolSize)

	t.Run("acquire and release", func(t *testing.T) {
		w := newTestWallet(t, "alice")
		pv, err := newPresVerifier(w.ipk)
		require.NoError(t, err)
		t.Cleanup(pv.Close)
		assert.Equal(t, 1, idleVerifiers(pv), "a new pool holds the verifier that checked the key")

		const n = verifierPoolSize + 3
		vs := make([]*anoncreds.Verifier, 0, n)
		for range n {
			v, err := pv.acquire()
			require.NoError(t, err)
			vs = append(vs, v)
		}
		assert.Zero(t, idleVerifiers(pv))
		for i, v := range vs {
			pv.release(v)
			assert.Equal(t, min(i+1, verifierPoolSize), idleVerifiers(pv))
		}
	})

	t.Run("concurrent verifications", func(t *testing.T) {
		w := newTestWallet(t, "alice")
		d, err := w.km.Identity(t.Context(), nil)
		require.NoError(t, err)
		msg := []byte("m")
		sigma, err := d.Signer.Sign(msg)
		require.NoError(t, err)

		const workers = 2 * verifierPoolSize
		start := make(chan struct{})
		errs := make([]error, workers)
		var wg sync.WaitGroup
		for i := range workers {
			wg.Go(func() {
				<-start
				errs[i] = d.Verifier.Verify(msg, sigma)
			})
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
		idle := idleVerifiers(w.km.pv)
		assert.LessOrEqual(t, idle, verifierPoolSize)
		assert.Positive(t, idle)
	})
}
