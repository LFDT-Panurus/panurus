/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"errors"
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditorWallet(t *testing.T) {
	t.Run("Creation and Basics", func(t *testing.T) {
		signer := &mock.Signer{}
		id := driver.Identity("auditorIdentity")
		w := role.NewAuditorWallet("w1", id, signer)

		require.NotNil(t, w)
		assert.Equal(t, "w1", w.ID())

		// Identity check
		gotID, err := w.GetAuditorIdentity()
		require.NoError(t, err)
		assert.Equal(t, id, gotID)

		// Contains
		assert.True(t, w.Contains(t.Context(), id))
		assert.False(t, w.Contains(t.Context(), driver.Identity("other")))

		// ContainsToken
		assert.True(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: id}))
		assert.False(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: driver.Identity("other")}))

		// GetSigner
		s, err := w.GetSigner(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, signer, s)

		_, err = w.GetSigner(t.Context(), driver.Identity("other"))
		require.Error(t, err)
	})
}

func TestCertifierWallet(t *testing.T) {
	t.Run("Creation and Basics", func(t *testing.T) {
		signer := &mock.Signer{}
		id := driver.Identity("certifierIdentity")
		w := role.NewCertifierWallet("w1", id, signer)

		require.NotNil(t, w)
		assert.Equal(t, "w1", w.ID())

		// Identity check
		gotID, err := w.GetCertifierIdentity()
		require.NoError(t, err)
		assert.Equal(t, id, gotID)

		// Contains
		assert.True(t, w.Contains(t.Context(), id))
		assert.False(t, w.Contains(t.Context(), driver.Identity("other")))

		// ContainsToken
		assert.True(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: id}))
		assert.False(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: driver.Identity("other")}))

		// GetSigner
		s, err := w.GetSigner(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, signer, s)

		_, err = w.GetSigner(t.Context(), driver.Identity("other"))
		require.Error(t, err)
	})
}

func TestIssuerWallet(t *testing.T) {
	setup := func() (*role.IssuerWallet, *mock.IssuerTokenVault, *mock.Signer) {
		tv := &mock.IssuerTokenVault{}
		signer := &mock.Signer{}
		logger := logging.MustGetLogger("test")
		id := driver.Identity("issuerIdentity")
		w := role.NewIssuerWallet(logger, tv, "w1", id, signer)

		return w, tv, signer
	}

	t.Run("Creation and Basics", func(t *testing.T) {
		w, _, signer := setup()
		id := driver.Identity("issuerIdentity")

		require.NotNil(t, w)
		assert.Equal(t, "w1", w.ID())

		// Identity check
		gotID, err := w.GetIssuerIdentity(token.Type("ANY"))
		require.NoError(t, err)
		assert.Equal(t, id, gotID)

		// Contains
		assert.True(t, w.Contains(t.Context(), id))
		assert.False(t, w.Contains(t.Context(), driver.Identity("other")))

		// ContainsToken
		assert.True(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: id}))
		assert.False(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: driver.Identity("other")}))

		// GetSigner
		s, err := w.GetSigner(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, signer, s)

		_, err = w.GetSigner(t.Context(), driver.Identity("other"))
		require.Error(t, err)
	})

	t.Run("HistoryTokens", func(t *testing.T) {
		w, tv, _ := setup()
		id := driver.Identity("issuerIdentity")

		// Prepare mock data
		tokens := &token.IssuedTokens{
			Tokens: []*token.IssuedToken{
				{Type: "T1", Quantity: "10", Issuer: id},
				{Type: "T2", Quantity: "20", Issuer: id},
				{Type: "T1", Quantity: "30", Issuer: driver.Identity("other")},
			},
		}
		tv.ListHistoryIssuedTokensReturns(tokens, nil)

		// Case 1: All types
		res, err := w.HistoryTokens(t.Context(), &driver.ListTokensOptions{})
		require.NoError(t, err)
		assert.Len(t, res.Tokens, 2)

		// Case 2: Filter by type
		res, err = w.HistoryTokens(t.Context(), &driver.ListTokensOptions{TokenType: "T1"})
		require.NoError(t, err)
		assert.Len(t, res.Tokens, 1)
		assert.Equal(t, token.Type("T1"), res.Tokens[0].Type)

		// Case 3: Error from vault
		tv.ListHistoryIssuedTokensReturns(nil, errors.New("vault error"))
		_, err = w.HistoryTokens(t.Context(), &driver.ListTokensOptions{})
		require.Error(t, err)
	})

	t.Run("Balances", func(t *testing.T) {
		w, tv, _ := setup()

		tv.IssuedBalanceReturns(big.NewInt(100), nil)
		tv.RedeemedBalanceReturns(big.NewInt(30), nil)

		issued, err := w.IssuedBalance(t.Context(), &driver.IssuerBalanceOptions{})
		require.NoError(t, err)
		assert.Equal(t, int64(100), issued.Int64())

		redeemed, err := w.RedeemedBalance(t.Context(), &driver.IssuerBalanceOptions{TokenType: "T1"})
		require.NoError(t, err)
		assert.Equal(t, int64(30), redeemed.Int64())

		// net balance = issued - redeemed = 70
		net, err := w.Balance(t.Context(), &driver.IssuerBalanceOptions{})
		require.NoError(t, err)
		assert.Equal(t, int64(70), net.Int64())

		// options are forwarded to the vault query
		_, err = w.IssuedBalance(t.Context(), &driver.IssuerBalanceOptions{TokenType: "T2"})
		require.NoError(t, err)
		_, gotOpts := tv.IssuedBalanceArgsForCall(tv.IssuedBalanceCallCount() - 1)
		assert.Equal(t, token.Type("T2"), gotOpts.TokenType)

		// error propagation
		tv.IssuedBalanceReturns(nil, errors.New("vault error"))
		_, err = w.Balance(t.Context(), &driver.IssuerBalanceOptions{})
		require.Error(t, err)
	})
}

