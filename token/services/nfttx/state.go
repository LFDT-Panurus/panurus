/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"bytes"
	"encoding/base64"

	"github.com/LFDT-Panurus/panurus/token/services/nfttx/marshaller"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// decodeState decodes the base64 state held in an NFT's type into state. On error
// state is left unusable: empty or half-written.
func decodeState(typ token2.Type, state any) error {
	decoded, err := base64.StdEncoding.DecodeString(string(typ))
	if err != nil {
		return errors.Wrap(err, "failed to decode type")
	}
	// null unmarshals into anything without writing to it, which would be a false success
	if bytes.Equal(bytes.TrimSpace(decoded), []byte("null")) {
		return errors.New("type carries no state")
	}
	if err := marshaller.Unmarshal(decoded, state); err != nil {
		return errors.Wrap(err, "failed to unmarshal state")
	}

	return nil
}

// LinearState models a state with a unique identifier that does not change through the evolution
// of the state.
type LinearState interface {
	// SetLinearID assigns the passed id to the state
	SetLinearID(id string) string
}

type AutoLinearState interface {
	GetLinearID() (string, error)
}
