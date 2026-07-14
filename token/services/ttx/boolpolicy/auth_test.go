/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// auth_test.go tests EscrowAuth, which implements policy-identity-based
// ownership checks (IsMine) for the boolpolicy escrow wallet.
package boolpolicy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	identityboolpolicy "github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePolicyToken wraps a valid policy identity into a token.Token.
func makePolicyToken(t *testing.T, policy string, ids ...[]byte) *token2.Token {
	t.Helper()
	rawIDs := make([]token.Identity, len(ids))
	for i, id := range ids {
		rawIDs[i] = id
	}
	owner, err := identityboolpolicy.WrapPolicyIdentity(policy, rawIDs...)
	require.NoError(t, err)

	return &token2.Token{Owner: owner, Type: "USD", Quantity: "100"}
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewEscrowAuth(t *testing.T) {
	auth := boolpolicy.NewEscrowAuth(nil)
	require.NotNil(t, auth)
	assert.Nil(t, auth.WalletService)
}

// ---------------------------------------------------------------------------
// AmIAnAuditor / Issued (always false)
// ---------------------------------------------------------------------------

func TestEscrowAuth_AmIAnAuditor(t *testing.T) {
	auth := boolpolicy.NewEscrowAuth(nil)
	assert.False(t, auth.AmIAnAuditor(), "policy EscrowAuth is never an auditor")
}

func TestEscrowAuth_Issued(t *testing.T) {
	auth := boolpolicy.NewEscrowAuth(nil)
	assert.False(t, auth.Issued(t.Context(), nil, nil), "policy EscrowAuth.Issued should always return false")
}

// ---------------------------------------------------------------------------
// IsMine — negative cases
// ---------------------------------------------------------------------------

func TestEscrowAuth_IsMine_InvalidOwnerBytes(t *testing.T) {
	auth := boolpolicy.NewEscrowAuth(nil)
	tok := &token2.Token{Owner: []byte("not-a-typed-identity"), Type: "USD", Quantity: "1"}

	walletID, ids, isMine := auth.IsMine(t.Context(), tok)
	assert.Empty(t, walletID)
	assert.Nil(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_NonPolicyOwner(t *testing.T) {
	auth := boolpolicy.NewEscrowAuth(nil)
	// Type 1 is not the policy type (6).
	typedID, err := identity.WrapWithType(identity.Type(1), []byte("some identity"))
	require.NoError(t, err)

	tok := &token2.Token{Owner: typedID, Type: "USD", Quantity: "1"}
	walletID, ids, isMine := auth.IsMine(t.Context(), tok)
	assert.Empty(t, walletID)
	assert.Nil(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_InvalidPolicyDeserialization(t *testing.T) {
	auth := boolpolicy.NewEscrowAuth(nil)
	// Wrap garbage bytes with the policy type tag.
	typedID, err := identity.WrapWithType(identityboolpolicy.Policy, []byte("garbage"))
	require.NoError(t, err)

	tok := &token2.Token{Owner: typedID, Type: "USD", Quantity: "1"}
	walletID, ids, isMine := auth.IsMine(t.Context(), tok)
	assert.Empty(t, walletID)
	assert.Nil(t, ids)
	assert.False(t, isMine)
}

func TestEscrowAuth_IsMine_NoMatchingWallet(t *testing.T) {
	mockWS := &drivermock.WalletService{}
	mockWS.OwnerWalletReturns(nil, errors.New("not found"))
	auth := boolpolicy.NewEscrowAuth(mockWS)

	tok := makePolicyToken(t, "$0 OR $1", []byte("alice"), []byte("bob"))
	walletID, ids, isMine := auth.IsMine(t.Context(), tok)
	assert.Empty(t, walletID)
	assert.Empty(t, ids)
	assert.False(t, isMine)
}

// ---------------------------------------------------------------------------
// IsMine — positive cases
// ---------------------------------------------------------------------------

func TestEscrowAuth_IsMine_OneComponentMine(t *testing.T) {
	mockWS := &drivermock.WalletService{}
	mockOW := &drivermock.OwnerWallet{}
	mockOW.IDReturns("w1")

	mockWS.OwnerWalletCalls(func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		if raw, ok := id.([]byte); ok && string(raw) == "alice" {
			return mockOW, nil
		}

		return nil, errors.New("not found")
	})

	auth := boolpolicy.NewEscrowAuth(mockWS)
	tok := makePolicyToken(t, "$0 OR $1", []byte("alice"), []byte("bob"))

	walletID, ids, isMine := auth.IsMine(t.Context(), tok)
	assert.Empty(t, walletID)
	require.Len(t, ids, 1)
	assert.Equal(t, "policyw1", ids[0])
	assert.True(t, isMine)
}

func TestEscrowAuth_IsMine_AllComponentsMine(t *testing.T) {
	mockWS := &drivermock.WalletService{}
	mockOW1 := &drivermock.OwnerWallet{}
	mockOW1.IDReturns("w1")
	mockOW2 := &drivermock.OwnerWallet{}
	mockOW2.IDReturns("w2")

	mockWS.OwnerWalletCalls(func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		if raw, ok := id.([]byte); ok {
			switch string(raw) {
			case "alice":
				return mockOW1, nil
			case "bob":
				return mockOW2, nil
			}
		}

		return nil, errors.New("not found")
	})

	auth := boolpolicy.NewEscrowAuth(mockWS)
	tok := makePolicyToken(t, "$0 AND $1", []byte("alice"), []byte("bob"))

	walletID, ids, isMine := auth.IsMine(t.Context(), tok)
	assert.Empty(t, walletID)
	require.Len(t, ids, 2)
	assert.Contains(t, ids, "policyw1")
	assert.Contains(t, ids, "policyw2")
	assert.True(t, isMine)
}
