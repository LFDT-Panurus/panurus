/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// checkTypeCommitmentHomogeneity verifies that every input and output in the
// action shares the same TypeCommitment value. Since the ZK proofs guarantee
// that each TypeCommitment correctly opens to the actual token type, equal
// commitments ⟹ equal types. No plaintext type string is needed.
//
// actionTC is the TypeCommitment from the action envelope (TransferAction or
// IssueAction). Each individual input/output TypeCommitment must match it.
func checkTypeCommitmentHomogeneity(actionTC fr.Element, decodedInputs []decodedSpend, decodedOutputs []decodedOutput) error {
	for i, d := range decodedInputs {
		if !d.TypeCommitment.Equal(&actionTC) {
			return errors.Wrapf(ErrTypeMismatch, "input %d: TypeCommitment does not match action's TypeCommitment", i)
		}
	}

	for j, d := range decodedOutputs {
		if !d.TypeCommitment.Equal(&actionTC) {
			return errors.Wrapf(ErrTypeMismatch, "output %d: TypeCommitment does not match action's TypeCommitment", j)
		}
	}

	return nil
}
