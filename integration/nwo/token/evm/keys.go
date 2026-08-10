/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// Besu's development network pre-funds a fixed set of accounts. The submitter has to pay for gas, so
// it uses one of them rather than a freshly generated key that would hold no ether.
//
// Endorsers do not need funding: they only ever sign EIP-712 digests off chain, and never send a
// transaction, so their keys are generated fresh per network.
const (
	// DevFundedKey is the first pre-funded development account's private key.
	DevFundedKey = "8f2a55949038a9610f50fb23b5883af3b4ecb3c3bb792cbcefbd1542c692be63"
	// DevFundedAddress is the address DevFundedKey derives.
	DevFundedAddress = "0xfe3b557e8fb62b89f4916b721be55ceb828dbd73"
)

// Identity is a generated EVM identity: the address it signs as, and where its key was written.
type Identity struct {
	Address  evmclient.Address
	Keystore string
}

// GenerateIdentity creates a fresh secp256k1 identity and writes the key to dir under name, in the
// hex format the driver's key loader reads.
func GenerateIdentity(dir, name string) (Identity, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Identity{}, errors.Wrapf(err, "evm nwo: failed to create the key directory [%s]", dir)
	}

	scalar := make([]byte, 32)
	if _, err := rand.Read(scalar); err != nil {
		return Identity{}, errors.Wrap(err, "evm nwo: failed to generate key material")
	}
	key := secp256k1.PrivKeyFromBytes(scalar)
	if key.Key.IsZero() {
		return Identity{}, errors.New("evm nwo: generated the zero scalar")
	}

	path := filepath.Join(dir, name+".key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(scalar)), 0o600); err != nil {
		return Identity{}, errors.Wrapf(err, "evm nwo: failed to write the key for [%s]", name)
	}

	return Identity{Address: eip712.PubKeyToAddress(key.PubKey()), Keystore: path}, nil
}

// WriteFundedSubmitter writes the pre-funded development key so a node can pay for transactions, and
// returns the identity it corresponds to.
func WriteFundedSubmitter(dir, name string) (Identity, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Identity{}, errors.Wrapf(err, "evm nwo: failed to create the key directory [%s]", dir)
	}
	path := filepath.Join(dir, name+".key")
	if err := os.WriteFile(path, []byte(DevFundedKey), 0o600); err != nil {
		return Identity{}, errors.Wrapf(err, "evm nwo: failed to write the submitter key for [%s]", name)
	}
	address, err := evmclient.HexToAddress(DevFundedAddress)
	if err != nil {
		return Identity{}, err
	}

	return Identity{Address: address, Keystore: path}, nil
}
