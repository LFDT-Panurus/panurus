/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	identityboolpolicy "github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	identitymultisig "github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const boundWalletID = "baseWallet"

// lookupBytes normalises a wallet lookup id to raw bytes. The type-gated
// authorizations pass typed identities (view.Identity / token.Identity) while
// the wallet-based one passes the raw owner bytes.
func lookupBytes(id driver.WalletLookupID) ([]byte, bool) {
	switch v := id.(type) {
	case []byte:
		return v, true
	case driver.Identity:
		return v, true
	default:
		return nil, false
	}
}

// ownerWallet returns a mock owner wallet with the given id.
func ownerWallet(id string) *drivermock.OwnerWallet {
	w := &drivermock.OwnerWallet{}
	w.IDReturns(id)

	return w
}

// stubWalletService returns a wallet service whose OwnerWallet resolves the
// identities in members to the associated wallet ids and, when bound is not
// nil, the full bound owner bytes to boundWalletID (simulating a composite
// identity registered with a wallet). Every other lookup fails.
func stubWalletService(members map[string]string, bound []byte) *drivermock.WalletService {
	ws := &drivermock.WalletService{}
	ws.OwnerWalletCalls(func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		raw, ok := lookupBytes(id)
		if !ok {
			return nil, errors.New("not found")
		}
		if bound != nil && bytes.Equal(raw, bound) {
			return ownerWallet(boundWalletID), nil
		}
		if wID, ok := members[string(raw)]; ok {
			return ownerWallet(wID), nil
		}

		return nil, errors.New("not found")
	})

	return ws
}

// htlcOwner returns an htlc script owner with the given sender and recipient.
func htlcOwner(t *testing.T, sender, recipient string) []byte {
	t.Helper()
	raw, err := json.Marshal(htlc.Script{Sender: []byte(sender), Recipient: []byte(recipient)})
	require.NoError(t, err)

	return marshal.EncodeIdentity(driver.HTLCScriptIdentityType, raw)
}

// multisigOwner returns a multisig owner over the given identities.
func multisigOwner(t *testing.T, ids ...string) []byte {
	t.Helper()
	tids := make([]token.Identity, len(ids))
	for i, id := range ids {
		tids[i] = token.Identity(id)
	}
	owner, err := identitymultisig.WrapIdentities(tids...)
	require.NoError(t, err)

	return owner
}

// policyOwner returns a boolpolicy owner over the given policy and identities.
func policyOwner(t *testing.T, policy string, ids ...string) []byte {
	t.Helper()
	tids := make([]token.Identity, len(ids))
	for i, id := range ids {
		tids[i] = token.Identity(id)
	}
	owner, err := identityboolpolicy.WrapPolicyIdentity(policy, tids...)
	require.NoError(t, err)

	return owner
}

// newAuth returns the zkatsnark-wired authorization chain over ws, with no auditors.
func newAuth(ws driver.WalletService) *common.AuthorizationMultiplexer {
	pp := &drivermock.PublicParameters{}
	pp.AuditorsReturns(nil)

	return zkatsnarkdriver.NewAuthorization(logging.MustGetLogger(), pp, ws)
}

