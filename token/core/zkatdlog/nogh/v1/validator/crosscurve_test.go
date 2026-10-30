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
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/require"
)

// foreignCurveID is any valid math.Curves index other than the one the params use.
const foreignCurveID = math.BLS12_377_GURVY

// Pre-ZK half of #2554: this stage runs before TransferZKProofValidate and is the first
// to touch an input commitment, so the verifier-side guards do not cover it.
func TestTransferUpgradeWitnessRejectsCrossCurveCommitment(t *testing.T) {
	pp, err := v1.Setup(32, nil, math.BN254)
	require.NoError(t, err)
	curve := math.Curves[pp.Curve]
	bf := curve.NewZrFromInt(7)

	newCtx := func(data *math.G1, witnessBF *math.Zr) *validator.Context {
		return &validator.Context{
			Logger: logging.MustGetLogger(),
			PP:     pp,
			TransferAction: &transfer.Action{
				Inputs: []*transfer.ActionInput{{
					Token: &token.Token{Owner: []byte("owner"), Data: data},
					UpgradeWitness: &token.UpgradeWitness{
						FabToken:       &fv1.Output{Owner: []byte("owner"), Type: "ABC", Quantity: "0x10"},
						BlindingFactor: witnessBF,
					},
				}},
			},
			MetadataCounter: make(map[string]int),
		}
	}

	toks, _, err := token.GetTokensWithWitnessAndBF(
		[]uint64{16}, []*math.Zr{bf}, "ABC", pp.PedersenGenerators, curve,
	)
	require.NoError(t, err)
	foreign := math.Curves[foreignCurveID]

	// The witness blinding factor must be checked before the arithmetic, not beside it.
	for _, tc := range []struct {
		name string
		data *math.G1
		bf   *math.Zr
		want string
	}{
		{"foreign curve commitment", foreign.GenG1.Copy(), bf, "invalid commitment in input token"},
		{"infinity commitment", curve.NewG1(), bf, "invalid commitment in input token"},
		{"foreign curve witness blinding factor", toks[0], foreign.NewZrFromInt(7), "invalid blinding factor in upgrade witness"},
		{"nil witness blinding factor", toks[0], nil, "invalid blinding factor in upgrade witness"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				err = validator.TransferUpgradeWitnessValidate(context.Background(), newCtx(tc.data, tc.bf))
			})
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}

	// A right-curve commitment still reaches the ordinary equality check.
	require.NoError(t, validator.TransferUpgradeWitnessValidate(context.Background(), newCtx(toks[0], bf)))
}
