/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

import (
	"encoding/asn1"

	"github.com/LFDT-Panurus/panurus/token/services/tokens/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TypedToken encodes a token representation along with its type identifier.
// This structure is used to support multiple cryptographic token formats in a unified way.
type TypedToken struct {
	// Type encodes the token's version or format type (e.g., Fabtoken, Commitment).
	Type driver.Type
	// Token encodes the raw byte representation of the token itself.
	Token driver.Token
}

// Bytes serializes the TypedToken into its ASN.1 byte representation.
func (i TypedToken) Bytes() ([]byte, error) {
	return asn1.Marshal(i)
}

// UnmarshalTypedToken deserializes an ASN.1 encoded byte slice into a TypedToken structure.
func UnmarshalTypedToken(token driver.Token) (*TypedToken, error) {
	si := &TypedToken{}
	_, err := asn1.Unmarshal(token, si)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedToken")
	}

	return si, nil
}

// WrapWithType creates a new TypedToken with the given type and raw representation, and returns its serialized bytes.
func WrapWithType(typ driver.Type, id driver.Token) (driver.Token, error) {
	raw, err := (&TypedToken{Type: typ, Token: id}).Bytes()
	if err != nil {
		return nil, err
	}

	return raw, nil
}

// TypedMetadata encodes token metadata along with its type identifier.
type TypedMetadata struct {
	// Type encodes the metadata version or format type.
	Type driver.Type
	// Metadata encodes the raw byte representation of the metadata itself.
	Metadata driver.Metadata
}

// Bytes serializes the TypedMetadata into its ASN.1 byte representation.
func (i TypedMetadata) Bytes() ([]byte, error) {
	return asn1.Marshal(i)
}

// UnmarshalTypedMetadata deserializes an ASN.1 encoded byte slice into a TypedMetadata structure.
func UnmarshalTypedMetadata(metadata driver.Metadata) (*TypedMetadata, error) {
	si := &TypedMetadata{}
	_, err := asn1.Unmarshal(metadata, si)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedMetadata")
	}

	return si, nil
}

// WrapMetadataWithType creates a new TypedMetadata with the given type and raw representation, and returns its serialized bytes.
func WrapMetadataWithType(typ driver.Type, id driver.Metadata) (driver.Metadata, error) {
	raw, err := (&TypedMetadata{Type: typ, Metadata: id}).Bytes()
	if err != nil {
		return nil, err
	}

	return raw, nil
}
