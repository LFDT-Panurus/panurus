/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKeyManagerProvider(t *testing.T) {
	testNewKeyManagerProvider(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewKeyManagerProvider(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewKeyManagerProvider(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)

	// create mock identity store service
	identityStoreService := &mock.IdentityStoreService{}

	// create key manager provider
	kmp := NewKeyManagerProvider(
		config.Ipk,
		curveID,
		keyStore,
		&mockConfig{},
		0,
		false,
		&disabled.Provider{},
		identityStoreService,
	)
	assert.NotNil(t, kmp)
}

type mockConfig struct{}

func (m mockConfig) CacheSizeForOwnerID(string) int {
	return 0
}

func (m mockConfig) TranslatePath(path string) string {
	return path
}
