/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	tokenconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/vault"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/assert"
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

// newTestPPService returns a public parameters service over a ledger with no namespace version.
func newTestPPService() *pp.PublicParametersService {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStateReturns(nil, nil)

	return pp.NewPublicParametersService(nil, qsp)
}

// recordingSubmitter keeps every transaction it is asked to submit.
type recordingSubmitter struct {
	txs []*applicationpb.Tx
}

func (r *recordingSubmitter) Submit(_, _ string, tx *applicationpb.Tx) error {
	r.txs = append(r.txs, tx)

	return nil
}

var testTMSID = token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

// newTestDeployer builds a deployer over a real core.yaml with one TMS whose publicParameters
// section is ppYAML.
func newTestDeployer(t *testing.T, ppYAML string) (*deployerService, *recordingSubmitter) {
	t.Helper()
	dir := t.TempDir()
	raw := `
fsc:
  id: test-node
token:
  enabled: true
  tms:
    mytms:
      network: testnet
      channel: testchannel
      namespace: testns
` + ppYAML
	require.NoError(t, os.WriteFile(filepath.Join(dir, "core.yaml"), []byte(raw), 0o600))
	provider, err := fscconfig.NewProvider(dir)
	require.NoError(t, err)
	submitter := &recordingSubmitter{}

	return NewTMSDeployerService(newTestPPService(), tokenconfig.NewService(provider), submitter), submitter
}

// TestDeployTMSUsesTheConfiguredPublicParameters is the first deployment on a namespace that was
// never initialized. The parameters used to be read from the ledger, which has none yet, so the
// deploy committed an empty value and the namespace was unusable.
func TestDeployTMSUsesTheConfiguredPublicParameters(t *testing.T) {
	ppRaw := []byte("public-parameters-from-the-file")
	ppPath := filepath.Join(t.TempDir(), "pp.json")
	require.NoError(t, os.WriteFile(ppPath, ppRaw, 0o600))
	d, submitter := newTestDeployer(t, `
      publicParameters:
        path: `+ppPath+`
`)

	require.NoError(t, d.DeployTMSs())

	require.Len(t, submitter.txs, 1)
	writes := submitter.txs[0].GetNamespaces()[0].GetBlindWrites()
	key, err := (&keys.Translator{}).CreateSetupKey()
	require.NoError(t, err)
	hashKey, err := (&keys.Translator{}).CreateSetupHashKey()
	require.NoError(t, err)
	hash := sha256.Sum256(ppRaw)
	require.Len(t, writes, 2)
	assert.Equal(t, []byte(key), writes[0].GetKey())
	assert.Equal(t, ppRaw, writes[0].GetValue())
	assert.Equal(t, []byte(hashKey), writes[1].GetKey())
	assert.Equal(t, hash[:], writes[1].GetValue())
}

// TestDeployTMSFailsWithoutPublicParameters checks a TMS whose public parameters cannot be loaded
// fails the deploy and submits nothing, rather than committing an empty value.
func TestDeployTMSFailsWithoutPublicParameters(t *testing.T) {
	t.Run("no path configured", func(t *testing.T) {
		d, submitter := newTestDeployer(t, "")

		err := d.DeployTMS(testTMSID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no public parameters path")
		assert.Empty(t, submitter.txs)
	})

	t.Run("file missing", func(t *testing.T) {
		d, submitter := newTestDeployer(t, `
      publicParameters:
        path: `+filepath.Join(t.TempDir(), "missing.json")+`
`)

		require.Error(t, d.DeployTMS(testTMSID))
		assert.Empty(t, submitter.txs)
	})

	t.Run("file empty", func(t *testing.T) {
		ppPath := filepath.Join(t.TempDir(), "empty.json")
		require.NoError(t, os.WriteFile(ppPath, nil, 0o600))
		d, submitter := newTestDeployer(t, `
      publicParameters:
        path: `+ppPath+`
`)

		err := d.DeployTMS(testTMSID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty public parameters")
		assert.Empty(t, submitter.txs)
	})
}

// TestDeployTMSWithPPRefusesEmptyParameters checks the explicit entry point refuses empty public
// parameters too.
func TestDeployTMSWithPPRefusesEmptyParameters(t *testing.T) {
	d, submitter := newTestDeployer(t, "")

	require.Error(t, d.DeployTMSWithPP(testTMSID, nil))
	assert.Empty(t, submitter.txs)

	require.NoError(t, d.DeployTMSWithPP(testTMSID, []byte("pp")))
	assert.Len(t, submitter.txs, 1)
}
