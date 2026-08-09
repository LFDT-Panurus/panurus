/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

func TestCheckTypeCommitmentHomogeneity_Consistent(t *testing.T) {
	// Build a spend description with a valid TypeCommitment.
	var typeRandomness fr.Element
	_, err := typeRandomness.SetRandom()
	require.NoError(t, err)

	tc, err := snarktoken.ComputeTypeCommitment("USD", typeRandomness)
	require.NoError(t, err)

	spend := decodedSpend{TypeCommitment: tc}
	output := decodedOutput{TypeCommitment: tc}

	err = checkTypeCommitmentHomogeneity(tc, []decodedSpend{spend}, []decodedOutput{output})
	require.NoError(t, err)
}

func TestCheckTypeCommitmentHomogeneity_Mismatch(t *testing.T) {
	var typeRandomness1, typeRandomness2 fr.Element
	_, err := typeRandomness1.SetRandom()
	require.NoError(t, err)
	_, err = typeRandomness2.SetRandom()
	require.NoError(t, err)

	tcUSD, err := snarktoken.ComputeTypeCommitment("USD", typeRandomness1)
	require.NoError(t, err)
	tcEUR, err := snarktoken.ComputeTypeCommitment("EUR", typeRandomness2)
	require.NoError(t, err)

	spend := decodedSpend{TypeCommitment: tcEUR}

	err = checkTypeCommitmentHomogeneity(tcUSD, []decodedSpend{spend}, nil)
	require.ErrorIs(t, err, ErrTypeMismatch)
}

func TestCheckTypeCommitmentHomogeneity_EmptySliceIsVacuouslyValid(t *testing.T) {
	var tc fr.Element
	_, err := tc.SetRandom()
	require.NoError(t, err)

	require.NoError(t, checkTypeCommitmentHomogeneity(tc, nil, nil))
}
