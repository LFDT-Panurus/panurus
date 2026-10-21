/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package bn254 provides MiMC hash over the BN254 scalar field.
// It mirrors the parent crypto/mimc package's structure exactly,
// but uses gnark-crypto's bn254/fr/mimc implementation.
package bn254

import (
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	gmimc "github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrInvalidInputCount is returned when the number of inputs is outside [1, MaxInputs].
var ErrInvalidInputCount = errors.Errorf("mimc/bn254: input count must be between 1 and %d", MaxInputs)

// MaxInputs is the maximum number of field elements Hash accepts.
// This covers all Phase 1 commitment variants: mimc(v, type, r).
const MaxInputs = 3

// Hash computes the mimc hash of 1 to 3 BN254 scalar field elements.
// The function uses gnark-crypto's default mimc parameters for BN254.
//
// INVARIANT: Hash(a, b, c) must return the same value as executing
// HashCircuit(api, a, b, c) inside a circuit compiled for BN254.
// This invariant is enforced by TestMimcBN254CrossConsistency.
// If it fails, every proof in the driver fails to verify.
func Hash(inputs ...fr.Element) (fr.Element, error) {
	n := len(inputs)
	if n == 0 || n > MaxInputs {
		return fr.Element{}, errors.Wrapf(ErrInvalidInputCount, "got %d", n)
	}

	// hasher is the default mimc hash implementation for BN254 provided by gnark-crypto
	hasher := gmimc.NewFieldHasher()
	for _, input := range inputs {
		hasher.WriteElement(input)
	}

	commitment := hasher.SumElement()

	return commitment, nil
}
