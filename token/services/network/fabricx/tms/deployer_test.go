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
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	return NewTMSDeployerService(tokenconfig.NewService(provider), submitter), submitter
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
