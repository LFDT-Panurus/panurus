/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/vault"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/require"
)

// captureSubmitter captures the submitted tx so tests can assert on it.
type captureSubmitter struct {
	capturedTx *applicationpb.Tx
	submitErr  error
}

func (c *captureSubmitter) Submit(_ string, _ string, tx *applicationpb.Tx) error {
	c.capturedTx = tx

	return c.submitErr
}

// TestCreatePublicParametersTx_NsVersionCopied verifies that the NsVersion passed
// to createPublicParametersTx is reflected in the built transaction.
func TestCreatePublicParametersTx_NsVersionCopied(t *testing.T) {
	s := &deployerService{
		keyTranslator: &keys.Translator{},
	}

	tx, err := s.createPublicParametersTx([]byte("raw-pp"), "test-ns", 7)
	require.NoError(t, err)
	require.NotNil(t, tx)
	require.Len(t, tx.Namespaces, 1)
	require.Equal(t, "test-ns", tx.Namespaces[0].NsId)
	require.Equal(t, uint64(7), tx.Namespaces[0].NsVersion)
}

// TestDeployPublicParametersRaw_UsesVersionFromFetcher verifies that
// deployPublicParametersRaw drives the real pp.PublicParametersService, fetches
// the namespace version from the ledger, and propagates it into the submitted transaction.
// This is the regression test for #2256. Reverting the FetchNamespaceVersion call in
// deployPublicParametersRaw causes NsVersion to be 0 and breaks this test.
func TestDeployPublicParametersRaw_UsesVersionFromFetcher(t *testing.T) {
	sub := &captureSubmitter{}
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	// Return version 5 encoded via vault.MarshalVersion under key (committerpb.MetaNamespaceID, "ns")
	qs.GetStateReturns(&cdriver.VaultValue{Version: vault.MarshalVersion(5)}, nil)

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.NotNil(t, sub.capturedTx)
	require.Equal(t, uint64(5), sub.capturedTx.Namespaces[0].NsVersion)

	// Verify the real ppService queried the query service with correct parameters
	require.Equal(t, 1, qs.GetStateCallCount())
	ns, key := qs.GetStateArgsForCall(0)
	require.Equal(t, committerpb.MetaNamespaceID, ns)
	require.Equal(t, "ns", key)
}

// TestDeployPublicParametersRaw_InitialDeploymentVersionZero verifies that an unregistered
// namespace or nil version defaults cleanly to version 0 for an initial deployment.
func TestDeployPublicParametersRaw_InitialDeploymentVersionZero(t *testing.T) {
	sub := &captureSubmitter{}
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(nil, nil)

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.NotNil(t, sub.capturedTx)
	require.Equal(t, uint64(0), sub.capturedTx.Namespaces[0].NsVersion)
}

// TestDeployPublicParametersRaw_FetchVersionError verifies that an error from
// FetchNamespaceVersion is propagated and the transaction is never submitted.
func TestDeployPublicParametersRaw_FetchVersionError(t *testing.T) {
	sub := &captureSubmitter{}
	qsp := &mock.QueryServiceProvider{}
	qsp.GetReturns(nil, errors.New("query service unavailable"))

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "query service unavailable")
	require.Nil(t, sub.capturedTx, "transaction must not be submitted when version fetch fails")
}

// TestDeployPublicParametersRaw_SubmitError verifies that an error from Submitter.Submit
// is returned directly to the caller.
func TestDeployPublicParametersRaw_SubmitError(t *testing.T) {
	sub := &captureSubmitter{
		submitErr: errors.New("network submit failed"),
	}
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(&cdriver.VaultValue{Version: vault.MarshalVersion(3)}, nil)

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "network submit failed")
	require.NotNil(t, sub.capturedTx)
	require.Equal(t, uint64(3), sub.capturedTx.Namespaces[0].NsVersion)
}
