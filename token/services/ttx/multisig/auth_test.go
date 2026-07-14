/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests auth.go which provides escrow-based authorization for multisig wallets.
// Tests verify wallet ownership checks, escrow wallet retrieval, and identity validation.
package multisig_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	identityMultisig "github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEscrowAuth(t *testing.T) {
	auth := multisig.NewEscrowAuth(nil)
	require.NotNil(t, auth)
	assert.Nil(t, auth.WalletService)
}

func TestEscrowAuth_AmIAnAuditor(t *testing.T) {
	auth := multisig.NewEscrowAuth(nil)
	assert.False(t, auth.AmIAnAuditor(), "EscrowAuth should never be an auditor")
}

func TestEscrowAuth_Issued(t *testing.T) {
	ctx := t.Context()
	auth := multisig.NewEscrowAuth(nil)

	result := auth.Issued(ctx, nil, nil)
	assert.False(t, result, "EscrowAuth.Issued should always return false")
}

func TestEscrowAuth_OwnerType_InvalidIdentity(t *testing.T) {
	auth := multisig.NewEscrowAuth(nil)

	// Test with invalid (empty) identity
	idType, identity, err := auth.OwnerType([]byte{})
	require.Error(t, err)
	assert.Empty(t, idType)
	assert.Nil(t, identity)
}

func TestEscrowAuth_OwnerType_InvalidFormat(t *testing.T) {
	auth := multisig.NewEscrowAuth(nil)

	// Test with malformed data
	idType, identity, err := auth.OwnerType([]byte("invalid data"))
	require.Error(t, err)
	assert.Empty(t, idType)
	assert.Nil(t, identity)
}

func TestEscrowAuth_OwnerType_ValidMultisigIdentity(t *testing.T) {
	auth := multisig.NewEscrowAuth(nil)

	// Create a valid multisig identity
	multiID, err := identityMultisig.WrapIdentities([]byte("identity1"), []byte("identity2"))
	require.NoError(t, err)

	idType, rawIdentity, err := auth.OwnerType(multiID)
	require.NoError(t, err)
	assert.Equal(t, identityMultisig.Multisig, idType)
	assert.NotNil(t, rawIdentity)

	// Verify we can deserialize the raw identity back
	mi := &identityMultisig.MultiIdentity{}
	err = mi.Deserialize(rawIdentity)
	require.NoError(t, err)
	assert.Len(t, mi.Identities, 2)
}

func TestEscrowAuth_IsMine_InvalidOwner(t *testing.T) {
	ctx := t.Context()
	auth := multisig.NewEscrowAuth(nil)

	tok := &token2.Token{
		Owner:    []byte("invalid owner"),
		Type:     "USD",
		Quantity: "100",
	}

	walletID, ids, isMine := auth.IsMine(ctx, tok)
	assert.Empty(t, walletID)
	assert.Nil(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_NonMultisigOwner(t *testing.T) {
	ctx := t.Context()
	auth := multisig.NewEscrowAuth(nil)

	// Create a typed identity that is not multisig (using a numeric type)
	typedID, err := identity.WrapWithType(identity.Type(1), []byte("some identity"))
	require.NoError(t, err)

	tok := &token2.Token{
		Owner:    typedID,
		Type:     "USD",
		Quantity: "100",
	}

	walletID, ids, isMine := auth.IsMine(ctx, tok)
	assert.Empty(t, walletID)
	assert.Nil(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_InvalidMultisigDeserialization(t *testing.T) {
	ctx := t.Context()
	auth := multisig.NewEscrowAuth(nil)

	// Create a typed identity with multisig type but invalid content
	typedID, err := identity.WrapWithType(identityMultisig.Multisig, []byte("invalid multisig data"))
	require.NoError(t, err)

	tok := &token2.Token{
		Owner:    typedID,
		Type:     "USD",
		Quantity: "100",
	}

	walletID, ids, isMine := auth.IsMine(ctx, tok)
	assert.Empty(t, walletID)
	assert.Nil(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_NoMatchingWallet(t *testing.T) {
	ctx := t.Context()

	// Create a mock wallet service that returns error (no wallet found)
	mockWalletService := &drivermock.WalletService{}
	mockWalletService.OwnerWalletReturns(nil, errors.New("wallet not found"))

	auth := multisig.NewEscrowAuth(mockWalletService)

	// Create a valid multisig identity
	multiID, err := identityMultisig.WrapIdentities([]byte("identity1"), []byte("identity2"))
	require.NoError(t, err)

	tok := &token2.Token{
		Owner:    multiID,
		Type:     "USD",
		Quantity: "100",
	}

	walletID, ids, isMine := auth.IsMine(ctx, tok)
	assert.Empty(t, walletID)
	assert.Empty(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_WithMatchingWallet(t *testing.T) {
	ctx := t.Context()

	// Create a mock wallet service that returns a wallet for the first identity
	mockWalletService := &drivermock.WalletService{}
	mockOwnerWallet := &drivermock.OwnerWallet{}
	mockOwnerWallet.IDReturns("wallet1")

	mockWalletService.OwnerWalletCalls(func(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		if identity, ok := id.(token.Identity); ok && string(identity) == "identity1" {
			return mockOwnerWallet, nil
		}

		return nil, errors.New("wallet not found")
	})

	auth := multisig.NewEscrowAuth(mockWalletService)

	// Create a valid multisig identity
	multiID, err := identityMultisig.WrapIdentities([]byte("identity1"), []byte("identity2"))
	require.NoError(t, err)

	tok := &token2.Token{
		Owner:    multiID,
		Type:     "USD",
		Quantity: "100",
	}

	walletID, ids, isMine := auth.IsMine(ctx, tok)
	assert.Empty(t, walletID)
	assert.Len(t, ids, 1)
	assert.Equal(t, "escrowwallet1", ids[0])
	assert.True(t, isMine)
}

func TestEscrowAuth_IsMine_WithMultipleMatchingWallets(t *testing.T) {
	ctx := t.Context()

	// Create a mock wallet service that returns wallets for both identities
	mockWalletService := &drivermock.WalletService{}
	mockOwnerWallet1 := &drivermock.OwnerWallet{}
	mockOwnerWallet1.IDReturns("wallet1")
	mockOwnerWallet2 := &drivermock.OwnerWallet{}
	mockOwnerWallet2.IDReturns("wallet2")

	mockWalletService.OwnerWalletCalls(func(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		if identity, ok := id.(token.Identity); ok {
			switch string(identity) {
			case "identity1":
				return mockOwnerWallet1, nil
			case "identity2":
				return mockOwnerWallet2, nil
			}
		}

		return nil, errors.New("wallet not found")
	})

	auth := multisig.NewEscrowAuth(mockWalletService)

	// Create a valid multisig identity with two identities
	multiID, err := identityMultisig.WrapIdentities([]byte("identity1"), []byte("identity2"))
	require.NoError(t, err)

	tok := &token2.Token{
		Owner:    multiID,
		Type:     "USD",
		Quantity: "100",
	}

	walletID, ids, isMine := auth.IsMine(ctx, tok)
	assert.Empty(t, walletID)
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, "escrowwallet1")
	assert.Contains(t, ids, "escrowwallet2")
	assert.True(t, isMine)
}
