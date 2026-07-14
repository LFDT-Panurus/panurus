/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"context"

	bccsp "github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/schema"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

// DeserializedIdentity contains a deserialized Idemix identity and its nym public key.
type DeserializedIdentity struct {
	// Deserialized and validated identity
	Identity *Identity
	// Pseudonym public key
	NymPublicKey bccsp.Key
}

// Deserializer handles deserialization and validation of Idemix identities.
type Deserializer struct {
	// Deserializer identifier
	Name string
	// Issuer public key (raw bytes)
	Ipk []byte
	// Cryptographic service provider
	Csp bccsp.BCCSP
	// Parsed issuer public key
	IssuerPublicKey bccsp.Key
	// Revocation public key
	RevocationPK bccsp.Key
	// Credential epoch
	Epoch int
	// Verification type
	VerType bccsp.VerificationType
	// Enrollment ID pseudonym
	NymEID []byte
	// Revocation handle pseudonym
	RhNym []byte
	// Schema manager
	SchemaManager schema.Manager
	// Credential schema version
	Schema string
}

// Deserialize deserializes and validates an Idemix identity from raw bytes.
func (d *Deserializer) Deserialize(_ context.Context, raw []byte) (*DeserializedIdentity, error) {
	return d.DeserializeAgainstNymEID(raw, nil)
}

// DeserializeAgainstNymEID deserializes and optionally validates against a specific EID nym.
func (d *Deserializer) DeserializeAgainstNymEID(identity []byte, nymEID []byte) (*DeserializedIdentity, error) {
	if len(identity) == 0 {
		return nil, errors.Errorf("empty identity")
	}
	serialized := new(SerializedIdemixIdentity)
	err := proto.Unmarshal(identity, serialized)
	if err != nil {
		return nil, errors.Wrap(err, "could not deserialize a SerializedIdemixIdentity")
	}
	if len(serialized.NymPublicKey) == 0 {
		return nil, errors.Errorf("unable to deserialize idemix identity: pseudonym's public key is empty")
	}

	// match schema
	if serialized.Schema != d.Schema {
		return nil, errors.Errorf("unable to deserialize idemix identity: schema does not match [%s]!=[%s]", serialized.Schema, d.Schema)
	}

	// Import NymPublicKey
	NymPublicKey, err := d.Csp.KeyImport(
		serialized.NymPublicKey,
		&bccsp.IdemixNymPublicKeyImportOpts{Temporary: true},
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to import nym public key")
	}

	idemix := d
	if len(nymEID) != 0 {
		idemix = &Deserializer{
			Name:            d.Name,
			Ipk:             d.Ipk,
			Csp:             d.Csp,
			IssuerPublicKey: d.IssuerPublicKey,
			RevocationPK:    d.RevocationPK,
			Epoch:           d.Epoch,
			VerType:         d.VerType,
			NymEID:          nymEID,
			SchemaManager:   d.SchemaManager,
		}
	}

	id := NewIdentity(idemix, NymPublicKey, serialized.Proof, d.VerType, d.SchemaManager, d.Schema)
	if err := id.Validate(); err != nil {
		return nil, errors.Wrap(err, "cannot deserialize, invalid identity")
	}

	return &DeserializedIdentity{
		Identity:     id,
		NymPublicKey: NymPublicKey,
	}, nil
}

// DeserializeAuditInfo deserializes audit info and populates crypto components.
func (d *Deserializer) DeserializeAuditInfo(_ context.Context, raw []byte) (*AuditInfo, error) {
	ai, err := DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing audit info [%s]", string(raw))
	}
	ai.Csp = d.Csp
	ai.IssuerPublicKey = d.IssuerPublicKey
	ai.SchemaManager = d.SchemaManager
	ai.Schema = d.Schema

	return ai, nil
}
