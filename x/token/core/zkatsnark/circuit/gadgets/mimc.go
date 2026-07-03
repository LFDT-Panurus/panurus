/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gadgets

import (
	"fmt"

	"github.com/consensys/gnark/frontend"
	stdmimc "github.com/consensys/gnark/std/hash/mimc"
)

// HashCircuit computes the MiMC hash of 1 to 3 circuit variables.
//
// This is the in-circuit counterpart of crypto/mimc/hash.go's Hash function.
// Both use the same Miyaguchi-Preneel construction over BLS12-381 with the
// same round constants derived from "seed" via Keccak256. This structural
// identity guarantees the cross-consistency invariant holds.
//
// INVARIANT: For any field elements a, b, c:
//
//	mimc.Hash(a, b, c) == HashCircuit(api, a, b, c)  [when circuit is BLS12-381]
func HashCircuit(api frontend.API, inputs ...frontend.Variable) (frontend.Variable, error) {
	n := len(inputs)
	if n == 0 || n > 3 {
		return nil, fmt.Errorf("mimc gadget: input count must be between 1 and 3, got %d", n)
	}

	// NewMiMC constructs a MiMC hasher for the circuit's scalar field.
	// When the circuit is compiled for BLS12-381, it uses BLS12-381 MiMC constants —
	// the same constants used by gnark-crypto's native implementation.
	h, err := stdmimc.NewMiMC(api)
	if err != nil {
		return nil, fmt.Errorf("mimc gadget: failed to create MiMC instance: %w", err)
	}

	// Write all inputs to the hasher sequentially.
	// This matches the native side's for-loop over WriteElement calls.
	h.Write(inputs...)

	// Sum() finalises the hash and returns the result as a frontend.Variable.
	// This matches SumElement() on the native side.
	hash := h.Sum()

	return hash, nil
}
