/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package mimc

import (
	"fmt"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	gmimc "github.com/consensys/gnark-crypto/ecc/bls12-381/fr/mimc"
)

// ErrInvalidInputCount is returned when the number of inputs is outside [1, MaxInputs].
var ErrInvalidInputCount = fmt.Errorf("mimc: input count must be between 1 and %d", MaxInputs)

// MaxInputs is the maximum number of field elements Hash accepts.
// This covers all Phase 1 commitment variants: mimc(v, type, r).
const MaxInputs = 3

// Hash computes the mimc hash of 1 to 3 BLS12-381 scalar field elements.
// The function uses gnark-crypto's default mimc parameters for BLS12-381
//
// INVARIANT: Hash(a, b, c) must return the same value as executing
// HashCircuit(api, a, b, c) inside a circuit compiled for BLS12-381.
// This invariant is enforced by TestMimcCrossConsistency.
// If it fails, every proof in the driver fails to verify.
func Hash(inputs ...fr.Element) (fr.Element, error) {
	n := len(inputs)
	if n == 0 || n > MaxInputs {
		return fr.Element{}, fmt.Errorf("%w: got %d", ErrInvalidInputCount, n)
	}

	// hasher is the default mimc hash implementation for BLS12-381 provided by gnark-crypto
	// WriteElement is used to add the inputs to the hasher
	// We iterate through the inputs and add them to the hasher
	hasher := gmimc.NewFieldHasher()
	for _, input := range inputs {
		hasher.WriteElement(input)
	}

	// SumElement is used to compute the hash of the inputs
	// commitment is the final hash of the inputs which is used to represent the token commitments
	// for the driver
	commitment := hasher.SumElement()

	return commitment, nil
}