func TestLongTermOwnerWallet(t *testing.T) {
	setup := func(t *testing.T) (*role.LongTermOwnerWallet, *mock.IdentityProvider, *mock.OwnerTokenVault) {
		t.Helper()
		ip := &mock.IdentityProvider{}
		tv := &mock.OwnerTokenVault{}
		info := &mockIdentityInfo{id: "ownerIdentity"}

		w, err := role.NewLongTermOwnerWallet(t.Context(), ip, tv, "w1", info)
		require.NoError(t, err)

		return w, ip, tv
	}

	t.Run("Creation and Basics", func(t *testing.T) {
		w, _, _ := setup(t)
		id := driver.Identity("ownerIdentity")

		require.NotNil(t, w)
		assert.Equal(t, "w1", w.ID())

		assert.True(t, w.Contains(t.Context(), id))
		assert.False(t, w.Contains(t.Context(), driver.Identity("other")))

		assert.True(t, w.ContainsToken(t.Context(), &token.UnspentToken{Owner: id}))

		recipient, err := w.GetRecipientIdentity(t.Context())
		require.NoError(t, err)
		assert.Equal(t, id, recipient)

		data, err := w.GetRecipientData(t.Context())
		require.NoError(t, err)
		assert.Equal(t, id, data.Identity)
		assert.Equal(t, []byte("audit-info"), data.AuditInfo) // mockIdentityInfo returns nil audit info
	})

	t.Run("ListTokens and Balance", func(t *testing.T) {
		w, _, tv := setup(t)

		// Setup mock iterator
		it := &mock.UnspentTokensIterator{}
		// We can't easily fake the Next calls logic with simple Returns unless we use Callbacks or careful Returns.
		// For simplicity, let's use ReturnsOnCall if counterfeiter supports simpler behavior, or stub it.
		// Counterfeiter Stub allows full function replacement.

		tokensList := []*token.UnspentToken{
			{Id: token.ID{TxId: "tx1", Index: 0}, Type: "T1", Quantity: "10"},
			{Id: token.ID{TxId: "tx2", Index: 0}, Type: "T1", Quantity: "20"},
		}

		idx := 0
		it.NextStub = func() (*token.UnspentToken, error) {
			if idx >= len(tokensList) {
				return nil, nil
			}
			t := tokensList[idx]
			idx++

			return t, nil
		}

		tv.UnspentTokensIteratorByReturns(it, nil)
		tv.BalanceReturns(big.NewInt(30), nil)

		// ListTokens
		tokens, err := w.ListTokens(t.Context(), &driver.ListTokensOptions{TokenType: "T1"})
		require.NoError(t, err)
		assert.Len(t, tokens.Tokens, 2)

		// ListTokensIterator
		tv.UnspentTokensIteratorByReturns(it, nil)
		itRet, err := w.ListTokensIterator(t.Context(), &driver.ListTokensOptions{})
		require.NoError(t, err)
		assert.Equal(t, it, itRet)

		// Balance
		bal, err := w.Balance(t.Context(), &driver.ListTokensOptions{})
		require.NoError(t, err)
		assert.Equal(t, big.NewInt(30), bal)
	})

	t.Run("GetSigner", func(t *testing.T) {
		w, ip, _ := setup(t)
		signer := &mock.Signer{}
		ip.GetSignerReturns(signer, nil)

		s, err := w.GetSigner(t.Context(), driver.Identity("ownerIdentity"))
		require.NoError(t, err)
		assert.Equal(t, signer, s)

		_, err = w.GetSigner(t.Context(), driver.Identity("other"))
		require.Error(t, err)
	})
}

