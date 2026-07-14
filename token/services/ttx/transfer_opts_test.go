/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests transfer_opts.go which provides transfer-specific option patterns.
// Tests verify proper attribute setting, retrieval, and error handling.
package ttx_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithFSCIssuerIdentity verifies the WithFSCIssuerIdentity option.
func TestWithFSCIssuerIdentity(t *testing.T) {
	issuerIdentity := view.Identity("issuer-identity-123")
	opts := &token.TransferOptions{}

	err := ttx.WithFSCIssuerIdentity(issuerIdentity)(opts)

	require.NoError(t, err)
	require.NotNil(t, opts.Attributes)
	assert.Equal(t, issuerIdentity, opts.Attributes[ttx.IssuerFSCIdentityKey])
}

// TestWithFSCIssuerIdentity_NilAttributes verifies WithFSCIssuerIdentity initializes attributes map.
func TestWithFSCIssuerIdentity_NilAttributes(t *testing.T) {
	issuerIdentity := view.Identity("issuer-identity-456")
	opts := &token.TransferOptions{
		Attributes: nil,
	}

	err := ttx.WithFSCIssuerIdentity(issuerIdentity)(opts)

	require.NoError(t, err)
	require.NotNil(t, opts.Attributes)
	assert.Equal(t, issuerIdentity, opts.Attributes[ttx.IssuerFSCIdentityKey])
}

// TestWithFSCIssuerIdentity_ExistingAttributes verifies WithFSCIssuerIdentity preserves existing attributes.
func TestWithFSCIssuerIdentity_ExistingAttributes(t *testing.T) {
	issuerIdentity := view.Identity("issuer-identity-789")
	opts := &token.TransferOptions{
		Attributes: map[string]any{
			"existing-key": "existing-value",
		},
	}

	err := ttx.WithFSCIssuerIdentity(issuerIdentity)(opts)

	require.NoError(t, err)
	assert.Equal(t, issuerIdentity, opts.Attributes[ttx.IssuerFSCIdentityKey])
	assert.Equal(t, "existing-value", opts.Attributes["existing-key"])
}

// TestGetFSCIssuerIdentityFromOpts_Success verifies successful identity extraction.
func TestGetFSCIssuerIdentityFromOpts_Success(t *testing.T) {
	issuerIdentity := view.Identity("issuer-identity-abc")
	attributes := map[string]any{
		ttx.IssuerFSCIdentityKey: issuerIdentity,
	}

	result, err := ttx.GetFSCIssuerIdentityFromOpts(attributes)

	require.NoError(t, err)
	assert.Equal(t, issuerIdentity, result)
}

// TestGetFSCIssuerIdentityFromOpts_NilAttributes verifies nil attributes returns nil.
func TestGetFSCIssuerIdentityFromOpts_NilAttributes(t *testing.T) {
	result, err := ttx.GetFSCIssuerIdentityFromOpts(nil)

	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestGetFSCIssuerIdentityFromOpts_MissingKey verifies missing key returns nil.
func TestGetFSCIssuerIdentityFromOpts_MissingKey(t *testing.T) {
	attributes := map[string]any{
		"other-key": "other-value",
	}

	result, err := ttx.GetFSCIssuerIdentityFromOpts(attributes)

	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestGetFSCIssuerIdentityFromOpts_WrongType verifies error on wrong type.
func TestGetFSCIssuerIdentityFromOpts_WrongType(t *testing.T) {
	attributes := map[string]any{
		ttx.IssuerFSCIdentityKey: "not-an-identity",
	}

	result, err := ttx.GetFSCIssuerIdentityFromOpts(attributes)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "expected identity")
	assert.Contains(t, err.Error(), "string")
}

// TestGetFSCIssuerIdentityFromOpts_WrongTypeInt verifies error on integer type.
func TestGetFSCIssuerIdentityFromOpts_WrongTypeInt(t *testing.T) {
	attributes := map[string]any{
		ttx.IssuerFSCIdentityKey: 123,
	}

	result, err := ttx.GetFSCIssuerIdentityFromOpts(attributes)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "expected identity")
	assert.Contains(t, err.Error(), "int")
}

// TestWithIssuerPublicParamsPublicKey verifies the WithIssuerPublicParamsPublicKey option.
func TestWithIssuerPublicParamsPublicKey(t *testing.T) {
	publicKey := view.Identity("public-key-123")
	opts := &token.TransferOptions{}

	err := ttx.WithIssuerPublicParamsPublicKey(publicKey)(opts)

	require.NoError(t, err)
	require.NotNil(t, opts.Attributes)
	assert.Equal(t, publicKey, opts.Attributes[ttx.IssuerPublicParamsPublicKey])
}

