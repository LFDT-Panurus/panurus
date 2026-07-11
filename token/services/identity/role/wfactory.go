/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role

import (
	"context"
	"math/big"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o mock/tv.go -fake-name TokenVault . TokenVault
type TokenVault interface {
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token.Type) (driver.UnspentTokensIterator, error)
	ListHistoryIssuedTokens(ctx context.Context) (*token.IssuedTokens, error)
	Balance(ctx context.Context, id string, tokenType token.Type) (*big.Int, error)
	IssuedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error)
	RedeemedBalance(ctx context.Context, opts driver.IssuerBalanceQuery) (*big.Int, error)
}

//go:generate counterfeiter -o mock/wc.go -fake-name WalletsConfiguration . WalletsConfiguration
type WalletsConfiguration interface {
	CacheSizeForOwnerID(id string) int
}

//go:generate counterfeiter -o mock/is.go -fake-name IdentitySupport . IdentitySupport
type IdentitySupport interface {
	BindIdentity(ctx context.Context, identity driver.Identity, eID string, wID idriver.WalletID, meta any, confID string) error
	ContainsIdentity(ctx context.Context, i driver.Identity, id string) bool
}

//go:generate counterfeiter -o mock/deserializer.go -fake-name Deserializer . Deserializer
type Deserializer = driver.Deserializer

// DefaultFactory creates wallets for the default role.
type DefaultFactory struct {
	Logger               logging.Logger
	IdentityProvider     IdentityProvider
	TokenVault           TokenVault
	WalletsConfiguration WalletsConfiguration
	Deserializer         Deserializer
	MetricsProvider      metrics.Provider
}

// NewDefaultFactory creates a new DefaultFactory.
func NewDefaultFactory(
	logger logging.Logger,
	identityProvider driver.IdentityProvider,
	tokenVault TokenVault,
	walletsConfiguration WalletsConfiguration,
	deserializer Deserializer,
	metricsProvider metrics.Provider,
) *DefaultFactory {
	return &DefaultFactory{
		Logger:               logger,
		IdentityProvider:     identityProvider,
		TokenVault:           tokenVault,
		WalletsConfiguration: walletsConfiguration,
		Deserializer:         deserializer,
		MetricsProvider:      metricsProvider,
	}
}

func (w *DefaultFactory) NewWallet(ctx context.Context, id idriver.WalletID, role idriver.IdentityRoleType, wr IdentitySupport, info idriver.IdentityInfo) (driver.Wallet, error) {
	switch role {
	case idriver.OwnerRole:
		if info.Anonymous() {
			newWallet, err := NewAnonymousOwnerWallet(
				w.Logger,
				w.IdentityProvider,
				w.TokenVault,
				w.Deserializer,
				wr,
				id,
				info,
				w.WalletsConfiguration.CacheSizeForOwnerID(id),
				w.MetricsProvider,
			)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to create new owner wallet [%s]", id)
			}
			w.Logger.DebugfContext(
				ctx,
				"created owner wallet [%s] for identity [%s:%s:%v]",
				id,
				info.ID(),
				info.EnrollmentID(),
				info.Remote(),
			)

			return newWallet, nil
		}

		// non-anonymous
		newWallet, err := NewLongTermOwnerWallet(ctx, w.IdentityProvider, w.TokenVault, id, info)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to create owner wallet [%s]", id)
		}

		return newWallet, nil
	case idriver.IssuerRole:
		idInfoIdentity, _, err := info.Get(ctx)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get issuer wallet identity for [%s]", id)
		}
		signer, err := w.IdentityProvider.GetSigner(ctx, idInfoIdentity)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get issuer signer [%s]", id)
		}

		newWallet := NewIssuerWallet(w.Logger, w.TokenVault, id, idInfoIdentity, signer)
		if err := wr.BindIdentity(ctx, idInfoIdentity, info.EnrollmentID(), id, nil, info.ConfigurationID()); err != nil {
			return nil, errors.WithMessagef(err, "programming error, failed to register recipient identity [%s]", id)
		}
		w.Logger.DebugfContext(ctx, "created issuer wallet [%s]", id)

		return newWallet, nil
	case idriver.AuditorRole:
		w.Logger.DebugfContext(ctx, "no wallet found, create it [%s]", id)
		idInfoIdentity, _, err := info.Get(ctx)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get auditor wallet identity for [%s]", id)
		}
		signer, err := w.IdentityProvider.GetSigner(ctx, idInfoIdentity)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get auditor signer [%s]", id)
		}

		newWallet := NewAuditorWallet(id, idInfoIdentity, signer)
		if err := wr.BindIdentity(ctx, idInfoIdentity, info.EnrollmentID(), id, nil, info.ConfigurationID()); err != nil {
			return nil, errors.WithMessagef(err, "programming error, failed to register recipient identity [%s]", id)
		}
		w.Logger.DebugfContext(ctx, "created auditor wallet [%s]", id)

		return newWallet, nil
	case idriver.CertifierRole:
		return nil, errors.Errorf("certifiers are not supported by this driver")
	default:
		return nil, errors.Errorf("role [%d] not supported", role)
	}
}
