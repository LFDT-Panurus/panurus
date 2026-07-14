/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o ../mock/signing_identity.go -fake-name SigningIdentity . SigningIdentity

// SigningIdentity defines the interface for a signing identity.
type SigningIdentity interface {
	driver.SigningIdentity
}

// WrappedSigningIdentity wraps an identity and its corresponding signer.
type WrappedSigningIdentity struct {
	// Identity represents the public identity bytes.
	Identity driver.Identity
	// Signer is the cryptographic signer for this identity.
	Signer driver.Signer
}

// Serialize returns the byte representation of the identity.
func (w *WrappedSigningIdentity) Serialize() ([]byte, error) {
	return w.Identity, nil
}

// Sign signs the provided raw bytes using the underlying signer.
// It returns an error if the signer is not initialized.
func (w *WrappedSigningIdentity) Sign(raw []byte) ([]byte, error) {
	if w.Signer == nil {
		return nil, errors.New("please initialize signing identity in WrappedSigningIdentity")
	}

	return w.Signer.Sign(raw)
}