// TestAuthorization_EscrowTokensUseTypeGatedAuth checks that the authorization
// chain wired by the zkatsnark driver attributes escrow-typed tokens (htlc,
// multisig, boolpolicy) through their type-gated authorization, even when the
// composite owner identity is bound to a wallet, rather than letting the
// wallet-based catch-all claim them under the bound wallet id.
func TestAuthorization_EscrowTokensUseTypeGatedAuth(t *testing.T) {
	members := map[string]string{"alice": "w1", "bob": "w2"}

	tests := []struct {
		name    string
		owner   []byte
		members map[string]string
		wantIDs []string
	}{
		{
			name:    "htlc sender",
			owner:   htlcOwner(t, "alice", "carol"),
			members: members,
			wantIDs: []string{"htlc.senderw1"},
		},
		{
			name:    "htlc sender and recipient",
			owner:   htlcOwner(t, "alice", "bob"),
			members: members,
			wantIDs: []string{"htlc.senderw1", "htlc.recipientw2"},
		},
		{
			name:    "multisig",
			owner:   multisigOwner(t, "alice", "carol"),
			members: members,
			wantIDs: []string{"escroww1"},
		},
		{
			name:    "boolpolicy",
			owner:   policyOwner(t, "$0", "alice"),
			members: members,
			wantIDs: []string{"policyw1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			owner := tc.owner
			tok := &token2.Token{Owner: owner, Type: "USD", Quantity: "0x64"}
			auth := newAuth(stubWalletService(tc.members, owner))

			walletID, ids, isMine := auth.IsMine(t.Context(), tok)
			assert.True(t, isMine)
			assert.Empty(t, walletID)
			assert.Equal(t, tc.wantIDs, ids)
		})
	}
}

// TestAuthorization_NonEscrowAndFallthrough covers the cases the reorder must
// not change: plain owners still resolve through the wallet-based
// authorization, and escrow tokens with no local member fall through to it.
func TestAuthorization_NonEscrowAndFallthrough(t *testing.T) {
	t.Run("plain owner resolves through the wallet-based auth", func(t *testing.T) {
		owner := marshal.EncodeIdentity(driver.X509IdentityType, []byte("alice"))
		auth := newAuth(stubWalletService(map[string]string{string(owner): "w1"}, nil))

		walletID, ids, isMine := auth.IsMine(t.Context(), &token2.Token{Owner: owner, Type: "USD", Quantity: "0x64"})
		assert.True(t, isMine)
		assert.Equal(t, "w1", walletID)
		assert.Nil(t, ids)
	})

	t.Run("escrow token with unknown members and unbound owner", func(t *testing.T) {
		owner := multisigOwner(t, "carol", "dave")
		auth := newAuth(stubWalletService(map[string]string{"alice": "w1"}, nil))

		walletID, ids, isMine := auth.IsMine(t.Context(), &token2.Token{Owner: owner, Type: "USD", Quantity: "0x64"})
		assert.False(t, isMine)
		assert.Empty(t, walletID)
		assert.Nil(t, ids)
	})

	// Shared common.NewStandardAuthorization semantics (identical in zkatdlog
	// and fabtoken), not zkatsnark-specific: when no member of an escrow token
	// is local, the type-gated auths decline it and the wallet-based catch-all
	// may still claim it if the whole composite identity is bound to a wallet.
	t.Run("escrow token with unknown members but bound owner", func(t *testing.T) {
		owner := multisigOwner(t, "carol", "dave")
		auth := newAuth(stubWalletService(map[string]string{"alice": "w1"}, owner))

		walletID, ids, isMine := auth.IsMine(t.Context(), &token2.Token{Owner: owner, Type: "USD", Quantity: "0x64"})
		assert.True(t, isMine)
		assert.Equal(t, boundWalletID, walletID)
		assert.Nil(t, ids)
	})
}

// TestAuthorization_IssuerAndAuditor checks that issuer and auditor checks
// still go through the wallet-based authorization.
func TestAuthorization_IssuerAndAuditor(t *testing.T) {
	tok := &token2.Token{Owner: []byte("owner"), Type: "USD", Quantity: "0x64"}

	ws := stubWalletService(nil, nil)
	ws.IssuerWalletReturns(nil, errors.New("not an issuer"))
	auth := newAuth(ws)
	assert.False(t, auth.AmIAnAuditor())
	assert.False(t, auth.Issued(t.Context(), token.Identity("issuer"), tok))

	ws = stubWalletService(nil, nil)
	ws.IssuerWalletReturns(&drivermock.IssuerWallet{}, nil)
	auth = newAuth(ws)
	assert.True(t, auth.Issued(t.Context(), token.Identity("issuer"), tok))
}
