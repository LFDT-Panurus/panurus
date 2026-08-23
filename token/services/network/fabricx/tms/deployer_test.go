/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/require"
)

type mockFetcherWithVersion struct{}

func (m *mockFetcherWithVersion) Fetch(network cdriver.Network, channel cdriver.Channel, namespace cdriver.Namespace) ([]byte, error) {
	return []byte("pp-data"), nil
}

func (m *mockFetcherWithVersion) FetchNamespaceVersion(network cdriver.Network, channel cdriver.Channel, namespace cdriver.Namespace) (uint64, error) {
	return 2, nil
}

func TestCreatePublicParametersTxDynamicNsVersion(t *testing.T) {
	s := &deployerService{
		ppFetcher:     &mockFetcherWithVersion{},
		keyTranslator: &keys.Translator{},
	}

	tx, err := s.createPublicParametersTx([]byte("raw-pp"), "test-ns", 2)
	require.NoError(t, err)
	require.NotNil(t, tx)
	require.Len(t, tx.Namespaces, 1)
	require.Equal(t, "test-ns", tx.Namespaces[0].NsId)
	require.Equal(t, uint64(2), tx.Namespaces[0].NsVersion)
}
