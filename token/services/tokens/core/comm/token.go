/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package comm

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// Type is the identifier for the commitment-based token representation.
	Type driver.Type = 2
)

// Token represents a token using Pedersen commitments to hide its type and value.
// This is typically used in privacy-preserving drivers like ZKAT-DLOG.
type Token struct {
	// Owner is the serialized identity of the token owner.
	Owner []byte
	// Data is a Pedersen commitment (G1 group element) to the token's type and value.
	Data *math.G1
}

// Metadata contains the opening information (blinding factors, type, value) for a commitment-based token.
// Possessing this metadata allows an entity to "de-obfuscate" the token.
type Metadata struct {
	// Type is the de-obfuscated type of the token.
	Type token2.Type
	// Value is the de-obfuscated quantity of the token, represented as a scalar (math.Zr).
	Value *math.Zr
	// BlindingFactor is the randomness used to create the Pedersen commitment.
	BlindingFactor *math.Zr
	// Issuer is the serialized identity of the token issuer, if applicable.
	Issuer []byte
}

// WrapTokenWithType serializes the token and wraps it with the commitment type identifier.
func WrapTokenWithType(token driver.Token) (driver.Token, error) {
	return tokens.WrapWithType(Type, token)
}

// UnmarshalTypedToken deserializes a byte slice into a TypedToken and verifies it matches the commitment type.
func UnmarshalTypedToken(token driver.Token) (*tokens.TypedToken, error) {
	ttoken, err := tokens.UnmarshalTypedToken(token)
	if err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling token")
	}
	if ttoken.Type != Type {
		return nil, errors.Errorf("invalid token type [%v]", ttoken.Type)
	}

	return ttoken, nil
}

// WrapMetadataWithType serializes the metadata and wraps it with the commitment type identifier.
func WrapMetadataWithType(metadata driver.Metadata) (driver.Metadata, error) {
	return tokens.WrapMetadataWithType(Type, metadata)
}

// UnmarshalTypedMetadata deserializes a byte slice into a TypedMetadata and verifies it matches the commitment type.
func UnmarshalTypedMetadata(metadata driver.Metadata) (*tokens.TypedMetadata, error) {
	tmetadata, err := tokens.UnmarshalTypedMetadata(metadata)
	if err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling metadata")
	}
	if tmetadata.Type != Type {
		return nil, errors.Errorf("invalid metadata type [%v]", tmetadata.Type)
	}

	return tmetadata, nil
}