func TestAnonymousOwnerWallet(t *testing.T) {
	setup := func(t *testing.T) (*role.AnonymousOwnerWallet, *mock.IdentityProvider, *mock.OwnerTokenVault, *mock.IdentitySupport, *mock.Deserializer) {
		t.Helper()
		ip := &mock.IdentityProvider{}
		tv := &mock.OwnerTokenVault{}
		info := &mockIdentityInfo{id: "ownerIdentity"}
		is := &mock.IdentitySupport{}
		des := &mock.Deserializer{}
		logger := logging.MustGetLogger("test")

		// Create wallet
		w, err := role.NewAnonymousOwnerWallet(logger, ip, tv, des, is, "w1", info, 10, &disabled.Provider{})
		require.NoError(t, err)

		return w, ip, tv, is, des
	}

	t.Run("Creation and Basics", func(t *testing.T) {
		w, _, _, reg, _ := setup(t)

		assert.NotNil(t, w)
		assert.Equal(t, "w1", w.ID())

		// Contains delegates to registry
		reg.ContainsIdentityReturns(true)
		assert.True(t, w.Contains(t.Context(), driver.Identity("someID")))

		reg.ContainsIdentityReturns(false)
		assert.False(t, w.Contains(t.Context(), driver.Identity("other")))
	})

	t.Run("GetRecipientIdentity", func(t *testing.T) {
		w, _, _, _, _ := setup(t)

		// First call should generate new identity and register it
		id, err := w.GetRecipientIdentity(t.Context())
		require.NoError(t, err)
		assert.Equal(t, driver.Identity("ownerIdentity"), id)
	})

	t.Run("RegisterRecipient", func(t *testing.T) {
		w, ip, _, reg, des := setup(t)

		data := &driver.RecipientData{
			Identity:  driver.Identity("newIdentity"),
			AuditInfo: []byte("audit"),
		}

		// Case 1: Success
		// Deserialize OwnerVerifier defaults to nil, error nil => success verification
		des.MatchIdentityReturns(nil)
		ip.RegisterRecipientDataReturns(nil)
		reg.BindIdentityReturns(nil)

		err := w.RegisterRecipient(t.Context(), data)
		require.NoError(t, err)

		// Case 2: MatchIdentity failure
		des.MatchIdentityReturns(errors.New("match error"))
		err = w.RegisterRecipient(t.Context(), data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to match identity")
		des.MatchIdentityReturns(nil)

		// Case 3: RegisterRecipientData failure
		ip.RegisterRecipientDataReturns(errors.New("reg recipient data error"))
		err = w.RegisterRecipient(t.Context(), data)
		require.Error(t, err)
		ip.RegisterRecipientDataReturns(nil)

		// Case 4: BindIdentity failure
		reg.BindIdentityReturns(errors.New("bind error"))
		err = w.RegisterRecipient(t.Context(), data)
		require.Error(t, err)
	})

	t.Run("GetSigner", func(t *testing.T) {
		w, ip, _, reg, _ := setup(t)
		signer := &mock.Signer{}
		ip.GetSignerReturns(signer, nil)

		reg.ContainsIdentityReturns(true)
		s, err := w.GetSigner(t.Context(), driver.Identity("someID"))
		require.NoError(t, err)
		assert.Equal(t, signer, s)

		reg.ContainsIdentityReturns(false)
		_, err = w.GetSigner(t.Context(), driver.Identity("other"))
		require.Error(t, err)
	})
}
