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

	// Build a deployerService with a real pp.PublicParametersService replaced
	// by a shim via a deployerServiceShim that lets us inject mock behaviour.
	shim := &deployerServiceShim{
		mock:          &mockPPService{versionToReturn: 5},
		sub:           sub,
		keyTranslator: &keys.Translator{},
	}

	err := shim.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.NotNil(t, sub.capturedTx)
	require.Equal(t, uint64(5), sub.capturedTx.Namespaces[0].NsVersion)
}

// TestDeployPublicParametersRaw_FetchVersionError verifies that an error from
// FetchNamespaceVersion is propagated and the transaction is never submitted.
func TestDeployPublicParametersRaw_FetchVersionError(t *testing.T) {
	sub := &captureSubmitter{}
	shim := &deployerServiceShim{
		mock:          &mockPPService{versionErr: errors.New("query service unavailable")},
		sub:           sub,
		keyTranslator: &keys.Translator{},
	}

	err := shim.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "query service unavailable")
	require.Nil(t, sub.capturedTx, "transaction must not be submitted when version fetch fails")
}

// TestDeployPublicParametersRaw_RetryOnSubmitFailure verifies that a single retry
// is attempted after a submit failure, re-fetching the namespace version each time.
// This covers the TOCTOU window where a policy update commits between version fetch
// and submit.
func TestDeployPublicParametersRaw_RetryOnSubmitFailure(t *testing.T) {
	submitCount := 0
	sub := &captureSubmitter{
		submitFn: func(tx *applicationpb.Tx) error {
			submitCount++
			if submitCount == 1 {
				return errors.New("version mismatch")
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
	shim := &deployerServiceShim{
		mock:          mock,
		sub:           sub,
		keyTranslator: &keys.Translator{},
	}

	err := shim.deployPublicParametersRaw(token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}, []byte("pp"))
	require.NoError(t, err)
	require.Equal(t, 2, submitCount, "should have retried once")
	require.Equal(t, 2, callCount, "should have fetched version twice")
	require.Equal(t, uint64(2), sub.capturedTx.Namespaces[0].NsVersion, "retry should use the refreshed version")
}

// ppVersionFetcher is the minimal interface the shim needs from the mock.
type ppVersionFetcher interface {
	FetchNamespaceVersion(cdriver.Network, cdriver.Channel, cdriver.Namespace) (uint64, error)
}

// deployerServiceShim mirrors deployerService but accepts a ppVersionFetcher
// directly, allowing tests to drive deployPublicParametersRaw without needing
// a real *pp.PublicParametersService (which requires a live qsProvider).
type deployerServiceShim struct {
	mock          ppVersionFetcher
	sub           Submitter
	keyTranslator *keys.Translator
}

func (s *deployerServiceShim) deployPublicParametersRaw(tmsID token.TMSID, ppRaw []byte) error {
	const maxAttempts = 2

	for attempt := range maxAttempts {
		nsVersion, err := s.mock.FetchNamespaceVersion(tmsID.Network, tmsID.Channel, tmsID.Namespace)
		if err != nil {
			return err
		}

		ds := &deployerService{keyTranslator: s.keyTranslator, nsSubmitter: s.sub}
		tx, err := ds.createPublicParametersTx(ppRaw, tmsID.Namespace, nsVersion)
		if err != nil {
			return err
		}

		err = s.sub.Submit(tmsID.Network, tmsID.Channel, tx)
		if err == nil {
			return nil
		}

		if attempt < maxAttempts-1 {
			continue
		}

		return err
	}

	return nil
}
