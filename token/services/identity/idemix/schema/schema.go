/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package schema

import (
	bccsp "github.com/IBM/idemix/bccsp/types"
	"github.com/IBM/idemix/msp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// How to create counterfeiters in case the corresponding code changes
//go:generate counterfeiter -o ../mock/bccsp.go -fake-name BCCSP github.com/IBM/idemix/bccsp/types.BCCSP
//go:generate counterfeiter -o ../mock/key.go -fake-name Key github.com/IBM/idemix/bccsp/types.Key
//go:generate counterfeiter -o ../mock/schema_manager.go -fake-name SchemaManager . Manager

// Manager handles the various credential schemas. A credential schema
// contains information about the number of attributes, which attributes
// must be disclosed when creating proofs, the format of the attributes etc.
type Manager interface {
	// EidNymAuditOpts returns the options that must be used to audit an enrollment ID pseudonym
	EidNymAuditOpts(schema string, attrs [][]byte) (*bccsp.EidNymAuditOpts, error)
	// RhNymAuditOpts returns the options that must be used to audit a revocation handle pseudonym
	RhNymAuditOpts(schema string, attrs [][]byte) (*bccsp.RhNymAuditOpts, error)
}

const (
	eidIdx = 2
	rhIdx  = 3
	skIdx  = 0

	// w3cEidIdx and w3cRhIdx are the enrollment-id and revocation-handle positions in the
	// W3CSchema attribute list (see attributeNames, offset by one for the hidden usk attribute
	// PublicKeyImportOpts prepends).
	w3cEidIdx = 26
	w3cRhIdx  = 27
	w3cSkIdx  = 24

	DefaultSchema = ""
	// W3CSchema is the identifier of the W3C Verifiable Credentials schema.
	W3CSchema = "w3c-v0.0.1"
)

// attributeAt returns attrs[i], or an error when attrs is too short for the schema that selected
// that index. The audit-opts builders index attrs at positions fixed by the schema name, and the
// schema travels alongside the attributes in a serialized AuditInfo: nothing in the wire format
// makes the two consistent, and callers (crypto.AuditInfo.Validate) only guarantee the four
// entries the default schema needs. Returning an error keeps a mismatched pair a rejected audit
// info instead of an index-out-of-range panic.
func attributeAt(schema string, attrs [][]byte, i int) ([]byte, error) {
	if i < 0 || i >= len(attrs) {
		return nil, errors.Errorf("schema '%s' requires at least %d attributes, got %d", schema, i+1, len(attrs))
	}

	return attrs[i], nil
}

// attributeNames are the attribute names for the `w3c` schema
var attributeNames = []string{
	"_:c14n0 <http://www.w3.",
	"_:c14n0 <https://w3id.o",
	"_:c14n0 <https://w3id.o",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<did:key:z6MknntgQWCT8Z",
	"<https://issuer.oidp.us",
	"<https://issuer.oidp.us",
	"<https://issuer.oidp.us",
	"<https://issuer.oidp.us",
	"<https://issuer.oidp.us",
	"<https://issuer.oidp.us",
	"<https://issuer.oidp.us",
	"_:c14n0 <cbdccard:2_ou>",
	"_:c14n0 <cbdccard:3_rol",
	"_:c14n0 <cbdccard:4_eid",
	"_:c14n0 <cbdccard:5_rh>",
}

// DefaultManager manages the fabric schemas, returning various attribute options types
//
// DefaultSchema (""):
// - 4 attributes: OU (Organizational Unit), Role (ADMIN, MEMBER, ...), EID (enrollment ID), RH (revocation handle))
// - all in bytes format except for Role
// - fixed positions
// - no other attributes
// - a "hidden" usk attribute at position 0
//
// W3C Verifiable Credentials ("w3c-v0.0.1")
// - 27 attributes (includinh OU, Role, EID, RH, and others - see above list)
type DefaultManager struct {
}

func NewDefaultManager() *DefaultManager {
	return &DefaultManager{}
}

// Returns the options for signing with pseudonyms
func (*DefaultManager) NymSignerOpts(schema string) (*bccsp.IdemixNymSignerOpts, error) {
	switch schema {
	case DefaultSchema:
		return &bccsp.IdemixNymSignerOpts{}, nil
	case W3CSchema:
		return &bccsp.IdemixNymSignerOpts{
			SKIndex: w3cSkIdx,
		}, nil
	}

	return nil, errors.Errorf("unsupported schema '%s' for NymSignerOpts", schema)
}

// Returns the options for importing issuer public keys (with the attribute names)
func (*DefaultManager) PublicKeyImportOpts(schema string) (*bccsp.IdemixIssuerPublicKeyImportOpts, error) {
	switch schema {
	case DefaultSchema:
		return &bccsp.IdemixIssuerPublicKeyImportOpts{
			Temporary: true,
			AttributeNames: []string{
				msp.AttributeNameOU,
				msp.AttributeNameRole,
				msp.AttributeNameEnrollmentId,
				msp.AttributeNameRevocationHandle,
			},
		}, nil
	case W3CSchema:
		return &bccsp.IdemixIssuerPublicKeyImportOpts{
			Temporary:      true,
			AttributeNames: append([]string{""}, attributeNames...),
		}, nil
	}

	return nil, errors.Errorf("unsupported schema '%s' for PublicKeyImportOpts", schema)
}

// Returns the options for creating signatures/proofs (specifying which attributes are hidden)
func (*DefaultManager) SignerOpts(schema string) (*bccsp.IdemixSignerOpts, error) {
	switch schema {
	case DefaultSchema:
		return &bccsp.IdemixSignerOpts{
			Attributes: []bccsp.IdemixAttribute{
				{Type: bccsp.IdemixHiddenAttribute},
				{Type: bccsp.IdemixHiddenAttribute},
				{Type: bccsp.IdemixHiddenAttribute},
				{Type: bccsp.IdemixHiddenAttribute},
			},
			RhIndex:  rhIdx,
			EidIndex: eidIdx,
		}, nil
	case W3CSchema:
		var idemixAttrs []bccsp.IdemixAttribute
		for i := range attributeNames {
			switch i {
			case 25:
				idemixAttrs = append(idemixAttrs, bccsp.IdemixAttribute{
					Type: bccsp.IdemixHiddenAttribute,
				})
			case 24:
				idemixAttrs = append(idemixAttrs, bccsp.IdemixAttribute{
					Type: bccsp.IdemixHiddenAttribute,
				})
			default:
				idemixAttrs = append(idemixAttrs, bccsp.IdemixAttribute{
					Type: bccsp.IdemixHiddenAttribute,
				})
			}
		}

		return &bccsp.IdemixSignerOpts{
			Attributes:       idemixAttrs,
			RhIndex:          w3cRhIdx,
			EidIndex:         w3cEidIdx,
			SKIndex:          w3cSkIdx,
			VerificationType: bccsp.ExpectEidNymRhNym,
		}, nil
	}

	return nil, errors.Errorf("unsupported schema '%s' for SignerOpts", schema)
}

// Returns the options for auditing revocation handle pseudonyms
func (*DefaultManager) RhNymAuditOpts(schema string, attrs [][]byte) (*bccsp.RhNymAuditOpts, error) {
	switch schema {
	case DefaultSchema:
		rh, err := attributeAt(schema, attrs, rhIdx)
		if err != nil {
			return nil, errors.Wrap(err, "cannot build RhNymAuditOpts")
		}

		return &bccsp.RhNymAuditOpts{
			RhIndex:          rhIdx,
			SKIndex:          skIdx,
			RevocationHandle: string(rh),
		}, nil
	case W3CSchema:
		rh, err := attributeAt(schema, attrs, w3cRhIdx)
		if err != nil {
			return nil, errors.Wrap(err, "cannot build RhNymAuditOpts")
		}

		return &bccsp.RhNymAuditOpts{
			RhIndex:          w3cRhIdx,
			SKIndex:          w3cSkIdx,
			RevocationHandle: string(rh),
		}, nil
	}

	return nil, errors.Errorf("unsupported schema '%s' for RhNymAuditOpts", schema)
}

// Returns options for auditing enrollment ID pseudonyms
func (*DefaultManager) EidNymAuditOpts(schema string, attrs [][]byte) (*bccsp.EidNymAuditOpts, error) {
	switch schema {
	case DefaultSchema:
		eid, err := attributeAt(schema, attrs, eidIdx)
		if err != nil {
			return nil, errors.Wrap(err, "cannot build EidNymAuditOpts")
		}

		return &bccsp.EidNymAuditOpts{
			EidIndex:     eidIdx,
			SKIndex:      skIdx,
			EnrollmentID: string(eid),
		}, nil
	case W3CSchema:
		eid, err := attributeAt(schema, attrs, w3cEidIdx)
		if err != nil {
			return nil, errors.Wrap(err, "cannot build EidNymAuditOpts")
		}

		return &bccsp.EidNymAuditOpts{
			EidIndex:     w3cEidIdx,
			SKIndex:      w3cSkIdx,
			EnrollmentID: string(eid),
		}, nil
	}

	return nil, errors.Errorf("unsupported schema '%s' for EidNymAuditOpts", schema)
}
