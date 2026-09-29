/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchSetupHashVersion(t *testing.T) {
	setupHashKey, err := (&keys.Translator{}).CreateSetupHashKey()
	require.NoError(t, err)

	t.Run("returns the ledger version of the setup hash key", func(t *testing.T) {
		qs := &mock.QueryService{}
		qs.GetStateReturns(&cdriver.VaultValue{Raw: []byte("digest"), Version: vault.MarshalVersion(3)}, nil)
		qsp := &mock.QueryServiceProvider{}
		qsp.GetReturns(qs, nil)

		version, found, err := pp.NewPublicParametersService(nil, qsp).FetchSetupHashVersion("net", "ch", "ns")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, uint64(3), version)

		network, channel := qsp.GetArgsForCall(0)
		assert.Equal(t, "net", network)
		assert.Equal(t, "ch", channel)

		// the version must be read for the key the endorser adds the versioned
		// read dependency for, that is, the setup hash key
		namespace, key := qs.GetStateArgsForCall(0)
		assert.Equal(t, "ns", namespace)
		assert.Equal(t, setupHashKey, key)
	})

	t.Run("reports a setup hash key at version zero", func(t *testing.T) {
		qs := &mock.QueryService{}
		qs.GetStateReturns(&cdriver.VaultValue{Raw: []byte("digest"), Version: vault.MarshalVersion(0)}, nil)
		qsp := &mock.QueryServiceProvider{}
		qsp.GetReturns(qs, nil)

		version, found, err := pp.NewPublicParametersService(nil, qsp).FetchSetupHashVersion("net", "ch", "ns")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, uint64(0), version)
	})

	t.Run("reports a missing setup hash key as not found", func(t *testing.T) {
		qs := &mock.QueryService{}
		qs.GetStateReturns(nil, nil)
		qsp := &mock.QueryServiceProvider{}
		qsp.GetReturns(qs, nil)

		version, found, err := pp.NewPublicParametersService(nil, qsp).FetchSetupHashVersion("net", "ch", "ns")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, uint64(0), version)
	})

	t.Run("fails when the query service is not available", func(t *testing.T) {
		qsp := &mock.QueryServiceProvider{}
		qsp.GetReturns(nil, errors.New("no query service"))

		_, found, err := pp.NewPublicParametersService(nil, qsp).FetchSetupHashVersion("net", "ch", "ns")
		require.ErrorContains(t, err, "no query service")
		assert.False(t, found)
	})

	t.Run("fails when the state cannot be read", func(t *testing.T) {
		qs := &mock.QueryService{}
		qs.GetStateReturns(nil, errors.New("ledger unreachable"))
		qsp := &mock.QueryServiceProvider{}
		qsp.GetReturns(qs, nil)

		_, found, err := pp.NewPublicParametersService(nil, qsp).FetchSetupHashVersion("net", "ch", "ns")
		require.ErrorContains(t, err, "ledger unreachable")
		assert.False(t, found)
	})
}
