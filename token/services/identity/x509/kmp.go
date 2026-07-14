/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package x509

import (
	"context"
	"path/filepath"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/csp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.yaml.in/yaml/v3"
)

const (
	KeystoreFullFolder = "keystoreFull"
	PrivateKeyFileName = "priv_sk"
	KeystoreFolder     = "keystore"
	ExtraPathElement   = "msp"
)

func NewKeyStore(kvs idriver.Keystore) crypto.KeyStore {
	return csp.NewKVSStore(kvs)
}

type KeyManagerProvider struct {
	config   idriver.Config
	keyStore crypto.KeyStore
	// ignoreVerifyOnlyWallet when set to true, for each wallet the service will force the load of the secrets
	ignoreVerifyOnlyWallet bool
}

func NewKeyManagerProvider(config idriver.Config, keyStore crypto.KeyStore, ignoreVerifyOnlyWallet bool) *KeyManagerProvider {
	return &KeyManagerProvider{
		config:                 config,
		ignoreVerifyOnlyWallet: ignoreVerifyOnlyWallet,
		keyStore:               keyStore,
	}
}

func (k *KeyManagerProvider) Get(ctx context.Context, idConfig *driver.IdentityConfiguration) (membership.KeyManager, error) {
	identityConfig := &idriver.ConfiguredIdentity{
		ID:   idConfig.ID,
		Path: idConfig.URL,
	}
	if len(idConfig.Config) != 0 {
		// load opts as yaml
		if err := yaml.Unmarshal(idConfig.Config, &identityConfig.Opts); err != nil {
			return nil, errors.Wrapf(err, "failed to load options for [%s]", idConfig.ID)
		}
	}
	var config *crypto.Config
	if len(idConfig.Raw) != 0 {
		// load raw as config
		var err error
		config, err = crypto.UnmarshalConfig(idConfig.Raw)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to load config [%s]", idConfig.ID)
		}
	}

	return k.registerIdentity(ctx, config, identityConfig, idConfig)
}

func (k *KeyManagerProvider) registerIdentity(ctx context.Context, conf *crypto.Config, identityConfig *idriver.ConfiguredIdentity, idConfig *driver.IdentityConfiguration) (membership.KeyManager, error) {
	p, err := k.registerProvider(ctx, conf, identityConfig, idConfig)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to register provider")
	}

	return p, nil
}

func (k *KeyManagerProvider) registerProvider(ctx context.Context, conf *crypto.Config, identityConfig *idriver.ConfiguredIdentity, idConfig *driver.IdentityConfiguration) (membership.KeyManager, error) {
	opts, err := crypto.ToBCCSPOpts(identityConfig.Opts)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to extract BCCSP options")
	}
	if opts == nil {
		logger.DebugfContext(ctx, "no BCCSP options set for [%s], opts [%v]", identityConfig.ID, identityConfig.Opts)
	} else {
		logger.DebugfContext(ctx, "BCCSP options set for [%s] to [%v:%v:%v]", identityConfig.ID, opts, opts.PKCS11, opts.SW)
	}

	translatedPath := k.config.TranslatePath(identityConfig.Path)
	keyStorePath := k.keyStorePath()
	logger.DebugfContext(ctx, "load provider at [%s][%s]", translatedPath, keyStorePath)
	// Try without ExtraPathElement
	provider, conf, err := NewKeyManagerFromConf(conf, translatedPath, keyStorePath, opts, k.keyStore)
	if err != nil {
		logger.DebugfContext(ctx, "failed loading provider at [%s]: [%s]", translatedPath, err)
		// Try with ExtraPathElement (msp/)
		provider, conf, err = NewKeyManagerFromConf(conf, filepath.Join(translatedPath, ExtraPathElement), keyStorePath, opts, k.keyStore)
		if err != nil {
			logger.DebugfContext(ctx, "failed loading provider at [%s]: [%s]", filepath.Join(translatedPath, ExtraPathElement), err)

			return nil, err
		}
	}

	logger.DebugfContext(ctx, "prepare opts and conf for [%s][%s]", translatedPath, keyStorePath)

	optsRaw, err := yaml.Marshal(identityConfig.Opts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal config [%v]", identityConfig)
	}
	confRaw, err := crypto.MarshalConfig(conf)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal config [%v]", identityConfig)
	}
	idConfig.Config = optsRaw
	idConfig.Raw = confRaw

	logger.DebugfContext(ctx, "provider ready for [%s][%s][%s]", translatedPath, keyStorePath, provider)

	return provider, nil
}

func (k *KeyManagerProvider) keyStorePath() string {
	if !k.ignoreVerifyOnlyWallet {
		return ""
	}

	return KeystoreFullFolder
}
