/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func DeserializeVerifier(id driver.Identity) (driver.Verifier, error) {
	genericPublicKey, err := PemDecodeKey(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed parsing received public key")
	}
	publicKey, ok := genericPublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("expected *ecdsa.PublicKey")
	}

	return NewECDSAVerifier(publicKey), nil
}

func Info(raw []byte) (string, error) {
	cert, err := PemDecodeCert(raw)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("X509: [%s][%s]", driver.Identity(raw).UniqueID(), cert.Subject.CommonName), nil
}
