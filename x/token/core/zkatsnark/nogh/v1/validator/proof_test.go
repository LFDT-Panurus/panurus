/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"
)

// TestPublicWitnessForOutput_BindsAllPublicInputs checks that the public
// witness the validator hands to Verify carries all four OutputCircuit
// public inputs, in declaration order. If the circuit's public tags are
// malformed, frontend.PublicOnly() silently yields an empty witness and the
// output proof binds nothing.
func TestPublicWitnessForOutput_BindsAllPublicInputs(t *testing.T) {
	d := decodedOutput{
		Commitment:     big.NewInt(11),
		ValueCommitX:   big.NewInt(22),
		ValueCommitY:   big.NewInt(33),
		TypeCommitment: big.NewInt(44),
	}

	for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
		t.Run(curve.String(), func(t *testing.T) {
			w, err := publicWitnessForOutput(d, curve)
			require.NoError(t, err)

			// A public-only witness holds just the public values.
			require.Equal(t, 4, reflect.ValueOf(w.Vector()).Len())
		})
	}
}

// TestPublicWitnessForSpend_BindsAllPublicInputs is the SpendCircuit
// counterpart of TestPublicWitnessForOutput_BindsAllPublicInputs.
func TestPublicWitnessForSpend_BindsAllPublicInputs(t *testing.T) {
	d := decodedSpend{
		Commitment:     big.NewInt(11),
		ValueCommitX:   big.NewInt(22),
		ValueCommitY:   big.NewInt(33),
		TypeCommitment: big.NewInt(44),
	}

	for _, curve := range []ecc.ID{ecc.BLS12_381, ecc.BN254} {
		t.Run(curve.String(), func(t *testing.T) {
			w, err := publicWitnessForSpend(d, curve)
			require.NoError(t, err)

			// A public-only witness holds just the public values.
			require.Equal(t, 4, reflect.ValueOf(w.Vector()).Len())
		})
	}
}
