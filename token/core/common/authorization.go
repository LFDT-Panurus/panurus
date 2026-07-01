/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Authorization defines an interface for checking the ownership, issuance, and auditor status of tokens.
type Authorization interface {
	// IsMine returns true if the passed token is owned by an owner wallet.
	// It returns the ID of the owner wallet and any additional owner identifier, if supported.
	// It is possible that the wallet ID is empty and the additional owner identifier list is not.
	IsMine(ctx context.Context, tok *token2.Token) (string, []string, bool)
	// AmIAnAuditor return true if the passed TMS contains an auditor wallet for any of the auditor identities
	// defined in the public parameters of the passed TMS.
	AmIAnAuditor() bool
	// Issued returns true if the passed issuer issued the passed token
	Issued(ctx context.Context, issuer token.Identity, tok *token2.Token) bool
}

// WalletBasedAuthorization is a wallet-based authorization implementation
type WalletBasedAuthorization struct {
	Logger           logging.Logger
	PublicParameters driver.PublicParameters
	WalletService    driver.WalletService
	amIAnAuditor     bool
}

// NewTMSAuthorization returns an Authorization for the passed public parameters and wallet service.
// A node that holds an auditor wallet and no owner wallets can never own a token, so its
// authorization is wrapped to skip the always-missing owner lookup on the audit hot path.
func NewTMSAuthorization(logger logging.Logger, publicParameters driver.PublicParameters, walletService driver.WalletService) Authorization {
	amIAnAuditor := false
	var errs []error
	for _, identity := range publicParameters.Auditors() {
		_, err := walletService.AuditorWallet(context.Background(), identity)
		if err == nil {
			amIAnAuditor = true

			break
		}
		errs = append(errs, errors.Wrapf(err, "I'm not this auditor identity [%s]", identity))
	}
	logger.Debugf("am I an auditor? [%v], with errs [%v]", amIAnAuditor, errs)

	auth := &WalletBasedAuthorization{Logger: logger, PublicParameters: publicParameters, WalletService: walletService, amIAnAuditor: amIAnAuditor}

	if amIAnAuditor {
		if ids, err := walletService.OwnerWalletIDs(context.Background()); err == nil && len(ids) == 0 {
			return pureAuditorAuthorization{auth}
		}
	}

	return auth
}

// pureAuditorAuthorization decorates an Authorization for a node that holds an auditor
// wallet and no owner wallets: it can never own a token, so IsMine is short-circuited
// and the owner lookup is skipped. All other checks delegate to the wrapped Authorization.
type pureAuditorAuthorization struct {
	Authorization
}

// IsMine always reports the token as not owned, since the node holds no owner wallets.
func (pureAuditorAuthorization) IsMine(context.Context, *token2.Token) (string, []string, bool) {
	return "", nil, false
}

// IsMine returns true if the passed token is owned by an owner wallet.
// It returns the ID of the owner wallet and no additional owner identifiers.
func (w *WalletBasedAuthorization) IsMine(ctx context.Context, tok *token2.Token) (string, []string, bool) {
	wallet, err := w.WalletService.OwnerWallet(ctx, tok.Owner)
	if err != nil {
		return "", nil, false
	}

	return wallet.ID(), nil, true
}

// AmIAnAuditor return true if the passed TMS contains an auditor wallet for any of the auditor identities
// defined in the public parameters of the passed TMS.
func (w *WalletBasedAuthorization) AmIAnAuditor() bool {
	return w.amIAnAuditor
}

// Issued returns true if the passed issuer issued the passed token
func (w *WalletBasedAuthorization) Issued(ctx context.Context, issuer token.Identity, tok *token2.Token) bool {
	_, err := w.WalletService.IssuerWallet(ctx, issuer)

	return err == nil
}

// AuthorizationMultiplexer iterates over multiple authorization checker
type AuthorizationMultiplexer struct {
	authorizations []Authorization
}

// NewAuthorizationMultiplexer returns a new AuthorizationMultiplexer for the passed ownership checkers
func NewAuthorizationMultiplexer(ownerships ...Authorization) *AuthorizationMultiplexer {
	return &AuthorizationMultiplexer{authorizations: ownerships}
}

// IsMine returns true it there exists an authorization checker that returns true
func (o *AuthorizationMultiplexer) IsMine(ctx context.Context, tok *token2.Token) (string, []string, bool) {
	for _, authorization := range o.authorizations {
		walletID, ids, mine := authorization.IsMine(ctx, tok)
		if mine {
			return walletID, ids, true
		}
	}

	return "", nil, false
}

// AmIAnAuditor returns true it there exists an authorization checker that returns true
func (o *AuthorizationMultiplexer) AmIAnAuditor() bool {
	for _, authorization := range o.authorizations {
		yes := authorization.AmIAnAuditor()
		if yes {
			return true
		}
	}

	return false
}

// Issued returns true it there exists an authorization checker that returns true
func (o *AuthorizationMultiplexer) Issued(ctx context.Context, issuer token.Identity, tok *token2.Token) bool {
	for _, authorization := range o.authorizations {
		yes := authorization.Issued(ctx, issuer, tok)
		if yes {
			return true
		}
	}

	return false
}

// OwnerType returns the type of owner (e.g. 'idemix' or 'htlc') and the identity bytes.
func (o *AuthorizationMultiplexer) OwnerType(raw []byte) (driver.IdentityType, []byte, error) {
	owner, err := identity.UnmarshalTypedIdentity(raw)
	if err != nil {
		return driver.ZeroIdentityType, nil, err
	}

	return owner.Type, owner.Identity, nil
}
