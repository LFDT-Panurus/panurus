/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"testing"

	fabtokenv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	upgrademock "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade/mock"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// TestIssueService_UpgradeWithoutWallet checks that Issue returns an error, not a panic,
// when an upgrade request must resolve the issuer identity but no wallet is set.
func TestIssueService_UpgradeWithoutWallet(t *testing.T) {
	ts := setupTest(t)

	verifier := &drivermock.Verifier{}
	verifier.VerifyReturns(nil)
	deserializer := &upgrademock.Deserializer{}
	deserializer.GetOwnerVerifierReturns(verifier, nil)
	upgradeService, err := upgrade.NewService(nil, 16, deserializer, nil, nil, nil)
	require.NoError(t, err)
	ts.service.TokensUpgradeService = upgradeService

	ch, err := upgradeService.NewUpgradeChallenge()
	require.NoError(t, err)
	raw, err := (&actions.Output{Owner: []byte("owner1"), Type: "ABC", Quantity: "10"}).Serialize()
	require.NoError(t, err)
	format32, err := fabtokenv1.SupportedTokenFormat(32)
	require.NoError(t, err)
	ledgerTokens := []token.LedgerToken{{ID: token.ID{TxId: "tx1", Index: 1}, Token: raw, Format: format32}}
	proof, err := (&upgrade.Proof{
		Challenge:  ch,
		Tokens:     ledgerTokens,
		Signatures: []upgrade.Signature{[]byte("a signature")},
	}).Serialize()
	require.NoError(t, err)

	opts := &driver.IssueOptions{
		TokensUpgradeRequest: &driver.TokenUpgradeRequest{Challenge: ch, Tokens: ledgerTokens, Proof: proof},
	}
	require.NotPanics(t, func() {
		_, _, err = ts.service.Issue(t.Context(), nil, "", nil, [][]byte{[]byte("alice")}, opts)
	})
	require.ErrorContains(t, err, "no issuer wallet provided")
}

// TestAuditorService_AuditorCheck_NilMetadata checks that AuditorCheck returns an error, not a panic,
// on nil metadata and on a nil action metadata entry.
func TestAuditorService_AuditorCheck_NilMetadata(t *testing.T) {
	s := &v1.AuditorService{Logger: logging.MustGetLogger()}
	for name, metadata := range map[string]*driver.TokenRequestMetadata{
		"nil metadata":     nil,
		"nil action entry": {Actions: []*driver.ActionMetadataEntry{nil}},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				err = s.AuditorCheck(t.Context(), &driver.TokenRequest{}, metadata, "anchor")
			})
			require.Error(t, err)
		})
	}
}
