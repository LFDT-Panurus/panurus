/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyManagerProvider(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ca, user := filepath.Join(dir, "ca"), filepath.Join(dir, "alice")

	// the lazergen flow
	isk, ipk := NewIssuerKey()
	require.NoError(t, WriteIssuerKeys(ca, isk, ipk))
	signer, err := IssueSignerConfig(isk, ipk, "org1", "member", "alice", "rh-alice")
	require.NoError(t, err)
	require.NoError(t, WriteIssuerKeys(user, nil, ipk))
	require.NoError(t, WriteSignerConfig(user, signer, true))
	remote := *signer
	remote.Sk = nil
	require.NoError(t, WriteSignerConfig(user, &remote, false))

	keystore, store := &memKeystore{}, &memIdentityStore{}
	kmp := NewKeyManagerProvider(ipk, keystore, store, mockConfig{}, 2, true, &disabled.Provider{})
	idConfig := &driver.IdentityConfiguration{ID: "alice", URL: user}
	km, err := kmp.Get(ctx, idConfig)
	require.NoError(t, err)
	if c, ok := km.(interface{ Close() }); ok {
		t.Cleanup(c.Close)
	}
	assert.False(t, km.IsRemote())
	assert.Equal(t, "alice", km.EnrollmentID())

	// the secret was moved to the keystore and removed from the stored configuration
	assert.Len(t, keystore.m, 1)
	conf, err := NewConfigFromRaw(ipk, idConfig.Raw)
	require.NoError(t, err)
	assert.Empty(t, conf.Signer.Sk)
	assert.NotEmpty(t, conf.Signer.Ski)
	assert.NotContains(t, string(idConfig.Raw), string(conf.Signer.Cred)+"x")

	d, err := km.Identity(ctx, nil)
	require.NoError(t, err)
	sigma, err := d.Signer.Sign([]byte("m"))
	require.NoError(t, err)
	require.NoError(t, d.Verifier.Verify([]byte("m"), sigma))

	// reload from the stored raw configuration: the secret comes from the keystore
	idConfig2 := &driver.IdentityConfiguration{ID: "alice", Raw: idConfig.Raw}
	km2, err := kmp.Get(ctx, idConfig2)
	require.NoError(t, err)
	if c, ok := km2.(interface{ Close() }); ok {
		t.Cleanup(c.Close)
	}
	assert.False(t, km2.IsRemote())
	store.put(d.Identity, d.SignerInfo)
	s, err := km2.DeserializeSigner(ctx, d.Identity)
	require.NoError(t, err)
	sigma, err = s.Sign([]byte("again"))
	require.NoError(t, err)
	v, err := km2.DeserializeVerifier(ctx, d.Identity)
	require.NoError(t, err)
	require.NoError(t, v.Verify([]byte("again"), sigma))

	// the wrong keystore cannot load the wallet
	kmp3 := NewKeyManagerProvider(ipk, &memKeystore{}, store, mockConfig{}, 0, true, &disabled.Provider{})
	_, err = kmp3.Get(ctx, &driver.IdentityConfiguration{ID: "alice", Raw: idConfig.Raw})
	require.Error(t, err)

	// without ignoreVerifyOnlyWallet the verify-only SignerConfig is used: remote
	require.NoError(t, os.Remove(filepath.Join(user, "user", ConfigFileSignerFull)))
	kmp4 := NewKeyManagerProvider(ipk, &memKeystore{}, store, mockConfig{}, 0, false, &disabled.Provider{})
	km4, err := kmp4.Get(ctx, &driver.IdentityConfiguration{ID: "alice", URL: user})
	require.NoError(t, err)
	assert.True(t, km4.IsRemote())
	_, err = km4.Identity(ctx, nil)
	require.Error(t, err)

	// a configuration for another issuer is refused
	_, otherIPK := NewIssuerKey()
	kmp5 := NewKeyManagerProvider(otherIPK, &memKeystore{}, store, mockConfig{}, 0, false, &disabled.Provider{})
	_, err = kmp5.Get(ctx, &driver.IdentityConfiguration{ID: "alice", Raw: idConfig.Raw})
	require.Error(t, err)
}

// TestKeyManagerProviderSharesOneLimiter checks that the key managers of one
// provider share a single ProverLimiter of DefaultProverLimit: with all its
// slots taken, an identity generation through the other key manager waits
// until a slot is released.
func TestKeyManagerProviderSharesOneLimiter(t *testing.T) {
	w := newTestWallet(t, "alice")
	raw, err := (&Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}).Marshal()
	require.NoError(t, err)
	kmp := NewKeyManagerProvider(w.ipk, &memKeystore{}, w.store, mockConfig{}, 0, true, &disabled.Provider{})
	get := func() *WrappedKeyManager {
		t.Helper()
		km, err := kmp.Get(t.Context(), &driver.IdentityConfiguration{ID: "alice", Raw: bytes.Clone(raw)})
		require.NoError(t, err)
		wkm, ok := km.(*WrappedKeyManager)
		require.True(t, ok)
		t.Cleanup(wkm.Close)

		return wkm
	}
	km1, km2 := get(), get()

	limiter := km1.limiter
	require.NotNil(t, limiter)
	assert.Same(t, limiter, km2.limiter, "the key managers of a provider must share one limiter")
	assert.Equal(t, DefaultProverLimit(), cap(limiter.slots))

	// take every slot through the first key manager's limiter
	for range cap(limiter.slots) {
		require.NoError(t, limiter.acquire(t.Context(), nil))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = km2.KeyManager.Identity(ctx, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded, "the shared limiter did not bound the second key manager")

	// a waiting generation proceeds once a slot is released
	res := make(chan error, 1)
	go func() {
		_, err := km2.KeyManager.Identity(t.Context(), nil)
		res <- err
	}()
	select {
	case err := <-res:
		t.Fatalf("identity generated while the shared limiter was full: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	limiter.release()
	select {
	case err := <-res:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("waiter not released by the shared limiter")
	}
	for range cap(limiter.slots) - 1 {
		limiter.release()
	}
	assert.Empty(t, limiter.slots, "limiter slots leaked")
}
