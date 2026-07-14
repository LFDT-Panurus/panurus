/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTokensService_Deobfuscate verifies Deobfuscate delegates to underlying service
func TestTokensService_Deobfuscate(t *testing.T) {
	mockTS := &mock.TokensService{}
	ts := &TokensService{ts: mockTS}

	ctx := context.Background()
	output := []byte("output")
	outputMetadata := []byte("metadata")

	expectedToken := &token.Token{Type: "USD", Quantity: "100"}
	expectedIssuer := Identity([]byte("issuer"))
	expectedOwners := []Identity{[]byte("owner1")}
	expectedFormat := token.Format("format1")

	mockTS.DeobfuscateReturns(expectedToken, expectedIssuer, expectedOwners, expectedFormat, nil)

	tok, issuer, owners, format, err := ts.Deobfuscate(ctx, output, outputMetadata)

	require.NoError(t, err)
	assert.Equal(t, expectedToken, tok)
	assert.Equal(t, expectedIssuer, issuer)
	assert.Equal(t, expectedOwners, owners)
	assert.Equal(t, expectedFormat, format)
	assert.Equal(t, 1, mockTS.DeobfuscateCallCount())
}

// TestTokensService_Deobfuscate_Error verifies error handling
func TestTokensService_Deobfuscate_Error(t *testing.T) {
	mockTS := &mock.TokensService{}
	ts := &TokensService{ts: mockTS}

	ctx := context.Background()
	expectedErr := errors.New("deobfuscate error")
	mockTS.DeobfuscateReturns(nil, nil, nil, "", expectedErr)

	tok, issuer, owners, format, err := ts.Deobfuscate(ctx, []byte("output"), []byte("metadata"))

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, tok)
	assert.Nil(t, issuer)
	assert.Nil(t, owners)
	assert.Equal(t, token.Format(""), format)
}

// TestTokensService_NewUpgradeChallenge verifies NewUpgradeChallenge delegates correctly
func TestTokensService_NewUpgradeChallenge(t *testing.T) {
	mockTUS := &mock.TokensUpgradeService{}
	ts := &TokensService{tus: mockTUS}

	expectedChallenge := []byte("challenge")
	mockTUS.NewUpgradeChallengeReturns(expectedChallenge, nil)

	challenge, err := ts.NewUpgradeChallenge()

	require.NoError(t, err)
	assert.Equal(t, expectedChallenge, challenge)
	assert.Equal(t, 1, mockTUS.NewUpgradeChallengeCallCount())
}

// TestTokensService_NewUpgradeChallenge_Error verifies error handling
func TestTokensService_NewUpgradeChallenge_Error(t *testing.T) {
	mockTUS := &mock.TokensUpgradeService{}
	ts := &TokensService{tus: mockTUS}

	expectedErr := errors.New("challenge error")
	mockTUS.NewUpgradeChallengeReturns(nil, expectedErr)

	challenge, err := ts.NewUpgradeChallenge()

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, challenge)
}

// TestTokensService_GenUpgradeProof verifies GenUpgradeProof delegates correctly
func TestTokensService_GenUpgradeProof(t *testing.T) {
	mockTUS := &mock.TokensUpgradeService{}
	ts := &TokensService{tus: mockTUS}

	ctx := context.Background()
	id := []byte("challenge-id")
	tokens := []token.LedgerToken{
		{ID: token.ID{TxId: "tx1", Index: 0}},
	}
	expectedProof := []byte("proof")

	mockTUS.GenUpgradeProofReturns(expectedProof, nil)

	proof, err := ts.GenUpgradeProof(ctx, id, tokens)

	require.NoError(t, err)
	assert.Equal(t, expectedProof, proof)
	assert.Equal(t, 1, mockTUS.GenUpgradeProofCallCount())

	// Verify arguments
	_, gotID, gotTokens, gotExtra := mockTUS.GenUpgradeProofArgsForCall(0)
	assert.Equal(t, id, gotID)
	assert.Equal(t, tokens, gotTokens)
	assert.Nil(t, gotExtra)
}

// TestTokensService_GenUpgradeProof_Error verifies error handling
func TestTokensService_GenUpgradeProof_Error(t *testing.T) {
	mockTUS := &mock.TokensUpgradeService{}
	ts := &TokensService{tus: mockTUS}

	ctx := context.Background()
	expectedErr := errors.New("proof error")
	mockTUS.GenUpgradeProofReturns(nil, expectedErr)

	proof, err := ts.GenUpgradeProof(ctx, []byte("id"), []token.LedgerToken{})

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, proof)
}

// TestTokensService_SupportedTokenFormats verifies SupportedTokenFormats delegates correctly
func TestTokensService_SupportedTokenFormats(t *testing.T) {
	mockTS := &mock.TokensService{}
	ts := &TokensService{ts: mockTS}

	expectedFormats := []token.Format{"format1", "format2"}
	mockTS.SupportedTokenFormatsReturns(expectedFormats)

	formats := ts.SupportedTokenFormats()

	assert.Equal(t, expectedFormats, formats)
	assert.Equal(t, 1, mockTS.SupportedTokenFormatsCallCount())
}

// TestTokensService_SupportedTokenFormats_Empty verifies empty formats list
func TestTokensService_SupportedTokenFormats_Empty(t *testing.T) {
	mockTS := &mock.TokensService{}
	ts := &TokensService{ts: mockTS}

	mockTS.SupportedTokenFormatsReturns([]token.Format{})

	formats := ts.SupportedTokenFormats()

	assert.Empty(t, formats)
}
