/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtoken

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// Type is the identifier for the Fabtoken (cleartext) token representation.
	Type driver.Type = 1
)

// Token represents a token where its type, value, and owner are stored in the clear on the ledger.
// This matches the default Hyperledger Fabric token implementation (Fabtoken).
type Token = token2.Token

// Metadata contains information associated with a Fabtoken, primarily the identity of the issuer.
// Since the token itself is in the clear, metadata is used for supplemental information like issuer verification.
type Metadata struct {
	// Issuer is the serialized identity of the entity that issued the token.
	Issuer []byte
}

// WrapTokenWithType serializes the token and wraps it with the Fabtoken type identifier.
func WrapTokenWithType(token driver.Token) (driver.Token, error) {
	return tokens.WrapWithType(Type, token)
}

// UnmarshalTypedToken deserializes a byte slice into a TypedToken and verifies it matches the Fabtoken type.
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

// WrapMetadataWithType serializes the metadata and wraps it with the Fabtoken type identifier.
func WrapMetadataWithType(metadata driver.Metadata) (driver.Metadata, error) {
	return tokens.WrapMetadataWithType(Type, metadata)
}

// UnmarshalTypedMetadata deserializes a byte slice into a TypedMetadata and verifies it matches the Fabtoken type.
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

// UnmarshalToken deserializes raw bytes into a cleartext Token structure.
func UnmarshalToken(raw []byte) (*Token, error) {
	token := &Token{}
	if err := json.Unmarshal(raw, &token); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling token")
	}

	return token, nil
}
