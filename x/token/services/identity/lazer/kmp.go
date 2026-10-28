/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/cache"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// KeyManagerProvider creates lazer key managers from identity configurations.
type KeyManagerProvider struct {
	issuerPublicKey []byte
	keystore        idriver.Keystore
	identityStore   IdentityStoreService
	config          idriver.Config
	cacheSize       int
	metricsProvider metrics.Provider

	// ignoreVerifyOnlyWallet when set to true, for each wallet the service will force the load of the secrets
	ignoreVerifyOnlyWallet bool
}

// NewKeyManagerProvider returns a provider for the given issuer public key.
func NewKeyManagerProvider(
	issuerPublicKey []byte,
	keystore idriver.Keystore,
	identityStore IdentityStoreService,
	config idriver.Config,
	cacheSize int,
	ignoreVerifyOnlyWallet bool,
	metricsProvider metrics.Provider,
) *KeyManagerProvider {
	return &KeyManagerProvider{
		issuerPublicKey:        issuerPublicKey,
		keystore:               keystore,
		identityStore:          identityStore,
		config:                 config,
		cacheSize:              cacheSize,
		ignoreVerifyOnlyWallet: ignoreVerifyOnlyWallet,
		metricsProvider:        metricsProvider,
	}
}

// Get returns the key manager for the passed identity configuration. The wallet
// secret is moved to the keystore and removed from identityConfig.Raw.
func (l *KeyManagerProvider) Get(ctx context.Context, identityConfig *driver.IdentityConfiguration) (membership.KeyManager, error) {
	var conf *Config
	var err error
	if len(identityConfig.Raw) != 0 {
		logger.Infof("load the config directly from identityConfig.Raw [%s][%s]", identityConfig.ID, utils.Hashable(identityConfig.Raw))
		conf, err = NewConfigFromRaw(l.issuerPublicKey, identityConfig.Raw)
	} else {
		logger.Infof("load the config from identityConfig.URL [%s][%s]", identityConfig.ID, identityConfig.URL)
		conf, err = NewConfigWithIPK(l.issuerPublicKey, l.config.TranslatePath(identityConfig.URL), l.ignoreVerifyOnlyWallet)
	}
	if err != nil {
		return nil, err
	}

	keyManager, err := NewKeyManager(conf, l.keystore, l.identityStore)
	if err != nil {
		return nil, errors.Wrapf(err, "failed instantiating lazer key manager from [%s]", identityConfig.URL)
	}

	var getIdentityFunc func(context.Context, []byte) (*idriver.IdentityDescriptor, error)
	// identityCache owns a background provisioning goroutine that only its Close stops
	var identityCache *cache.IdentityCache
	if keyManager.IsRemote() {
		id := identityConfig.ID
		getIdentityFunc = func(context.Context, []byte) (*idriver.IdentityDescriptor, error) {
			return nil, errors.Errorf("cannot invoke this function, remote must register pseudonyms on wallet [%v]", id)
		}
	} else {
		identityCache = cache.NewIdentityCache(keyManager.Identity, l.cacheSizeForID(identityConfig.ID), nil, cache.NewMetrics(l.metricsProvider))
		getIdentityFunc = identityCache.Identity
	}

	// finalize identity configuration: keep only the SKI, never the secret
	if conf.Signer != nil {
		conf.Signer.Sk = nil
	}
	raw, err := conf.Marshal()
	if err != nil {
		keyManager.Close()

		return nil, errors.WithMessagef(err, "failed to marshal identity configuration")
	}
	identityConfig.Raw = raw

	return &WrappedKeyManager{KeyManager: keyManager, getIdentityFunc: getIdentityFunc, identityCache: identityCache}, nil
}

func (l *KeyManagerProvider) cacheSizeForID(id string) int {
	if cacheSize := l.config.CacheSizeForOwnerID(id); cacheSize > 0 {
		return cacheSize
	}
	logger.Debugf("cache size for %s not configured, using default (%d)", id, l.cacheSize)

	return l.cacheSize
}

// WrappedKeyManager serves identities from the cache of pre-generated pseudonyms.
type WrappedKeyManager struct {
	*KeyManager
	getIdentityFunc func(context.Context, []byte) (*idriver.IdentityDescriptor, error)
	// identityCache is nil for remote key managers, which do not pre-provision.
	identityCache *cache.IdentityCache
}

// Close stops the background provisioning and releases the key manager.
//
// The identity cache has no way to wait for its provisioning goroutine, so
// Close only cancels it: an identity generation in progress completes or fails
// with "key manager closed", after which the goroutine sees the cancellation
// and exits. This is safe because KeyManager.Close and KeyManager.Identity are
// synchronized, and Identity refuses to serve once closed.
func (k *WrappedKeyManager) Close() {
	if k.identityCache != nil {
		k.identityCache.Close()
	}
	k.KeyManager.Close()
}

// Identity returns a pseudonym, from the cache if possible. It fails after
// Close, even if the cache still holds pseudonyms generated before.
func (k *WrappedKeyManager) Identity(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
	if k.isClosed() {
		return nil, errors.New("key manager closed")
	}

	return k.getIdentityFunc(ctx, auditInfo)
}

var (
	_ membership.KeyManager               = (*WrappedKeyManager)(nil)
	_ idriver.ProbeFreeSignerDeserializer = (*WrappedKeyManager)(nil)
)
