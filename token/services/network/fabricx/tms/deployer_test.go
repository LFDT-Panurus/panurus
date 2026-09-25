/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/stretchr/testify/require"
)

// mockPPService stubs pp.PublicParametersService for tests.
// It records the last submitted tx so tests can assert on NsVersion.
type mockPPService struct {
	fetchErr        error
	versionToReturn uint64
	versionErr      error
}

func (m *mockPPService) Fetch(_ cdriver.Network, _ cdriver.Channel, _ cdriver.Namespace) ([]byte, error) {
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}

	return []byte("pp-data"), nil
}

func (m *mockPPService) FetchNamespaceVersion(_ cdriver.Network, _ cdriver.Channel, _ cdriver.Namespace) (uint64, error) {
	return m.versionToReturn, m.versionErr
}

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

// mockPPServiceFn is a variant of mockPPService that uses a function for FetchNamespaceVersion.
type mockPPServiceFn struct {
	versionFn func() (uint64, error)
}

func (m *mockPPServiceFn) Fetch(_ cdriver.Network, _ cdriver.Channel, _ cdriver.Namespace) ([]byte, error) {
	return []byte("pp-data"), nil
}

func (m *mockPPServiceFn) FetchNamespaceVersion(_ cdriver.Network, _ cdriver.Channel, _ cdriver.Namespace) (uint64, error) {
	v, err := m.versionFn()

	return v, err
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
// deployPublicParametersRaw fetches the namespace version and propagates it
// into the submitted transaction. This is the regression test for #2256.
// Reverting the FetchNamespaceVersion call in deployPublicParametersRaw
// causes NsVersion to be 0, which fails this assertion.
func TestDeployPublicParametersRaw_UsesVersionFromFetcher(t *testing.T) {
	sub := &captureSubmitter{}
	mock := &mockPPService{versionToReturn: 5}

	ds := &deployerService{
		ppFetcher:     mock,
		nsSubmitter:   sub,
		keyTranslator: &keys.Translator{},
	}

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.NotNil(t, sub.capturedTx)
	require.Equal(t, uint64(5), sub.capturedTx.Namespaces[0].NsVersion)
}

// TestDeployPublicParametersRaw_FetchVersionError verifies that an error from
// FetchNamespaceVersion is propagated and the transaction is never submitted.
func TestDeployPublicParametersRaw_FetchVersionError(t *testing.T) {
	sub := &captureSubmitter{}
	mock := &mockPPService{versionErr: errors.New("query service unavailable")}

	ds := &deployerService{
		ppFetcher:     mock,
		nsSubmitter:   sub,
		keyTranslator: &keys.Translator{},
	}

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "query service unavailable")
	require.Nil(t, sub.capturedTx, "transaction must not be submitted when version fetch fails")
}

// TestDeployPublicParametersRaw_RetryOnSubmitFailure verifies that a retry
// is attempted on version mismatch errors, re-fetching the namespace version.
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
	// Return different versions on each call to simulate a policy update between retries.
	callCount := 0
	mock := &mockPPServiceFn{
		versionFn: func() (uint64, error) {
			callCount++

			return uint64(callCount), nil //nolint:gosec
		},
	}

	ds := &deployerService{
		ppFetcher:     mock,
		nsSubmitter:   sub,
		keyTranslator: &keys.Translator{},
	}

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.Equal(t, 2, submitCount, "should have retried once on version mismatch error")
	require.Equal(t, 2, callCount, "should have fetched version twice")
	require.Equal(t, uint64(2), sub.capturedTx.Namespaces[0].NsVersion, "retry should use the refreshed version")
}

// TestDeployPublicParametersRaw_NoRetryOnTransientError verifies that transient
// network or submission errors (non-version errors) do not trigger a retry,
// preventing duplicate/conflicting submissions against an initialized namespace.
func TestDeployPublicParametersRaw_NoRetryOnTransientError(t *testing.T) {
	submitCount := 0
	sub := &captureSubmitter{
		submitFn: func(tx *applicationpb.Tx) error {
			submitCount++

			return errors.New("connection timeout: max retries reached")
		},
	}
	callCount := 0
	mock := &mockPPServiceFn{
		versionFn: func() (uint64, error) {
			callCount++

			return 1, nil
		},
	}

	ds := &deployerService{
		ppFetcher:     mock,
		nsSubmitter:   sub,
		keyTranslator: &keys.Translator{},
	}

	err := ds.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection timeout")
	require.Equal(t, 1, submitCount, "should NOT retry on transient submit error")
	require.Equal(t, 1, callCount, "should NOT re-fetch version on transient error")
}
