/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

// idemixTestdataPath points at the idemix MSP material shared with
// zkatdlog/nogh/v1 (generic idemix fixtures, nothing zkatdlog-specific).
const idemixTestdataPath = "../../../../../../../token/core/zkatdlog/nogh/v1/testdata"

var testCurves = []ecc.ID{ecc.BN254, ecc.BLS12_381}

func TestNewEnv(t *testing.T) {
	configurations, err := NewSetupConfigurations(idemixTestdataPath, testCurves, idemixnym.IdentityType)
	require.NoError(t, err)

	for _, curveID := range testCurves {
		configuration, err := configurations.GetSetupConfiguration(curveID)
		require.NoError(t, err)

		env, err := NewEnv(configuration)
		require.NoError(t, err, "failed to build env for curve [%s]", curveID)
		require.NotNil(t, env)

		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithIssueTxID, env.TRWithIssueRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithRedeemTxID, env.TRWithRedeemRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithTransferTxID, env.TRWithTransferRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithSwapTxID, env.TRWithSwapRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithPublicMetadataIssueTxID, env.TRWithPublicMetadataIssueRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithPublicMetadataTransferTxID, env.TRWithPublicMetadataTransferRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithMultiAuditorTransferTxID, env.TRWithMultiAuditorTransferRaw)

		// Negative fixtures: validation over the real validator stack must fail.
		_, _, err = env.Engine.VerifyTokenRequestFromRaw(t.Context(), env.Ledger.GetState, driver.TokenRequestAnchor(env.TRWithUnclaimedMetadataTransferTxID), env.TRWithUnclaimedMetadataTransferRaw)
		require.Error(t, err, "unclaimed-metadata transfer must fail re-validation for curve [%s]", curveID)

		_, _, err = env.Engine.VerifyTokenRequestFromRaw(t.Context(), env.Ledger.GetState, driver.TokenRequestAnchor(env.TRWithExtraSignatureTransferTxID), env.TRWithExtraSignatureTransferRaw)
		require.Error(t, err, "extra-signature transfer must fail re-validation for curve [%s]", curveID)
	}
}

func assertPositiveFixture(t *testing.T, engine *validator.Validator, l *ledger, txID string, raw []byte) {
	t.Helper()
	_, _, err := engine.VerifyTokenRequestFromRaw(t.Context(), l.GetState, driver.TokenRequestAnchor(txID), raw)
	assert.NoError(t, err)
}

func TestNewOpenPolicyEnv(t *testing.T) {
	configurations, err := NewOpenIssuerPolicySetupConfigurations(SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		CurveIDs:           testCurves,
		OwnerIdentityType:  idemixnym.IdentityType,
	})
	require.NoError(t, err)

	for _, curveID := range testCurves {
		configuration, err := configurations.GetSetupConfiguration(curveID)
		require.NoError(t, err)

		env, err := NewOpenPolicyEnv(configuration)
		require.NoError(t, err, "failed to build open-policy env for curve [%s]", curveID)
		require.NotNil(t, env)

		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithOpenPolicyIssueTxID, env.TRWithOpenPolicyIssueRaw)
		assertPositiveFixture(t, env.Engine, env.Ledger, env.TRWithOpenPolicyRedeemTxID, env.TRWithOpenPolicyRedeemRaw)
	}
}

func TestSaveTransferToFile(t *testing.T) {
	metadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{
				ActionID: 1,
				IssueMetadata: &driver.IssueMetadata{
					Issuer: driver.AuditableIdentity{
						Identity:  []byte{1, 2, 3, 4, 5},
						AuditInfo: []byte{6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
					},
					Inputs:       []*driver.IssueInputMetadata{},
					Outputs:      []*driver.IssueOutputMetadata{},
					ExtraSigners: []driver.AuditableIdentity{},
				},
			},
		},
		Application: nil,
	}
	inputs := map[string]*token.Token{
		token.ID{TxId: "a_transaction_id", Index: 1}.String(): {
			Owner:    []byte("an_owner"),
			Type:     "a_type",
			Quantity: "a_quantity",
		},
	}
	e := &Env{
		TRWithTransferTxID:     "tx123",
		TRWithTransferRaw:      []byte{1, 2, 3, 4, 5},
		TRWithTransferMetadata: metadata,
		TRWithTransferInputs:   inputs,
	}
	path := filepath.Join(t.TempDir(), "transfer.json")
	err := e.SaveTransferToFile(path)
	require.NoError(t, err)

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	var payload TestCase
	err = json.Unmarshal(b, &payload)
	require.NoError(t, err)
	require.Equal(t, e.TRWithTransferTxID, payload.TxID)

	decoded, err := base64.StdEncoding.DecodeString(payload.ReqRaw)
	require.NoError(t, err)
	require.Equal(t, e.TRWithTransferRaw, decoded)

	metadataBytes, err := base64.StdEncoding.DecodeString(payload.Metadata)
	require.NoError(t, err)
	decodedMetadata := &driver.TokenRequestMetadata{}
	require.NoError(t, decodedMetadata.FromBytes(metadataBytes))
	require.Equal(t, metadata, decodedMetadata)

	require.Equal(t, inputs, payload.Inputs.Inputs)
}
