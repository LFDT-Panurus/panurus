/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
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

	t.Run("RegisterRecipient", func(t *testing.T) {
		w, ip, _ := setup(t)

		// Case 1: nil data
		err := w.RegisterRecipient(t.Context(), nil)
		require.ErrorIs(t, err, wallet.ErrNilRecipientData)

		// Case 2: matching identity and audit info
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  driver.Identity("ownerIdentity"),
			AuditInfo: []byte("audit-info"),
		})
		require.NoError(t, err)

		// Case 3: mismatched identity
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  driver.Identity("other"),
			AuditInfo: []byte("audit-info"),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 4: matching identity, mismatched audit info
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  driver.Identity("ownerIdentity"),
			AuditInfo: []byte("other-audit-info"),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "audit info does not match")

		// Case 5: a well-formed boolpolicy recipient listing the wallet's
		// identity among its components, with the wallet's audit info at that
		// component's entry, is accepted and registered as the rest recipient
		// of a transfer spending policy-owned inputs
		policyID, err := boolpolicy.WrapPolicyIdentity("$0 OR $1",
			token2.Identity("ownerIdentity"), token2.Identity("member2"))
		require.NoError(t, err)
		policyInfo, err := boolpolicy.WrapAuditInfo([][]byte{[]byte("audit-info"), []byte("ai2")})
		require.NoError(t, err)
		acceptedData := &driver.RecipientData{
			Identity:  policyID,
			AuditInfo: policyInfo,
		}
		err = w.RegisterRecipient(t.Context(), acceptedData)
		require.NoError(t, err)
		require.Equal(t, 1, ip.RegisterRecipientDataCallCount())
		_, gotData := ip.RegisterRecipientDataArgsForCall(0)
		assert.Equal(t, acceptedData, gotData)

		// Case 6: policy recipient whose audit info does not cover every
		// component is rejected
		shortInfo, err := boolpolicy.WrapAuditInfo([][]byte{[]byte("ai1")})
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  policyID,
			AuditInfo: shortInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 7: garbage under the policy type tag is rejected
		garbageID, err := identity.WrapWithType(boolpolicy.Policy, identity.Identity("garbage"))
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  garbageID,
			AuditInfo: policyInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 8: a policy the wallet's identity is not a component of is
		// rejected, however well-formed
		foreignID, err := boolpolicy.WrapPolicyIdentity("$0 OR $1",
			token2.Identity("member1"), token2.Identity("member2"))
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  foreignID,
			AuditInfo: policyInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 9: a policy with no components is rejected even when the audit
		// info is empty as well
		emptyInner, err := (&boolpolicy.PolicyIdentity{Policy: "$0"}).Serialize()
		require.NoError(t, err)
		emptyID, err := identity.WrapWithType(boolpolicy.Policy, emptyInner)
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  emptyID,
			AuditInfo: []byte(`{"IdentityAuditInfos":[]}`),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 10: duplicate components are rejected
		dupInner, err := (&boolpolicy.PolicyIdentity{
			Policy:     "$0 OR $1",
			Identities: [][]byte{[]byte("ownerIdentity"), []byte("ownerIdentity")},
		}).Serialize()
		require.NoError(t, err)
		dupID, err := identity.WrapWithType(boolpolicy.Policy, dupInner)
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  dupID,
			AuditInfo: policyInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 11: empty components are rejected
		blankInner, err := (&boolpolicy.PolicyIdentity{
			Policy:     "$0 OR $1",
			Identities: [][]byte{[]byte("ownerIdentity"), {}},
		}).Serialize()
		require.NoError(t, err)
		blankID, err := identity.WrapWithType(boolpolicy.Policy, blankInner)
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  blankID,
			AuditInfo: policyInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 12: audit info carrying unknown fields is rejected
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  policyID,
			AuditInfo: []byte(`{"IdentityAuditInfos":[{"AuditInfo":"YQ=="},{"AuditInfo":"Yg=="}],"Extra":1}`),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 13: a policy at the component fan-out limit is accepted
		limit := driver.DefaultResourceLimits().MaxIdentityComponents
		members := make([]token2.Identity, limit)
		infos := make([][]byte, limit)
		members[0] = token2.Identity("ownerIdentity")
		infos[0] = []byte("audit-info")
		for i := 1; i < limit; i++ {
			members[i] = token2.Identity(fmt.Sprintf("member%d", i))
			infos[i] = fmt.Appendf(nil, "ai%d", i)
		}
		limitID, err := boolpolicy.WrapPolicyIdentity("$0", members...)
		require.NoError(t, err)
		limitInfo, err := boolpolicy.WrapAuditInfo(infos)
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  limitID,
			AuditInfo: limitInfo,
		})
		require.NoError(t, err)

		// Case 14: one component above the fan-out limit is rejected
		overRaw := make([][]byte, limit+1)
		overInfos := make([][]byte, limit+1)
		overRaw[0] = []byte("ownerIdentity")
		overInfos[0] = []byte("ai0")
		for i := 1; i <= limit; i++ {
			overRaw[i] = fmt.Appendf(nil, "member%d", i)
			overInfos[i] = fmt.Appendf(nil, "ai%d", i)
		}
		overInner, err := (&boolpolicy.PolicyIdentity{Policy: "$0", Identities: overRaw}).Serialize()
		require.NoError(t, err)
		overID, err := identity.WrapWithType(boolpolicy.Policy, overInner)
		require.NoError(t, err)
		overInfo, err := boolpolicy.WrapAuditInfo(overInfos)
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  overID,
			AuditInfo: overInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 15: the fan-out bound follows the limits carried by the
		// context, not just the defaults — the same two-component policy is
		// rejected under a limit of 1 and accepted under a limit of 2
		tightCtx := driver.WithIdentityNestingLimits(t.Context(), 0, 1)
		err = w.RegisterRecipient(tightCtx, &driver.RecipientData{
			Identity:  policyID,
			AuditInfo: policyInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		exactCtx := driver.WithIdentityNestingLimits(t.Context(), 0, 2)
		err = w.RegisterRecipient(exactCtx, &driver.RecipientData{
			Identity:  policyID,
			AuditInfo: policyInfo,
		})
		require.NoError(t, err)

		// Case 16: an empty policy expression is rejected — no verifier can
		// ever be built for it, so the rest would be unspendable
		noPolicyInner, err := (&boolpolicy.PolicyIdentity{
			Identities: [][]byte{[]byte("ownerIdentity"), []byte("member2")},
		}).Serialize()
		require.NoError(t, err)
		noPolicyID, err := identity.WrapWithType(boolpolicy.Policy, noPolicyInner)
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  noPolicyID,
			AuditInfo: policyInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 17: audit info of the right cardinality but carrying a foreign
		// entry at the wallet's own component is rejected
		foreignInfo, err := boolpolicy.WrapAuditInfo([][]byte{[]byte("ai1"), []byte("ai2")})
		require.NoError(t, err)
		err = w.RegisterRecipient(t.Context(), &driver.RecipientData{
			Identity:  policyID,
			AuditInfo: foreignInfo,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong to this wallet")

		// Case 18: a registration failure surfaces to the caller
		ip.RegisterRecipientDataReturns(errors.New("registration error"))
		err = w.RegisterRecipient(t.Context(), acceptedData)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed registering audit info")
		ip.RegisterRecipientDataReturns(nil)
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
		// The wallet pre-provisions recipient data in the background; release it so the
		// test does not leave the goroutine behind.
		t.Cleanup(w.Close)

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

	t.Run("Close stops the recipient data provisioning", func(t *testing.T) {
		// Wait for goroutines released by earlier subtests to exit before counting.
		requireProvisioningGoroutines(t, 0, 2*time.Second)
		w, _, _, _, _ := setup(t)

		// Using the wallet starts the background provisioning goroutine.
		_, err := w.GetRecipientIdentity(t.Context())
		require.NoError(t, err)
		requireProvisioningGoroutines(t, 1, 2*time.Second)

		// Closing the wallet must terminate it. Close is idempotent, so the cleanup
		// registered by setup can still run.
		w.Close()
		requireProvisioningGoroutines(t, 0, 2*time.Second)
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