// TestWithIssuerPublicParamsPublicKey_NilAttributes verifies attribute map initialization.
func TestWithIssuerPublicParamsPublicKey_NilAttributes(t *testing.T) {
	publicKey := view.Identity("public-key-456")
	opts := &token.TransferOptions{
		Attributes: nil,
	}

	err := ttx.WithIssuerPublicParamsPublicKey(publicKey)(opts)

	require.NoError(t, err)
	require.NotNil(t, opts.Attributes)
	assert.Equal(t, publicKey, opts.Attributes[ttx.IssuerPublicParamsPublicKey])
}

// TestWithIssuerPublicParamsPublicKey_ExistingAttributes verifies preservation of existing attributes.
func TestWithIssuerPublicParamsPublicKey_ExistingAttributes(t *testing.T) {
	publicKey := view.Identity("public-key-789")
	opts := &token.TransferOptions{
		Attributes: map[string]any{
			"existing-key": "existing-value",
		},
	}

	err := ttx.WithIssuerPublicParamsPublicKey(publicKey)(opts)

	require.NoError(t, err)
	assert.Equal(t, publicKey, opts.Attributes[ttx.IssuerPublicParamsPublicKey])
	assert.Equal(t, "existing-value", opts.Attributes["existing-key"])
}

// TestGetIssuerPublicParamsPublicKeyFromOpts_Success verifies successful key extraction.
func TestGetIssuerPublicParamsPublicKeyFromOpts_Success(t *testing.T) {
	publicKey := view.Identity("public-key-abc")
	attributes := map[string]any{
		ttx.IssuerPublicParamsPublicKey: publicKey,
	}

	result, err := ttx.GetIssuerPublicParamsPublicKeyFromOpts(attributes)

	require.NoError(t, err)
	assert.Equal(t, publicKey, result)
}

// TestGetIssuerPublicParamsPublicKeyFromOpts_NilAttributes verifies nil attributes returns nil.
func TestGetIssuerPublicParamsPublicKeyFromOpts_NilAttributes(t *testing.T) {
	result, err := ttx.GetIssuerPublicParamsPublicKeyFromOpts(nil)

	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestGetIssuerPublicParamsPublicKeyFromOpts_MissingKey verifies missing key returns nil.
func TestGetIssuerPublicParamsPublicKeyFromOpts_MissingKey(t *testing.T) {
	attributes := map[string]any{
		"other-key": "other-value",
	}

	result, err := ttx.GetIssuerPublicParamsPublicKeyFromOpts(attributes)

	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestGetIssuerPublicParamsPublicKeyFromOpts_WrongType verifies error on wrong type.
func TestGetIssuerPublicParamsPublicKeyFromOpts_WrongType(t *testing.T) {
	attributes := map[string]any{
		ttx.IssuerPublicParamsPublicKey: "not-a-key",
	}

	result, err := ttx.GetIssuerPublicParamsPublicKeyFromOpts(attributes)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "expected signing key")
	assert.Contains(t, err.Error(), "string")
}

// TestGetIssuerPublicParamsPublicKeyFromOpts_WrongTypeStruct verifies error on struct type.
func TestGetIssuerPublicParamsPublicKeyFromOpts_WrongTypeStruct(t *testing.T) {
	attributes := map[string]any{
		ttx.IssuerPublicParamsPublicKey: struct{}{},
	}

	result, err := ttx.GetIssuerPublicParamsPublicKeyFromOpts(attributes)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "expected signing key")
	assert.Contains(t, err.Error(), "struct")
}

// TestTransferOptions_BothAttributes verifies both options can be used together.
func TestTransferOptions_BothAttributes(t *testing.T) {
	issuerIdentity := view.Identity("issuer-identity")
	publicKey := view.Identity("public-key")
	opts := &token.TransferOptions{}

	err := ttx.WithFSCIssuerIdentity(issuerIdentity)(opts)
	require.NoError(t, err)

	err = ttx.WithIssuerPublicParamsPublicKey(publicKey)(opts)
	require.NoError(t, err)

	// Verify both are set
	assert.Equal(t, issuerIdentity, opts.Attributes[ttx.IssuerFSCIdentityKey])
	assert.Equal(t, publicKey, opts.Attributes[ttx.IssuerPublicParamsPublicKey])

	// Verify both can be retrieved
	retrievedIdentity, err := ttx.GetFSCIssuerIdentityFromOpts(opts.Attributes)
	require.NoError(t, err)
	assert.Equal(t, issuerIdentity, retrievedIdentity)

	retrievedKey, err := ttx.GetIssuerPublicParamsPublicKeyFromOpts(opts.Attributes)
	require.NoError(t, err)
	assert.Equal(t, publicKey, retrievedKey)
}

// TestIssuerFSCIdentityKey_Value verifies the constant value.
func TestIssuerFSCIdentityKey_Value(t *testing.T) {
	assert.Equal(t, "IssuerFSCIdentityKey", ttx.IssuerFSCIdentityKey)
}

// TestIssuerPublicParamsPublicKey_Value verifies the constant value.
func TestIssuerPublicParamsPublicKey_Value(t *testing.T) {
	assert.Equal(t, "IssuerPublicParamsPublicKey", ttx.IssuerPublicParamsPublicKey)
}
