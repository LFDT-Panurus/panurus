/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
