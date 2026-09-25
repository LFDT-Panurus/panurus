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
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// captureSubmitter captures the submitted tx so tests can assert on it.
type captureSubmitter struct {
	capturedTx *applicationpb.Tx
	submitErr  error
	submitFn   func(tx *applicationpb.Tx) error
}

func (c *captureSubmitter) Submit(_ string, _ string, tx *applicationpb.Tx) error {
	c.capturedTx = tx
	if c.submitFn != nil {
		return c.submitFn(tx)
	}

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
	// Return version 5 encoded as varint under key ("_meta", "ns")
	qs.GetStateReturns(&cdriver.VaultValue{Version: protowire.AppendVarint(nil, 5)}, nil)

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.NotNil(t, sub.capturedTx)
	require.Equal(t, uint64(5), sub.capturedTx.Namespaces[0].NsVersion)

	// Verify the real ppService queried the query service with correct parameters
	require.Equal(t, 1, qs.GetStateCallCount())
	ns, key := qs.GetStateArgsForCall(0)
	require.Equal(t, "_meta", ns)
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

// TestDeployPublicParametersRaw_FetchVersionDecodeError verifies that corrupted varint bytes
// return an explicit error and prevent submission.
func TestDeployPublicParametersRaw_FetchVersionDecodeError(t *testing.T) {
	sub := &captureSubmitter{}
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	// Truncated varint byte yields n < 0 from protowire.ConsumeVarint
	qs.GetStateReturns(&cdriver.VaultValue{Version: []byte{0xff}}, nil)

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid varint")
	require.Nil(t, sub.capturedTx, "transaction must not be submitted when version decode fails")
}

// TestDeployPublicParametersRaw_RetryOnSubmitFailure verifies that a retry
// is attempted on version mismatch errors, re-fetching the namespace version
// through the real PublicParametersService.
// This covers the TOCTOU window where a policy update commits between version fetch
// and submit.
func TestDeployPublicParametersRaw_RetryOnSubmitFailure(t *testing.T) {
	submitCount := 0
	sub := &captureSubmitter{
		submitFn: func(tx *applicationpb.Tx) error {
			submitCount++
			if submitCount == 1 {
				return errors.New("version mismatch error")
			}

			return nil
		},
	}

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	// Return version 1 on first fetch, version 2 on second fetch
	qs.GetStateReturnsOnCall(0, &cdriver.VaultValue{Version: protowire.AppendVarint(nil, 1)}, nil)
	qs.GetStateReturnsOnCall(1, &cdriver.VaultValue{Version: protowire.AppendVarint(nil, 2)}, nil)

	realPPService := pp.NewPublicParametersService(nil, qsp)
	ds := NewTMSDeployerService(realPPService, nil, sub)

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.Equal(t, 2, submitCount, "should have retried once on version mismatch error")
	require.Equal(t, 2, qs.GetStateCallCount(), "should have queried ledger version twice")
	require.Equal(t, uint64(2), sub.capturedTx.Namespaces[0].NsVersion, "retry should use the refreshed version")
}

// TestDeployPublicParametersRaw_NoRetryOnNonVersionErrors verifies that non-version
// errors (transient errors, timeouts, or broad errors like 'validation failed' or 'invalidated')
// do NOT trigger a retry, preventing duplicate/conflicting submissions against an initialized namespace.
func TestDeployPublicParametersRaw_NoRetryOnNonVersionErrors(t *testing.T) {
	nonVersionErrors := []string{
		"connection timeout: max retries reached",
		"validation failed: signature mismatch",
		"transaction invalidated by committer",
		"orderer connection lost",
	}

	for _, errMsg := range nonVersionErrors {
		t.Run(errMsg, func(t *testing.T) {
			submitCount := 0
			sub := &captureSubmitter{
				submitFn: func(tx *applicationpb.Tx) error {
					submitCount++

					return errors.New(errMsg)
				},
			}

			qsp := &mock.QueryServiceProvider{}
			qs := &mock.QueryService{}
			qsp.GetReturns(qs, nil)
			qs.GetStateReturns(&cdriver.VaultValue{Version: protowire.AppendVarint(nil, 1)}, nil)

			realPPService := pp.NewPublicParametersService(nil, qsp)
			ds := NewTMSDeployerService(realPPService, nil, sub)

			err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
			require.Error(t, err)
			require.Contains(t, err.Error(), errMsg)
			require.Equal(t, 1, submitCount, "should NOT retry on non-version error: "+errMsg)
			require.Equal(t, 1, qs.GetStateCallCount(), "should NOT re-fetch version on non-version error: "+errMsg)
		})
	}
}
