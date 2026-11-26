/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"testing"

	math "github.com/IBM/mathlib"
	fv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/stretchr/testify/require"
)

// TestTransferUpgradeWitnessValidate_NilInputs checks that TransferUpgradeWitnessValidate returns an error,
// not a panic, on a nil input, a nil input token, or a nil input commitment.
func TestTransferUpgradeWitnessValidate_NilInputs(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BN254)
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]
	witness := func() *token.UpgradeWitness {
		return &token.UpgradeWitness{
			FabToken:       &fv1.Output{Quantity: "0x10", Type: "ABC", Owner: []byte("owner1")},
			BlindingFactor: curve.NewZrFromInt(1),
		}
	}

	tests := []struct {
		name  string
		input *transfer.ActionInput
	}{
		{"nil input", nil},
		{"nil token", &transfer.ActionInput{UpgradeWitness: witness()}},
		{"nil token data", &transfer.ActionInput{UpgradeWitness: witness(), Token: &token.Token{Owner: []byte("owner1")}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &validator.Context{
				PP:             pp,
				TransferAction: &transfer.Action{Inputs: []*transfer.ActionInput{tc.input}},
			}
			require.NotPanics(t, func() { err = validator.TransferUpgradeWitnessValidate(context.Background(), ctx) })
			require.Error(t, err)
		})
	}
}
