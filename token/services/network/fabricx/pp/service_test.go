/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/qe/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestFetchNamespaceVersion_NilValue(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(nil, nil)

	s := pp.NewPublicParametersService(nil, qsp)
	ver, err := s.FetchNamespaceVersion("net", "ch", "ns")
	require.NoError(t, err)
	require.Equal(t, uint64(0), ver)
}

func TestFetchNamespaceVersion_EmptyVersion(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(&driver.VaultValue{Version: nil}, nil)

	s := pp.NewPublicParametersService(nil, qsp)
	ver, err := s.FetchNamespaceVersion("net", "ch", "ns")
	require.NoError(t, err)
	require.Equal(t, uint64(0), ver)

	qs.GetStateReturns(&driver.VaultValue{Version: []byte{}}, nil)
	ver2, err2 := s.FetchNamespaceVersion("net", "ch", "ns")
	require.NoError(t, err2)
	require.Equal(t, uint64(0), ver2)
}

func TestFetchNamespaceVersion_ValidVersion(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(&driver.VaultValue{Version: protowire.AppendVarint(nil, 7)}, nil)

	s := pp.NewPublicParametersService(nil, qsp)
	ver, err := s.FetchNamespaceVersion("net", "ch", "ns")
	require.NoError(t, err)
	require.Equal(t, uint64(7), ver)
}

func TestFetchNamespaceVersion_InvalidVarint(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(&driver.VaultValue{Version: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}, nil)

	s := pp.NewPublicParametersService(nil, qsp)
	_, err := s.FetchNamespaceVersion("net", "ch", "ns")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid varint")
}

func TestFetchNamespaceVersion_QueryServiceError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qsp.GetReturns(nil, errors.New("qs unavailable"))

	s := pp.NewPublicParametersService(nil, qsp)
	_, err := s.FetchNamespaceVersion("net", "ch", "ns")
	require.Error(t, err)
	require.Contains(t, err.Error(), "qs unavailable")
}

func TestFetchNamespaceVersion_GetStateError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(nil, errors.New("get state failed"))

	s := pp.NewPublicParametersService(nil, qsp)
	_, err := s.FetchNamespaceVersion("net", "ch", "ns")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get state failed")
}
