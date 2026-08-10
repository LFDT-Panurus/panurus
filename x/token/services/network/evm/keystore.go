/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// privateKeyLength is the byte length of a secp256k1 private-key scalar.
const privateKeyLength = 32

// LoadKey reads a secp256k1 private key from a file holding its hex-encoded scalar, optionally
// 0x-prefixed and with surrounding whitespace, which is the format foundry and the common EVM dev
// tooling emit.
//
// Encrypted keystores are deliberately not supported here: this is the key material an operator
// provisions for a node, and adding a passphrase path without somewhere safe to source the passphrase
// from would be security theatre. Production custody (HSM, rotation) is called out as deployment work
// in the design, and the signer is an interface, so a custody-backed implementation can replace this
// without touching the driver.
func LoadKey(path string) (*secp256k1.PrivateKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("evm keystore: empty key path")
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, errors.Wrapf(err, "evm keystore: failed to read the key at [%s]", path)
	}

	scalar, err := decodeKeyScalar(string(raw))
	if err != nil {
		return nil, errors.Wrapf(err, "evm keystore: invalid key material at [%s]", path)
	}

	key := secp256k1.PrivKeyFromBytes(scalar)
	if key.Key.IsZero() {
		return nil, errors.Errorf("evm keystore: the key at [%s] is the zero scalar", path)
	}

	return key, nil
}

// LoadKeyForAddress loads a key and checks it derives the expected address, so a configuration that
// pairs the wrong key with an address fails at startup rather than by producing signatures nobody
// accepts. An empty expected address skips the check.
func LoadKeyForAddress(path, expected string) (*secp256k1.PrivateKey, error) {
	key, err := LoadKey(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(expected) == "" {
		return key, nil
	}

	want, err := client.HexToAddress(expected)
	if err != nil {
		return nil, errors.Wrapf(err, "evm keystore: invalid expected address [%s]", expected)
	}
	if got := eip712.PubKeyToAddress(key.PubKey()); got != want {
		return nil, errors.Errorf(
			"evm keystore: the key at [%s] derives address %s, configuration says %s", path, got, want)
	}

	return key, nil
}

// decodeKeyScalar parses the hex scalar, tolerating a 0x prefix and trailing newline.
func decodeKeyScalar(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")

	scalar, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.Wrap(err, "key must be hex encoded")
	}
	if len(scalar) != privateKeyLength {
		return nil, errors.Errorf("key must be %d bytes, got %d", privateKeyLength, len(scalar))
	}

	return scalar, nil
}
