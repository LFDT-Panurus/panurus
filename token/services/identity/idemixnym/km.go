/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"context"
	"encoding/json"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/nym"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	IdentityType       = tdriver.IdemixNymIdentityType
	IdentityTypeString = tdriver.IdemixNymIdentityTypeString
)

type IdentityStoreService interface {
	// GetSignerInfo returns the signer info bound to the given identity
	GetSignerInfo(ctx context.Context, id []byte) ([]byte, error)
}

type KeyManager struct {
	backend              *idemix.KeyManager
	identityStoreService IdentityStoreService
}

func NewKeyManager(backend *idemix.KeyManager, identityStoreService IdentityStoreService) *KeyManager {
	return &KeyManager{
		backend:              backend,
		identityStoreService: identityStoreService,
	}
}

func (k *KeyManager) DeserializeVerifier(ctx context.Context, raw []byte) (tdriver.Verifier, error) {
	return &nym.Verifier{
		NymEID: raw,
		Backed: k.backend.Deserializer,
	}, nil
}

func (k *KeyManager) DeserializeSigner(ctx context.Context, raw []byte) (tdriver.Signer, error) {
	signerInfoRaw, err := k.identityStoreService.GetSignerInfo(ctx, raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve signer info")
	}
	auditInfo := &nym.AuditInfo{}
	if err := json.Unmarshal(signerInfoRaw, auditInfo); err != nil {
		return nil, errors.Wrapf(err, "failed to deserialize audit info")
	}

	signer, err := k.backend.DeserializeSigner(ctx, auditInfo.IdemixSignature)
	if err != nil {
		return nil, errors.Wrap(err, "cannot deserializer signer, cannot create signer")
	}

	return &nym.Signer{
		Creator: auditInfo.IdemixSignature,
		Signer:  signer,
	}, nil
}

func (k *KeyManager) EnrollmentID() string {
	return k.backend.EnrollmentID()
}

func (k *KeyManager) IsRemote() bool {
	return k.backend.IsRemote()
}

func (k *KeyManager) Anonymous() bool {
	return k.backend.Anonymous()
}

func (k *KeyManager) IdentityType() idriver.IdentityType {
	return IdentityType
}

func (k *KeyManager) Identity(ctx context.Context, referenceAuditInfo []byte) (*idriver.IdentityDescriptor, error) {
	var backendAuditInfoRaw []byte
	if len(referenceAuditInfo) != 0 {
		// extract the audit infor for the backed
		auditInfo, err := nym.DeserializeAuditInfo(referenceAuditInfo)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to deserialize audit info")
		}
		backendAuditInfoRaw, err = auditInfo.Bytes()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to serialize audit info")
		}
	}

	descriptor, err := k.backend.Identity(ctx, backendAuditInfoRaw)
	if err != nil {
		return nil, err
	}

	// compile options and check for idemix
	ai, err := k.backend.DeserializeAuditInfo(ctx, descriptor.AuditInfo)
	if err != nil {
		return nil, err
	}

	auditInfo := &nym.AuditInfo{
		AuditInfo:       ai,
		IdemixSignature: descriptor.Identity,
	}
	auditInfoRaw, err := json.Marshal(auditInfo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal audit info")
	}

	return &idriver.IdentityDescriptor{
		// Commitment to the Enrollment ID is the new identity
		Identity:  ai.EidNymAuditData.Nym.Bytes(),
		AuditInfo: auditInfoRaw,
		Signer: &nym.Signer{
			Creator: descriptor.Identity,
			Signer:  descriptor.Signer,
		},
		SignerInfo: auditInfoRaw,
		Verifier: &nym.Verifier{
			NymEID: ai.EidNymAuditData.Nym.Bytes(),
			Backed: k.backend.Deserializer,
		},
		Ephemeral: false,
	}, nil
}

func (k *KeyManager) DeserializeAuditInfo(ctx context.Context, raw []byte) (*nym.AuditInfo, error) {
	ai, err := nym.DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize audit info")
	}
	ai.Csp = k.backend.Csp
	ai.IssuerPublicKey = k.backend.IssuerPublicKey
	ai.SchemaManager = k.backend.SchemaManager
	ai.Schema = k.backend.Schema

	return ai, nil
}

// DeserializeSigningIdentity deserializes a signing identity from the given raw bytes
func (k *KeyManager) DeserializeSigningIdentity(ctx context.Context, raw []byte) (tdriver.SigningIdentity, error) {
	signer, err := k.DeserializeSigner(ctx, raw)
	if err != nil {
		return nil, err
	}

	return &signerIdentity{
		id:     raw,
		signer: signer,
	}, nil
}

type signerIdentity struct {
	id     []byte
	signer tdriver.Signer
}

func (s *signerIdentity) Sign(raw []byte) ([]byte, error) {
	return s.signer.Sign(raw)
}

func (s *signerIdentity) Serialize() ([]byte, error) {
	return s.id, nil
}
