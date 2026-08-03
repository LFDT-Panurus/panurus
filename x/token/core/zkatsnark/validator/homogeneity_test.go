/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckTypeHomogeneitySpend_Consistent(t *testing.T) {
	d := validSpendBytes(t)
	decoded, err := decodeSpendDescription(0, d)
	require.NoError(t, err)

	err = checkTypeHomogeneitySpend("USD", []decodedSpend{decoded})

	require.NoError(t, err)
}

func TestCheckTypeHomogeneitySpend_Mismatch(t *testing.T) {
	d := validSpendBytes(t)
	decoded, err := decodeSpendDescription(0, d)
	require.NoError(t, err)

	err = checkTypeHomogeneitySpend("EUR", []decodedSpend{decoded})

	require.ErrorIs(t, err, ErrTypeMismatch)
}

func TestCheckTypeHomogeneity_EmptySliceIsVacuouslyValid(t *testing.T) {
	require.NoError(t, checkTypeHomogeneitySpend("USD", nil))
	require.NoError(t, checkTypeHomogeneityOutput("USD", nil))
}
