/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/fabtoken"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// OutputMetadata contains a serialization of the issuer of the token.
// type, value and owner of token can be derived from the token itself.
type OutputMetadata fabtoken.Metadata

// Deserialize un-marshals Metadata
func (m *OutputMetadata) Deserialize(b []byte) error {
	typed, err := fabtoken.UnmarshalTypedToken(b)
	if err != nil {
		return errors.Wrapf(err, "failed deserializing metadata")
	}

	return json.Unmarshal(typed.Token, m)
}

// Serialize un-marshals Metadata
func (m *OutputMetadata) Serialize() ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrapf(err, "failed serializing token")
	}

	return fabtoken.WrapMetadataWithType(raw)
}

// Output carries the output of an action
type Output fabtoken.Token

// Serialize marshals a Token
func (t *Output) Serialize() ([]byte, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, errors.Wrapf(err, "failed serializing token")
	}

	return fabtoken.WrapTokenWithType(raw)
}

// Deserialize unmarshals Token
func (t *Output) Deserialize(bytes []byte) error {
	typed, err := fabtoken.UnmarshalTypedToken(bytes)
	if err != nil {
		return errors.Wrapf(err, "failed deserializing token")
	}

	return json.Unmarshal(typed.Token, t)
}

// IsRedeem returns true if the owner of a Token is empty
// todo update interface to account for nil t.Token.Owner and nil t.Token
func (t *Output) IsRedeem() bool {
	return len(t.Owner) == 0
}

func (t *Output) GetOwner() []byte {
	return t.Owner
}

func (t *Output) Validate(checkOwner bool) error {
	if checkOwner && len(t.Owner) == 0 {
		return errors.Errorf("token owner cannot be empty")
	}
	if len(t.Quantity) == 0 {
		return errors.Errorf("token quantity cannot be empty")
	}
	if len(t.Type) == 0 {
		return errors.Errorf("token type cannot be empty")
	}

	return nil
}
