/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"encoding/hex"

	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/csp"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/pkcs11"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/hyperledger/fabric-lib-go/bccsp/sw"
)

type (
	//go:generate counterfeiter -o mocks/key_store.go -fake-name KeyStore github.com/hyperledger/fabric-lib-go/bccsp.KeyStore
	KeyStore = bccsp.KeyStore
)

// GetBCCSPFromConf returns a BCCSP instance and its relative key store from the passed configuration.
// If no configuration is passed, the default one is used, namely the `SW` provider.
func GetBCCSPFromConf(conf *BCCSP, keyStore bccsp.KeyStore) (bccsp.BCCSP, error) {
	if conf == nil {
		return GetDefaultBCCSP(keyStore)
	}
	switch conf.Default {
	case "SW":
		return GetDefaultBCCSP(keyStore)
	case "PKCS11":
		return GetPKCS11BCCSP(conf, keyStore)
	default:
		return nil, errors.Errorf("invalid BCCSP.Default.%s", conf.Default)
	}
}

// GetPKCS11BCCSP returns a new instance of the HSM-based BCCSP
func GetPKCS11BCCSP(conf *BCCSP, keyStore bccsp.KeyStore) (bccsp.BCCSP, error) {
	if conf.PKCS11 == nil {
		return nil, errors.New("invalid BCCSP.PKCS11. missing configuration")
	}

	p11Opts := conf.PKCS11
	if keyStore == nil {
		keyStore = sw.NewDummyKeyStore()
	}
	opts := ToPKCS11OptsOpts(p11Opts)
	csp, err := pkcs11.NewProvider(*opts, keyStore, skiMapper(*p11Opts))

	return csp, err
}

func skiMapper(p11Opts PKCS11) func([]byte) []byte {
	keyMap := map[string]string{}
	for _, k := range p11Opts.KeyIDs {
		keyMap[k.SKI] = k.ID
	}

	return func(ski []byte) []byte {
		keyID := hex.EncodeToString(ski)
		if id, ok := keyMap[keyID]; ok {
			return []byte(id)
		}
		if p11Opts.AltID != "" {
			return []byte(p11Opts.AltID)
		}

		return ski
	}
}

// GetDefaultBCCSP returns a new instance of the software-based BCCSP
func GetDefaultBCCSP(keyStore bccsp.KeyStore) (bccsp.BCCSP, error) {
	if keyStore == nil {
		keyStore = sw.NewDummyKeyStore()
	}
	cryptoProvider, err := csp.NewCSP(keyStore)
	if err != nil {
		return nil, err
	}

	return cryptoProvider, nil
}
