/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"math/big"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// BigInt is a custom wrapper around math/big.Int that implements
// sql.Scanner for reading NUMERIC values from the DB
type BigInt struct {
	*big.Int
}

// Scan implements the sql.Scanner interface for reading from the DB
func (b *BigInt) Scan(value any) error {
	if value == nil {
		b.Int = new(big.Int)
		b.SetInt64(0)

		return nil
	}

	var str string
	switch v := value.(type) {
	case []byte:
		str = string(v)
	case string:
		str = v
	case int64:
		b.Int = new(big.Int)
		b.SetInt64(v)

		return nil
	default:

		return errors.Errorf("cannot scan type %T into BigInt", value)
	}

	if b.Int == nil {
		b.Int = new(big.Int)
	}
	if _, ok := b.SetString(str, 10); !ok {
		return errors.Errorf("failed to parse NUMERIC %q into big.Int", str)
	}

	return nil
}
