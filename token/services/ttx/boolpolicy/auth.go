/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token3 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

var logger = logging.MustGetLogger()

// EscrowAuth implements the Authorization interface for policy identity tokens.
type EscrowAuth struct {
	WalletService driver.WalletService
}

// NewEscrowAuth returns a new EscrowAuth.
func NewEscrowAuth(walletService driver.WalletService) *EscrowAuth {
	return &EscrowAuth{WalletService: walletService}
}

// AmIAnAuditor returns false; policy identities are never auditors.
func (s *EscrowAuth) AmIAnAuditor() bool {
	return false
}

// IsMine returns true if any component identity of the policy token belongs to one of our owner wallets.
func (s *EscrowAuth) IsMine(ctx context.Context, tok *token3.Token) (string, []string, bool) {
	owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
	if err != nil {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, failed unmarshalling [%s]", view.Identity(tok.Owner), tok.Type, tok.Quantity, err)

		return "", nil, false
	}
	if owner.Type != boolpolicy.Policy {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, owner type is [%d] instead of [%d]", view.Identity(tok.Owner), tok.Type, tok.Quantity, owner.Type, boolpolicy.Policy)

		return "", nil, false
	}
	pi := &boolpolicy.PolicyIdentity{}
	if err := pi.Deserialize(owner.Identity); err != nil {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, failed deserialising policy identity [%s]", view.Identity(tok.Owner), tok.Type, tok.Quantity, err)

		return "", nil, false
	}
	var ids []string
	for i := range len(pi.Identities) {
		if wallet, err := s.WalletService.OwnerWallet(ctx, pi.Identities[i]); err == nil {
			ids = append(ids, policyWallet(wallet))
		}
	}

	return "", ids, len(ids) != 0
}

// Issued always returns false; policy identities are not issuers.
func (s *EscrowAuth) Issued(_ context.Context, _ driver.Identity, _ *token3.Token) bool {
	return false
}

// OwnerType returns the identity type and inner bytes of a typed identity.
func (s *EscrowAuth) OwnerType(raw []byte) (driver.IdentityType, []byte, error) {
	owner, err := identity.UnmarshalTypedIdentity(raw)
	if err != nil {
		return driver.ZeroIdentityType, nil, err
	}

	return owner.Type, owner.Identity, nil
}

type ownerWallet interface {
	ID() string
}

func policyWallet(w ownerWallet) string {
	return "policy" + w.ID()
}
