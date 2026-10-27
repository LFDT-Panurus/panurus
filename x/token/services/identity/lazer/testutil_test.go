/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"sync"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// memKeystore is an in-memory idriver.Keystore.
type memKeystore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKeystore) Put(id string, key any) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string][]byte{}
	}
	b, ok := key.([]byte)
	if !ok {
		return errors.New("only []byte supported")
	}
	k.m[id] = append([]byte(nil), b...)

	return nil
}

func (k *memKeystore) Get(id string, key any) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	b, ok := k.m[id]
	if !ok {
		return errors.Errorf("no entry for [%s]", id)
	}
	p, ok := key.(*[]byte)
	if !ok {
		return errors.New("only *[]byte supported")
	}
	*p = append([]byte(nil), b...)

	return nil
}

func (k *memKeystore) Delete(id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, id)

	return nil
}

func (k *memKeystore) Close() error { return nil }

// memIdentityStore is an in-memory IdentityStoreService.
type memIdentityStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memIdentityStore) put(id, info []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[string(id)] = info
}

func (s *memIdentityStore) GetSignerInfo(_ context.Context, id []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.m[string(id)], nil
}

type mockConfig struct{}

func (mockConfig) CacheSizeForOwnerID(string) int { return 0 }
func (mockConfig) TranslatePath(p string) string  { return p }

// testWallet is an issuer and a wallet issued by it.
type testWallet struct {
	isk, ipk []byte
	signer   *SignerConfig
	keystore *memKeystore
	store    *memIdentityStore
	km       *KeyManager
}

func newTestWallet(tb testing.TB, eid string) *testWallet {
	tb.Helper()
	w := &testWallet{keystore: &memKeystore{}, store: &memIdentityStore{}}
	w.isk, w.ipk = NewIssuerKey()
	w.addWallet(tb, eid)

	return w
}

func (w *testWallet) addWallet(tb testing.TB, eid string) {
	tb.Helper()
	signer, err := IssueSignerConfig(w.isk, w.ipk, "org1", "member", eid, "rh-"+eid)
	require.NoError(tb, err)
	w.signer = signer
	conf := &Config{Version: ProtocolVersionV1, Name: eid, IPK: w.ipk, Signer: signer}
	km, err := NewKeyManager(conf, w.keystore, w.store)
	require.NoError(tb, err)
	tb.Cleanup(km.Close)
	w.km = km
}
