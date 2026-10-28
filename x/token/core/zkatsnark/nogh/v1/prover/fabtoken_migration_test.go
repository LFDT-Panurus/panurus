/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"context"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

func TestBuildFabtokenMigrationWitness_Valid(t *testing.T) {
	p := pp.DefaultPublicParams()
	var tr fr.Element
	_, err := tr.SetRandom()
	require.NoError(t, err)
	trBytes := tr.Bytes()

	opening := snarktoken.FabtokenOpening{
		Value:     150,
		TokenType: "USD",
	}

	res, err := prover.BuildFabtokenMigrationWitness(opening, p, trBytes[:])
	require.NoError(t, err)
	require.NotNil(t, res.Assignment)
	require.NotNil(t, res.Note)
	require.Equal(t, uint64(150), res.Note.Value)
	require.Equal(t, "USD", res.Note.TokenType)
	require.NotEmpty(t, res.FabtokenDigest)
	require.NotEmpty(t, res.Commitment)
	require.NotEmpty(t, res.ValueCommitmentX)
	require.NotEmpty(t, res.ValueCommitmentY)
	require.NotEmpty(t, res.TypeCommitment)
}

func TestBuildFabtokenMigrationWitness_InvalidOpening(t *testing.T) {
	p := pp.DefaultPublicParams()
	var tr fr.Element
	_, _ = tr.SetRandom()
	trBytes := tr.Bytes()

	zeroVal := snarktoken.FabtokenOpening{
		Value:     0,
		TokenType: "USD",
	}
	_, err := prover.BuildFabtokenMigrationWitness(zeroVal, p, trBytes[:])
	require.Error(t, err)

	emptyType := snarktoken.FabtokenOpening{
		Value:     100,
		TokenType: "",
	}
	_, err = prover.BuildFabtokenMigrationWitness(emptyType, p, trBytes[:])
	require.Error(t, err)
}

func TestBuildFabtokenMigrationWitness_UnsupportedCurve(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BW6_761
	opening := snarktoken.FabtokenOpening{
		Value:     100,
		TokenType: "USD",
	}
	_, err := prover.BuildFabtokenMigrationWitness(opening, p, make([]byte, 32))
	require.Error(t, err)
}

func TestNewFabtokenMigrationProver_NoKey_Error(t *testing.T) {
	p := pp.DefaultPublicParams()
	p, err := setup.SetupAll(p)
	require.NoError(t, err)

	pr, err := prepared.NewProver(p)
	require.NoError(t, err)

	_, err = prover.NewFabtokenMigrationProver(pr)
	require.Error(t, err)
}

func TestFabtokenMigrationProver_Prove_Success(t *testing.T) {
	p := pp.DefaultPublicParams()
	p, err := setup.SetupAll(p)
	require.NoError(t, err)
	p, err = setup.SetupFabtokenMigration(p)
	require.NoError(t, err)

	pr, err := prepared.NewProver(p)
	require.NoError(t, err)
	pr, err = pr.WithFabtokenMigration()
	require.NoError(t, err)

	fProver, err := prover.NewFabtokenMigrationProver(pr)
	require.NoError(t, err)

	var tr fr.Element
	_, _ = tr.SetRandom()
	trBytes := tr.Bytes()
	opening := snarktoken.FabtokenOpening{
		Value:     200,
		TokenType: "USD",
	}

	wRes, err := prover.BuildFabtokenMigrationWitness(opening, p, trBytes[:])
	require.NoError(t, err)

	proof, err := fProver.Prove(wRes.Assignment)
	require.NoError(t, err)
	require.NotEmpty(t, proof)
}

func TestOrchestrator_BuildFabtokenMigrationAction_NoProver(t *testing.T) {
	p := pp.DefaultPublicParams()
	o := prover.NewOrchestrator(nil, nil)

	_, _, err := o.BuildFabtokenMigrationAction(
		context.Background(),
		[]prover.FabtokenMigrationRequest{{
			Opening:   snarktoken.FabtokenOpening{Value: 50, TokenType: "USD"},
			Recipient: []byte("alice"),
		}},
		p,
	)
	require.Error(t, err)
}

func TestOrchestrator_BuildFabtokenMigrationAction_NoRequests(t *testing.T) {
	p := pp.DefaultPublicParams()
	o := prover.NewOrchestrator(nil, nil)

	_, _, err := o.BuildFabtokenMigrationAction(context.Background(), nil, p)
	require.Error(t, err)
}

func TestOrchestrator_BuildFabtokenMigrationAction_Valid(t *testing.T) {
	p := pp.DefaultPublicParams()
	p, err := setup.SetupAll(p)
	require.NoError(t, err)
	p, err = setup.SetupFabtokenMigration(p)
	require.NoError(t, err)

	pr, err := prepared.NewProver(p)
	require.NoError(t, err)
	pr, err = pr.WithFabtokenMigration()
	require.NoError(t, err)

	fProver, err := prover.NewFabtokenMigrationProver(pr)
	require.NoError(t, err)

	o := prover.NewOrchestrator(nil, nil)
	o.SetFabtokenMigrationProver(fProver)

	requests := []prover.FabtokenMigrationRequest{
		{
			Opening:   snarktoken.FabtokenOpening{Value: 100, TokenType: "USD"},
			Recipient: []byte("alice"),
		},
		{
			Opening:   snarktoken.FabtokenOpening{Value: 200, TokenType: "USD"},
			Recipient: []byte("bob"),
		},
	}

	actions, notes, err := o.BuildFabtokenMigrationAction(context.Background(), requests, p)
	require.NoError(t, err)
	require.Len(t, actions, 2)
	require.Len(t, notes, 2)

	require.Equal(t, uint64(100), notes[0].Value)
	require.Equal(t, uint64(200), notes[1].Value)
	require.NotEmpty(t, actions[0].MigrationProof)
	require.NotEmpty(t, actions[1].MigrationProof)
	require.Equal(t, []byte("alice"), actions[0].Recipient)
	require.Equal(t, []byte("bob"), actions[1].Recipient)
}
