/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

const conservationTestPrecision = 64

// curve returns the curve used to build the field-element output values in these tests.
func conservationCurve() *math.Curve {
	return math.Curves[math.BLS12_381_BBS_GURVY]
}

// outputMeta serializes a zkatdlog token.Metadata carrying the given cleartext value, as it would
// appear in a transfer output's OutputMetadata blob.
func outputMeta(t *testing.T, value int64) []byte {
	t.Helper()
	c := conservationCurve()
	m := &token.Metadata{
		Type:           "ABC",
		Value:          c.NewZrFromInt(value),
		BlindingFactor: c.NewZrFromInt(7),
	}
	raw, err := m.Serialize()
	require.NoError(t, err)

	return raw
}

// inputMetaAndTokens builds transfer input metadata for the given token IDs together with the
// matching audit-token store entries carrying the given cleartext quantities.
func inputMetaAndTokens(ids []string, quantities []string) ([]*driver.TransferInputMetadata, map[string]*token2.Token) {
	inputs := make([]*driver.TransferInputMetadata, len(ids))
	auditTokens := make(map[string]*token2.Token, len(ids))
	for i, id := range ids {
		tokenID := &token2.ID{TxId: id}
		inputs[i] = &driver.TransferInputMetadata{TokenID: tokenID}
		auditTokens[tokenID.String()] = &token2.Token{Type: "ABC", Quantity: quantities[i]}
	}

	return inputs, auditTokens
}

func TestValidateTransferValueConservation(t *testing.T) {
	t.Run("balanced inputs and outputs pass", func(t *testing.T) {
		inputs, auditTokens := inputMetaAndTokens([]string{"0", "1"}, []string{"25", "35"})
		metadata := &driver.TransferMetadata{
			Inputs: inputs,
			Outputs: []*driver.TransferOutputMetadata{
				{OutputMetadata: outputMeta(t, 40)},
				{OutputMetadata: outputMeta(t, 20)},
			},
		}
		require.NoError(t, validateTransferValueConservation(metadata, auditTokens, conservationTestPrecision))
	})

	t.Run("inflation is rejected", func(t *testing.T) {
		inputs, auditTokens := inputMetaAndTokens([]string{"0", "1"}, []string{"25", "35"})
		metadata := &driver.TransferMetadata{
			Inputs: inputs,
			Outputs: []*driver.TransferOutputMetadata{
				{OutputMetadata: outputMeta(t, 40)},
				{OutputMetadata: outputMeta(t, 21)}, // sum 61 > 60
			},
		}
		err := validateTransferValueConservation(metadata, auditTokens, conservationTestPrecision)
		require.Error(t, err)
		require.Contains(t, err.Error(), "value conservation violated")
	})

	t.Run("deflation is rejected", func(t *testing.T) {
		inputs, auditTokens := inputMetaAndTokens([]string{"0", "1"}, []string{"25", "35"})
		metadata := &driver.TransferMetadata{
			Inputs: inputs,
			Outputs: []*driver.TransferOutputMetadata{
				{OutputMetadata: outputMeta(t, 40)},
				{OutputMetadata: outputMeta(t, 19)}, // sum 59 < 60
			},
		}
		err := validateTransferValueConservation(metadata, auditTokens, conservationTestPrecision)
		require.Error(t, err)
		require.Contains(t, err.Error(), "value conservation violated")
	})

	t.Run("redeem output value is included", func(t *testing.T) {
		// A single redeem (nil-owner) output must still count toward the output sum.
		inputs, auditTokens := inputMetaAndTokens([]string{"0", "1"}, []string{"25", "35"})
		balanced := &driver.TransferMetadata{
			Inputs:  inputs,
			Outputs: []*driver.TransferOutputMetadata{{OutputMetadata: outputMeta(t, 60)}},
		}
		require.NoError(t, validateTransferValueConservation(balanced, auditTokens, conservationTestPrecision))

		unbalanced := &driver.TransferMetadata{
			Inputs:  inputs,
			Outputs: []*driver.TransferOutputMetadata{{OutputMetadata: outputMeta(t, 59)}},
		}
		require.Error(t, validateTransferValueConservation(unbalanced, auditTokens, conservationTestPrecision))
	})

	t.Run("missing audit token is rejected", func(t *testing.T) {
		inputs, _ := inputMetaAndTokens([]string{"0", "1"}, []string{"25", "35"})
		metadata := &driver.TransferMetadata{
			Inputs:  inputs,
			Outputs: []*driver.TransferOutputMetadata{{OutputMetadata: outputMeta(t, 60)}},
		}
		err := validateTransferValueConservation(metadata, map[string]*token2.Token{}, conservationTestPrecision)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("non-64 precision balances correctly", func(t *testing.T) {
		inputs, auditTokens := inputMetaAndTokens([]string{"0", "1"}, []string{"25", "35"})
		metadata := &driver.TransferMetadata{
			Inputs: inputs,
			Outputs: []*driver.TransferOutputMetadata{
				{OutputMetadata: outputMeta(t, 40)},
				{OutputMetadata: outputMeta(t, 20)},
			},
		}
		require.NoError(t, validateTransferValueConservation(metadata, auditTokens, 32))
	})
}
