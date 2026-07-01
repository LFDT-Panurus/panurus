/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalletBasedAuthorization(t *testing.T) {
	logger := &logging.MockLogger{}
	pp := &mock.PublicParameters{}
	ws := &mock.WalletService{}

	auditorId := driver.Identity("auditor-id")
	pp.AuditorsReturns([]driver.Identity{auditorId})

	t.Run("NewTMSAuthorization_IsAuditor", func(t *testing.T) {
		ws.AuditorWalletReturns(&mock.AuditorWallet{}, nil)
		auth := NewTMSAuthorization(logger, pp, ws)
		assert.True(t, auth.AmIAnAuditor())
	})

	t.Run("NewTMSAuthorization_IsNotAuditor", func(t *testing.T) {
		ws.AuditorWalletReturns(nil, errors.New("not auditor"))
		auth := NewTMSAuthorization(logger, pp, ws)
		assert.False(t, auth.AmIAnAuditor())
	})

	t.Run("IsMine_PureAuditor_SkipsOwnerLookup", func(t *testing.T) {
		ws.AuditorWalletReturns(&mock.AuditorWallet{}, nil)
		ws.OwnerWalletIDsReturns(nil, nil)
		auth := NewTMSAuthorization(logger, pp, ws)

		before := ws.OwnerWalletCallCount()
		walletID, ids, mine := auth.IsMine(context.Background(), &token2.Token{Owner: driver.Identity("owner-id")})
		assert.False(t, mine)
		assert.Empty(t, walletID)
		assert.Nil(t, ids)
		assert.Equal(t, before, ws.OwnerWalletCallCount(), "pure auditor must not perform the owner lookup")
	})

	t.Run("IsMine_AuditorWithOwnerWallets_StillResolves", func(t *testing.T) {
		ws.AuditorWalletReturns(&mock.AuditorWallet{}, nil)
		ws.OwnerWalletIDsReturns([]string{"some-owner-wallet"}, nil)
		wallet := &mock.OwnerWallet{}
		wallet.IDReturns("wallet-id")
		ws.OwnerWalletReturns(wallet, nil)
		auth := NewTMSAuthorization(logger, pp, ws)

		walletID, _, mine := auth.IsMine(context.Background(), &token2.Token{Owner: driver.Identity("owner-id")})
		assert.True(t, mine)
		assert.Equal(t, "wallet-id", walletID)
	})

	auth := &WalletBasedAuthorization{
		Logger:           logger,
		PublicParameters: pp,
		WalletService:    ws,
		amIAnAuditor:     true,
	}

	t.Run("IsMine_True", func(t *testing.T) {
		tok := &token2.Token{Owner: driver.Identity("owner-id")}
		wallet := &mock.OwnerWallet{}
		wallet.IDReturns("wallet-id")
		ws.OwnerWalletReturns(wallet, nil)

		walletID, ids, mine := auth.IsMine(context.Background(), tok)
		assert.True(t, mine)
		assert.Equal(t, "wallet-id", walletID)
		assert.Nil(t, ids)
	})

	t.Run("IsMine_False", func(t *testing.T) {
		tok := &token2.Token{Owner: driver.Identity("owner-id")}
		ws.OwnerWalletReturns(nil, errors.New("not mine"))

		walletID, ids, mine := auth.IsMine(context.Background(), tok)
		assert.False(t, mine)
		assert.Empty(t, walletID)
		assert.Nil(t, ids)
	})

	t.Run("AmIAnAuditor", func(t *testing.T) {
		assert.True(t, auth.AmIAnAuditor())
		auth.amIAnAuditor = false
		assert.False(t, auth.AmIAnAuditor())
	})

	t.Run("Issued_True", func(t *testing.T) {
		issuer := driver.Identity("issuer-id")
		tok := &token2.Token{}
		ws.IssuerWalletReturns(&mock.IssuerWallet{}, nil)

		assert.True(t, auth.Issued(context.Background(), issuer, tok))
	})

	t.Run("Issued_False", func(t *testing.T) {
		issuer := driver.Identity("issuer-id")
		tok := &token2.Token{}
		ws.IssuerWalletReturns(nil, errors.New("not issuer"))

		assert.False(t, auth.Issued(context.Background(), issuer, tok))
	})
}

type mockAuth struct {
	isMineWalletID string
	isMineIDs      []string
	isMineBool     bool
	amIAnAuditor   bool
	issued         bool
}

func (m *mockAuth) IsMine(ctx context.Context, tok *token2.Token) (string, []string, bool) {
	return m.isMineWalletID, m.isMineIDs, m.isMineBool
}

func (m *mockAuth) AmIAnAuditor() bool {
	return m.amIAnAuditor
}

func (m *mockAuth) Issued(ctx context.Context, issuer view.Identity, tok *token2.Token) bool {
	return m.issued
}

func TestAuthorizationMultiplexer(t *testing.T) {
	auth1 := &mockAuth{isMineBool: false, amIAnAuditor: false, issued: false}
	auth2 := &mockAuth{isMineWalletID: "w2", isMineIDs: []string{"id2"}, isMineBool: true, amIAnAuditor: true, issued: true}

	mux := NewAuthorizationMultiplexer(auth1, auth2)

	t.Run("IsMine", func(t *testing.T) {
		walletID, ids, mine := mux.IsMine(context.Background(), &token2.Token{})
		assert.True(t, mine)
		assert.Equal(t, "w2", walletID)
		assert.Equal(t, []string{"id2"}, ids)

		muxEmpty := NewAuthorizationMultiplexer(auth1)
		_, _, mine = muxEmpty.IsMine(context.Background(), &token2.Token{})
		assert.False(t, mine)
	})

	t.Run("AmIAnAuditor", func(t *testing.T) {
		assert.True(t, mux.AmIAnAuditor())

		muxEmpty := NewAuthorizationMultiplexer(auth1)
		assert.False(t, muxEmpty.AmIAnAuditor())
	})

	t.Run("Issued", func(t *testing.T) {
		assert.True(t, mux.Issued(context.Background(), nil, &token2.Token{}))

		muxEmpty := NewAuthorizationMultiplexer(auth1)
		assert.False(t, muxEmpty.Issued(context.Background(), nil, &token2.Token{}))
	})

	t.Run("OwnerType", func(t *testing.T) {
		idType := identity.Type(99) // test type
		idRaw := []byte("test-identity")
		raw, err := identity.WrapWithType(idType, idRaw)
		require.NoError(t, err)

		resType, resId, err := mux.OwnerType(raw)
		require.NoError(t, err)
		assert.Equal(t, idType, resType)
		assert.Equal(t, idRaw, resId)

		_, _, err = mux.OwnerType([]byte("invalid"))
		require.Error(t, err)
	})
}
