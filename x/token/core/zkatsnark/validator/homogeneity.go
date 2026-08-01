/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// checkTypeHomogeneitySpend confirms every input's decoded TokenType
// matches EncodeTokenType(declaredType), the same function token/note.go
// uses everywhere else, called directly rather than reimplemented, so the
// encoding this check compares against can never silently drift from what
// the rest of the system actually produces.
func checkTypeHomogeneitySpend(declaredType string, decoded []decodedSpend) error {
	expected := snarktoken.EncodeTokenType(declaredType)
	for i, d := range decoded {
		if !d.TokenType.Equal(&expected) {
			return errors.Wrapf(ErrTypeMismatch, "input %d: token type does not match declared type %q", i, declaredType)
		}
	}
	return nil
}

// checkTypeHomogeneityOutput is the OutputDescription equivalent.
func checkTypeHomogeneityOutput(declaredType string, decoded []decodedOutput) error {
	expected := snarktoken.EncodeTokenType(declaredType)
	for j, d := range decoded {
		if !d.TokenType.Equal(&expected) {
			return errors.Wrapf(ErrTypeMismatch, "output %d: token type does not match declared type %q", j, declaredType)
		}
	}
	return nil
}
