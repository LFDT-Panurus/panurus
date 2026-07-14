/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ParseFabtokenToken unmarshals a raw fabtoken output and converts its quantity to a uint64 value
// using maxPrecision.
func ParseFabtokenToken(tok []byte, maxPrecision uint64) (*actions.Output, uint64, error) {
	output := &actions.Output{}
	err := output.Deserialize(tok)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to unmarshal fabtoken")
	}
	q, err := token.NewUBigQuantity(output.Quantity, maxPrecision)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to create quantity")
	}

	return output, q.Uint64(), nil
}
