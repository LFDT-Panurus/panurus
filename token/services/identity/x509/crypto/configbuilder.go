/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"os"
	"path/filepath"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-lib-go/bccsp"
)

const (
	SignCertsDirName = "signcerts"
	KeyStoreDirName  = "keystore"
	PrivSKFileName   = "priv_sk"

	ProtobufProtocolVersionV1 uint32 = 1
)

func LoadConfig(dir string, keyStoreDirName string) (*Config, error) {
	signcertDir := filepath.Join(dir, SignCertsDirName)
	signcert, err := getPemMaterialFromDir(signcertDir)
	if err != nil {
		return nil, errors.Wrapf(err, "could not load a valid signer certificate from directory %s", signcertDir)
	}
	if len(signcert) == 0 {
		return nil, errors.Errorf("no signer certificate found in directory %s", signcertDir)
	}
	// load secret key, if available. If not available, the public's key SKI will be used to load the secret key from the key store
	if len(keyStoreDirName) == 0 {
		keyStoreDirName = KeyStoreDirName
	}
	secretKeyFile := filepath.Join(dir, keyStoreDirName, PrivSKFileName)
	var skRaw []byte
	if _, err := os.Stat(secretKeyFile); err == nil {
		skRaw, err = readPemFile(secretKeyFile)
		if err != nil {
			return nil, errors.Wrapf(err, "could not load private key from file %s", secretKeyFile)
		}
	}

	return LoadConfigWithIdentityInfo(&SigningIdentityInfo{
		PublicSigner: signcert[0],
		PrivateSigner: &KeyInfo{
			KeyIdentifier: "",
			KeyMaterial:   skRaw,
		},
	})
}

func LoadConfigWithIdentityInfo(signingIdentityInfo *SigningIdentityInfo) (*Config, error) {
	config := &Config{
		Version:         ProtobufProtocolVersionV1,
		SigningIdentity: signingIdentityInfo,
		CryptoConfig: &CryptoConfig{
			SignatureHashFamily: bccsp.SHA2,
		},
	}

	return config, nil
}

func RemovePrivateSigner(c *Config) (*Config, error) {
	if c.SigningIdentity == nil {
		// nothing to remove
		return c, nil
	}
	c.SigningIdentity.PrivateSigner = nil

	return c, nil
}
