/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"context"
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// onCurveG1Bytes returns the uncompressed byte encoding of the BLS12-381 G1
// generator, a well-known on-curve point accepted by gnark-crypto's SetBytes.
func onCurveG1Bytes(t *testing.T) []byte {
	t.Helper()
	_, _, g1, _ := bls12381.Generators()
	raw := g1.RawBytes()

	return raw[:]
}

// randomCanonicalFr returns a random canonical BLS12-381 scalar field element
// serialized to 32 bytes.
func randomCanonicalFr(t *testing.T) []byte {
	t.Helper()
	var e fr.Element
	_, err := e.SetRandom()
	require.NoError(t, err)
	b := e.Bytes()

	return b[:]
}

func validOpening(t *testing.T) *snarktoken.DLogOpening {
	t.Helper()

	return &snarktoken.DLogOpening{
		DLogInput:      snarktoken.DLogInput{Owner: []byte("alice"), PedersenXY: onCurveG1Bytes(t)},
		Type:           "USD",
		Value:          100,
		BlindingFactor: randomCanonicalFr(t),
	}
}

func TestBuildMigrationWitness_Basic(t *testing.T) {
	p := pp.DefaultPublicParams()
	opening := validOpening(t)
	w, err := prover.BuildMigrationWitness(opening, p)
	require.NoError(t, err)
	require.Len(t, w.RCV, 32)
	require.Len(t, w.ValueCommitmentX, 32)
	require.Len(t, w.ValueCommitmentY, 32)
	require.Equal(t, opening.PedersenXY, w.PedersenXY)
	require.Equal(t, 0, snarktoken.TokenTypePed("USD").Cmp(w.Assignment.TokenTypePed.(*big.Int)),
		"the public type scalar is the ZKAT-DLOG encoding of the opened type")
}

func TestBuildMigrationWitness_Rejections(t *testing.T) {
	nonCanonical := make([]byte, 32)
	for i := range nonCanonical {
		nonCanonical[i] = 0xFF
	}
	tests := []struct {
		name   string
		mutate func(o *snarktoken.DLogOpening, p *pp.PublicParams)
	}{
		{"empty type", func(o *snarktoken.DLogOpening, _ *pp.PublicParams) { o.Type = "" }},
		{"zero value", func(o *snarktoken.DLogOpening, _ *pp.PublicParams) { o.Value = 0 }},
		{"value above MaxTokenValue", func(o *snarktoken.DLogOpening, p *pp.PublicParams) {
			p.MaxBits = 8
			o.Value = 256
		}},
		{"empty blinding factor", func(o *snarktoken.DLogOpening, _ *pp.PublicParams) { o.BlindingFactor = nil }},
		{"non-canonical blinding factor", func(o *snarktoken.DLogOpening, _ *pp.PublicParams) { o.BlindingFactor = nonCanonical }},
		{"short commitment", func(o *snarktoken.DLogOpening, _ *pp.PublicParams) { o.PedersenXY = o.PedersenXY[:95] }},
		{"commitment off curve", func(o *snarktoken.DLogOpening, _ *pp.PublicParams) {
			o.PedersenXY = append([]byte(nil), o.PedersenXY...)
			o.PedersenXY[95] ^= 1
		}},
		{"BN254 zkatsnark curve", func(_ *snarktoken.DLogOpening, p *pp.PublicParams) { p.Curve = ecc.BN254 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := pp.DefaultPublicParams()
			o := validOpening(t)
			tc.mutate(o, p)
			_, err := prover.BuildMigrationWitness(o, p)
			require.Error(t, err)
		})
	}
	_, err := prover.BuildMigrationWitness(nil, pp.DefaultPublicParams())
	require.Error(t, err)
}

func TestNewMigrationProver_RequiresMigrationKey(t *testing.T) {
	_, err := prover.NewMigrationProver(nil)
	require.Error(t, err)
}

// ── Orchestrator error paths ──────────────────────────────────────────────────

func TestOrchestrator_BuildTransferAction_NoInputsNoOutputs(t *testing.T) {
	setupProvers(t)
	o := prover.NewOrchestrator(spendProver, outputProver)

	_, _, err := o.BuildTransferAction(
		context.Background(),
		nil,
		nil,
		"USD",
		testPP,
		nil,
	)
	require.Error(t, err, "empty inputs and outputs must be rejected")
}

func TestOrchestrator_BuildIssueAction_NoOutputs(t *testing.T) {
	setupProvers(t)
	o := prover.NewOrchestrator(spendProver, outputProver)

	_, _, err := o.BuildIssueAction(
		context.Background(),
		[]byte("issuer"),
		nil,
		"USD",
		testPP,
		nil,
	)
	require.Error(t, err, "zero outputs must be rejected")
}

// TestOrchestrator_BuildDLogUpgradeIssueAction_Rejections covers the checks
// that run before any proving.
func TestOrchestrator_BuildDLogUpgradeIssueAction_Rejections(t *testing.T) {
	p := pp.DefaultPublicParams()
	in := prover.DLogUpgradeInput{ID: token2.ID{TxId: "tx", Index: 0}, Token: []byte("tok"), Opening: validOpening(t)}
	out := prover.OutputRequest{Value: 100, TokenType: "USD", Recipient: []byte("bob")}
	o := prover.NewOrchestrator(nil, nil)

	_, _, err := o.BuildDLogUpgradeIssueAction(context.Background(), []byte("issuer"), []prover.DLogUpgradeInput{in}, []prover.OutputRequest{out}, "USD", p, nil)
	require.ErrorContains(t, err, "migration prover not configured")

	_, _, err = o.BuildDLogUpgradeIssueAction(context.Background(), []byte("issuer"), nil, []prover.OutputRequest{out}, "USD", p, nil)
	require.Error(t, err, "no inputs")
	_, _, err = o.BuildDLogUpgradeIssueAction(context.Background(), []byte("issuer"), []prover.DLogUpgradeInput{in}, nil, "USD", p, nil)
	require.Error(t, err, "no outputs")
}
