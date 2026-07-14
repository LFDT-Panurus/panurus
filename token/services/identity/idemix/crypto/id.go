/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	bccsp "github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/schema"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

const (
	// EIDIndex is the attribute index for the enrollment ID in Idemix credentials.
	EIDIndex = 2
	// RHIndex is the attribute index for the revocation handle in Idemix credentials.
	RHIndex = 3
	// SignerConfigFull is the filename for the full signer configuration (including secret key).
	SignerConfigFull = "SignerConfigFull"
)

var logger = logging.MustGetLogger()

// Identity represents an Idemix identity with pseudonym and cryptographic proof.
type Identity struct {
	// Pseudonym public key
	NymPublicKey bccsp.Key
	// Deserializer with issuer info and crypto provider
	Idemix *Deserializer
	// Cryptographic proof of validity
	AssociationProof []byte
	// Verification type for signatures
	VerificationType bccsp.VerificationType
	// Schema-specific operations manager
	SchemaManager schema.Manager
	// Credential schema version
	Schema Schema
}

// NewIdentity creates a new Idemix identity.
func NewIdentity(idemix *Deserializer, nymPublicKey bccsp.Key, proof []byte, verificationType bccsp.VerificationType, schemaManager schema.Manager, schema Schema) *Identity {
	return &Identity{
		Idemix:           idemix,
		NymPublicKey:     nymPublicKey,
		AssociationProof: proof,
		VerificationType: verificationType,
		SchemaManager:    schemaManager,
		Schema:           schema,
	}
}

// Validate checks that the cryptographic proof is valid.
func (id *Identity) Validate() error {
	return id.verifyProof()
}

// Verify checks that a signature was created by this identity's pseudonym.
func (id *Identity) Verify(msg []byte, sig []byte) error {
	_, err := id.Idemix.Csp.Verify(
		id.NymPublicKey,
		sig,
		msg,
		&bccsp.IdemixNymSignerOpts{
			IssuerPK: id.Idemix.IssuerPublicKey,
		},
	)

	return err
}

// Serialize converts the identity to protobuf wire format.
func (id *Identity) Serialize() ([]byte, error) {
	serialized := &SerializedIdemixIdentity{}

	raw, err := id.NymPublicKey.Bytes()
	if err != nil {
		return nil, errors.Wrapf(err, "could not serialize nym")
	}
	serialized.NymPublicKey = raw
	serialized.Proof = id.AssociationProof
	serialized.Schema = id.Schema

	idemixIDBytes, err := proto.Marshal(serialized)
	if err != nil {
		return nil, errors.Wrapf(err, "could not serialize idemix identity")
	}

	return idemixIDBytes, nil
}

// verifyProof validates the id's association proof against the issuer's public key.
func (id *Identity) verifyProof() error {
	// Verify signature
	var metadata *bccsp.IdemixSignerMetadata
	if len(id.Idemix.NymEID) != 0 {
		metadata = &bccsp.IdemixSignerMetadata{
			EidNym: id.Idemix.NymEID,
			RhNym:  id.Idemix.RhNym,
		}
	}

	valid, err := id.Idemix.Csp.Verify(
		id.Idemix.IssuerPublicKey,
		id.AssociationProof,
		nil,
		&bccsp.IdemixSignerOpts{
			RevocationPublicKey: id.Idemix.RevocationPK,
			Attributes: []bccsp.IdemixAttribute{
				{Type: bccsp.IdemixHiddenAttribute},
				{Type: bccsp.IdemixHiddenAttribute},
				{Type: bccsp.IdemixHiddenAttribute},
				{Type: bccsp.IdemixHiddenAttribute},
			},
			RhIndex:          RHIndex,
			EidIndex:         EIDIndex,
			Epoch:            id.Idemix.Epoch,
			VerificationType: id.VerificationType,
			Metadata:         metadata,
		},
	)
	if err == nil && !valid {
		return errors.Errorf("unexpected condition, an error should be returned for an invalid signature")
	}

	return err
}

// SigningIdentity extends Identity with signing capabilities.
type SigningIdentity struct {
	// Embedded base identity
	*Identity `json:"-"`
	// Crypto provider with secret keys
	CSP bccsp.BCCSP `json:"-"`
	// Enrollment identifier
	EnrollmentId string
	// Pseudonym secret key identifier
	NymKeySKI []byte
	// User secret key identifier
	UserKeySKI []byte
}

// Sign creates a signature using secret keys.
func (id *SigningIdentity) Sign(msg []byte) ([]byte, error) {
	nymKey, err := id.CSP.GetKey(id.NymKeySKI)
	if err != nil {
		return nil, errors.Wrap(err, "cannot find nym secret key")
	}

	// Load the user key
	userKey, err := id.CSP.GetKey(id.UserKeySKI)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve user key with ski [%s]", id.UserKeySKI)
	}

	sig, err := id.Idemix.Csp.Sign(
		userKey,
		msg,
		&bccsp.IdemixNymSignerOpts{
			Nym:      nymKey,
			IssuerPK: id.Idemix.IssuerPublicKey,
		},
	)
	if err != nil {
		return nil, err
	}

	return sig, nil
}

// NymSignatureVerifier verifies signatures created with Idemix pseudonym keys.
type NymSignatureVerifier struct {
	// Crypto provider for verification
	CSP bccsp.BCCSP
	// Issuer public key
	IPK bccsp.Key
	// Pseudonym public key
	NymPK bccsp.Key
	// Schema operations manager
	SchemaManager schema.Manager
	// Credential schema version
	Schema Schema
}

// Verify checks that a signature was created by the pseudonym key.
func (v *NymSignatureVerifier) Verify(message, sigma []byte) error {
	_, err := v.CSP.Verify(
		v.NymPK,
		sigma,
		message,
		&bccsp.IdemixNymSignerOpts{
			IssuerPK: v.IPK,
		},
	)

	return err
}
