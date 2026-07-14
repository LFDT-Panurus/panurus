/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"context"

	bccsp "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/cache"
	crypto2 "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

var logger = logging.MustGetLogger()

type NewKeyManagerFunc = func(
	conf *crypto2.Config,
	sigType bccsp.SignatureType,
	csp bccsp.BCCSP,
) (membership.KeyManager, error)

type KeyManagerProvider struct {
	issuerPublicKey []byte
	curveID         math.CurveID
	keyStore        bccsp.KeyStore
	config          idriver.Config
	cacheSize       int
	metricsProvider metrics.Provider

	newKeyManager NewKeyManagerFunc

	// ignoreVerifyOnlyWallet when set to true, for each wallet the service will force the load of the secrets
	ignoreVerifyOnlyWallet bool
}

func NewKeyManagerProvider(
	issuerPublicKey []byte,
	curveID math.CurveID,
	keyStore bccsp.KeyStore,
	config idriver.Config,
	cacheSize int,
	ignoreVerifyOnlyWallet bool,
	metricsProvider metrics.Provider,
) *KeyManagerProvider {
	return NewKeyManagerProviderWithKeyManagerFactory(
		issuerPublicKey,
		curveID,
		keyStore,
		config,
		cacheSize,
		ignoreVerifyOnlyWallet,
		metricsProvider,
		func(conf *crypto2.Config, sigType bccsp.SignatureType, csp bccsp.BCCSP) (membership.KeyManager, error) {
			return NewKeyManager(conf, sigType, csp)
		},
	)
}

func NewKeyManagerProviderWithKeyManagerFactory(
	issuerPublicKey []byte,
	curveID math.CurveID,
	keyStore bccsp.KeyStore,
	config idriver.Config,
	cacheSize int,
	ignoreVerifyOnlyWallet bool,
	metricsProvider metrics.Provider,
	newKeyManager NewKeyManagerFunc,
) *KeyManagerProvider {
	return &KeyManagerProvider{
		issuerPublicKey:        issuerPublicKey,
		curveID:                curveID,
		keyStore:               keyStore,
		config:                 config,
		cacheSize:              cacheSize,
		ignoreVerifyOnlyWallet: ignoreVerifyOnlyWallet,
		metricsProvider:        metricsProvider,
		newKeyManager:          newKeyManager,
	}
}

func (l *KeyManagerProvider) Get(ctx context.Context, identityConfig *driver.IdentityConfiguration) (membership.KeyManager, error) {
	var conf *crypto2.Config
	var err error
	if len(identityConfig.Raw) != 0 {
		// load the config directly from identityConfig.Raw
		logger.Infof("load the config directly from identityConfig.Raw [%s][%s]", identityConfig.ID, utils.Hashable(identityConfig.Raw))
		conf, err = crypto2.NewConfigFromRaw(l.issuerPublicKey, identityConfig.Raw)
	} else {
		// load from URL
		logger.Infof("load the config form identityConfig.URL [%s][%s]", identityConfig.ID, identityConfig.URL)
		conf, err = crypto2.NewConfigWithIPK(l.issuerPublicKey, identityConfig.URL, l.ignoreVerifyOnlyWallet)
	}
	if err != nil {
		return nil, err
	}

	// instantiate provider from configuration
	cryptoProvider, err := crypto2.NewBCCSP(l.keyStore, l.curveID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to instantiate crypto provider")
	}
	keyManager, err := l.newKeyManager(conf, bccsp.EidNymRhNym, cryptoProvider)
	if err != nil {
		return nil, errors.Wrapf(err, "failed instantiating idemix key manager provider from [%s]", identityConfig.URL)
	}

	cacheSize, err := l.cacheSizeForID(identityConfig.ID)
	if err != nil {
		return nil, err
	}

	var getIdentityFunc func(context.Context, []byte) (*idriver.IdentityDescriptor, error)
	if keyManager.IsRemote() {
		id := identityConfig.ID
		getIdentityFunc = func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
			return nil, errors.Errorf("cannot invoke this function, remote must register pseudonyms on wallet [%v]", id)
		}
	} else {
		getIdentityFunc = cache.NewIdentityCache(
			keyManager.Identity,
			cacheSize,
			nil,
			cache.NewMetrics(l.metricsProvider),
		).Identity
	}

	// finalize identity configuration
	// remove SK and keep only SKI
	conf.Signer.Sk = nil
	identityConfigurationRawField, err := proto.Marshal(conf)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to marshal identity configuration")
	}
	identityConfig.Raw = identityConfigurationRawField

	return &WrappedKeyManager{
		KeyManager:      keyManager,
		getIdentityFunc: getIdentityFunc,
	}, nil
}

func (l *KeyManagerProvider) cacheSizeForID(id string) (int, error) {
	cacheSize := l.config.CacheSizeForOwnerID(id)
	if cacheSize <= 0 {
		logger.Debugf("cache size for %s not configured, using default (%d)", id, l.cacheSize)
		cacheSize = l.cacheSize
	}

	return cacheSize, nil
}

type WrappedKeyManager struct {
	membership.KeyManager
	getIdentityFunc func(context.Context, []byte) (*idriver.IdentityDescriptor, error)
}

func (k *WrappedKeyManager) Identity(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
	return k.getIdentityFunc(ctx, auditInfo)
}
