/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

func TestCheckTypeCommitmentHomogeneity_Consistent(t *testing.T) {
	// Build a spend description with a valid TypeCommitment.
	var typeRandomness fr.Element
	_, err := typeRandomness.SetRandom()
	require.NoError(t, err)

	tr := typeRandomness.Bytes()
	tcBytes, err := snarktoken.ComputeTypeCommitment("USD", tr[:], ecc.BLS12_381)
	require.NoError(t, err)

	tc := new(big.Int).SetBytes(tcBytes)

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

	tr1 := typeRandomness1.Bytes()
	tr2 := typeRandomness2.Bytes()
	tcUSDBytes, err := snarktoken.ComputeTypeCommitment("USD", tr1[:], ecc.BLS12_381)
	require.NoError(t, err)
	tcEURBytes, err := snarktoken.ComputeTypeCommitment("EUR", tr2[:], ecc.BLS12_381)
	require.NoError(t, err)

	tcUSD, tcEUR := new(big.Int).SetBytes(tcUSDBytes), new(big.Int).SetBytes(tcEURBytes)

	spend := decodedSpend{TypeCommitment: tcEUR}

	err = checkTypeCommitmentHomogeneity(tcUSD, []decodedSpend{spend}, nil)
	require.ErrorIs(t, err, ErrTypeMismatch)
}

func TestCheckTypeCommitmentHomogeneity_EmptySliceIsVacuouslyValid(t *testing.T) {
	require.NoError(t, checkTypeCommitmentHomogeneity(big.NewInt(42), nil, nil))
}
