/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// ProtocolVersionV1 is the only supported configuration version.
	ProtocolVersionV1 = 1

	// ExtraPathElement is tried when the configuration is not found in the given directory.
	ExtraPathElement = "msp"

	ConfigDirUser                = "user"
	ConfigFileSigner             = "SignerConfig"
	ConfigFileSignerFull         = "SignerConfigFull"
	ConfigFileIssuerPublicKey    = "IssuerPublicKey"
	ConfigFileIssuerSecretKey    = "IssuerSecretKey"
	secretLen                    = anoncreds.AttrLen
	maxConfigSize                = 1 << 20
	filePerm                     = 0o600
	dirPerm                      = 0o700
	signerConfigRequiredFieldMsg = "signer config: missing"
)

// Config is the configuration of a lazer key manager.
type Config struct {
	Version int           `json:"version"`
	Name    string        `json:"name"`
	IPK     []byte        `json:"ipk"`
	Signer  *SignerConfig `json:"signer,omitempty"`
}

// SignerConfig holds the credential and the secrets of a wallet.
type SignerConfig struct {
	// Cred is the credential issued by the issuer.
	Cred []byte `json:"cred,omitempty"`
	// Sk is usk (32 bytes) followed by the exported state of the user. It is
	// moved to the keystore and cleared from the stored configuration.
	Sk               []byte `json:"sk,omitempty"`
	OU               string `json:"ou"`
	Role             string `json:"role"`
	EnrollmentID     string `json:"enrollment_id"`
	RevocationHandle string `json:"revocation_handle"`
	// Ski identifies the secret in the keystore.
	Ski []byte `json:"ski,omitempty"`
}

// NewConfigFromRaw parses a marshalled configuration and checks it against the issuer public key.
func NewConfigFromRaw(ipk []byte, raw []byte) (*Config, error) {
	if len(raw) == 0 || len(raw) > maxConfigSize {
		return nil, errors.New("invalid configuration size")
	}
	conf := &Config{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(conf); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal configuration")
	}

	return conf, conf.validate(ipk)
}

// NewConfigWithIPK loads the signer configuration from dir (or dir/msp) and
// combines it with the passed issuer public key. If ignoreVerifyOnlyWallet is
// set, SignerConfigFull is preferred.
func NewConfigWithIPK(ipk []byte, dir string, ignoreVerifyOnlyWallet bool) (*Config, error) {
	conf, err := newConfigWithIPK(ipk, dir, ignoreVerifyOnlyWallet)
	if err != nil {
		conf, err = newConfigWithIPK(ipk, filepath.Join(dir, ExtraPathElement), ignoreVerifyOnlyWallet)
		if err != nil {
			return nil, errors.Wrapf(err, "failed reading lazer configuration from [%s]", dir)
		}
	}

	return conf, nil
}

// NewConfig loads the configuration (issuer public key included) from dir.
func NewConfig(dir string) (*Config, error) {
	ipk, err := readFile(filepath.Join(dir, ExtraPathElement, ConfigFileIssuerPublicKey))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read issuer public key")
	}

	return NewConfigWithIPK(ipk, dir, true)
}

func newConfigWithIPK(ipk []byte, dir string, ignoreVerifyOnlyWallet bool) (*Config, error) {
	path := filepath.Join(dir, ConfigDirUser, ConfigFileSigner)
	if ignoreVerifyOnlyWallet {
		full := filepath.Join(dir, ConfigDirUser, ConfigFileSignerFull)
		if _, err := os.Stat(full); err == nil {
			path = full
		}
	}
	conf := &Config{Version: ProtocolVersionV1, IPK: ipk}
	raw, err := readFile(path)
	if err != nil {
		if !os.IsNotExist(errors.Cause(err)) {
			return nil, err
		}
		// no signer: verify-only (remote) wallet

		return conf, conf.validate(ipk)
	}
	signer := &SignerConfig{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(signer); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal signer config at [%s]", path)
	}
	conf.Signer = signer

	return conf, conf.validate(ipk)
}

func (c *Config) validate(ipk []byte) error {
	if c.Version != ProtocolVersionV1 {
		return errors.Errorf("unsupported protocol version [%d]", c.Version)
	}
	if len(c.IPK) != anoncreds.PubKeyLen {
		return errors.Errorf("invalid issuer public key length [%d]", len(c.IPK))
	}
	if len(ipk) != 0 && !bytes.Equal(ipk, c.IPK) {
		return errors.New("issuer public key does not match the configuration")
	}
	if c.Signer != nil && len(c.Signer.Sk) != 0 && len(c.Signer.Sk) <= secretLen {
		return errors.New("invalid secret")
	}

	return nil
}

// Marshal serializes the configuration.
func (c *Config) Marshal() ([]byte, error) {
	return json.Marshal(c)
}

// WriteSignerConfig writes the signer configuration (with secrets) to dir/user.
func WriteSignerConfig(dir string, signer *SignerConfig, full bool) error {
	name := ConfigFileSigner
	if full {
		name = ConfigFileSignerFull
	}
	// Cred is the public credential, the secret is Sk: writing both is the point of this function
	raw, err := json.Marshal(signer) //nolint:gosec // G117: see above
	if err != nil {
		return errors.Wrap(err, "failed to marshal signer config")
	}

	return writeFile(filepath.Join(dir, ConfigDirUser, name), raw)
}

// WriteIssuerKeys writes the issuer key pair to dir/msp.
func WriteIssuerKeys(dir string, sk, pk []byte) error {
	if err := writeFile(filepath.Join(dir, ExtraPathElement, ConfigFileIssuerPublicKey), pk); err != nil {
		return err
	}
	if len(sk) == 0 {
		return nil
	}

	return writeFile(filepath.Join(dir, ExtraPathElement, ConfigFileIssuerSecretKey), sk)
}

// ReadIssuerKeys reads the issuer key pair from dir/msp.
func ReadIssuerKeys(dir string) (sk, pk []byte, err error) {
	pk, err = readFile(filepath.Join(dir, ExtraPathElement, ConfigFileIssuerPublicKey))
	if err != nil {
		return nil, nil, err
	}
	sk, err = readFile(filepath.Join(dir, ExtraPathElement, ConfigFileIssuerSecretKey))
	if err != nil {
		return nil, nil, err
	}

	return sk, pk, nil
}

func readFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read [%s]", path)
	}

	return raw, nil
}

func writeFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return errors.Wrapf(err, "failed to create [%s]", filepath.Dir(path))
	}

	return errors.Wrapf(os.WriteFile(path, raw, filePerm), "failed to write [%s]", path)
}
