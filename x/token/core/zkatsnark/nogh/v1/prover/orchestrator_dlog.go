/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// DLogUpgradeInput is a ZKAT-DLOG output redeemed by an upgrade issuance.
type DLogUpgradeInput struct {
	// ID is the ledger identifier of the redeemed token.
	ID token2.ID
	// Token is the serialized ZKAT-DLOG output, exactly as stored on the ledger.
	Token []byte
	// Opening is the opening of Token's commitment (see token.OpenDLogOutput).
	Opening *snarktoken.DLogOpening
}

type migrationOutcome struct {
	index  int
	result ProofResult
	desc   snarktoken.MigrationDescription
	err    error
}

// BuildDLogUpgradeIssueAction builds an upgrade issuance redeeming the passed
// ZKAT-DLOG outputs and minting outputs of the same type and total value.
//
// Every input gets a migration proof that its ledger commitment opens to
// tokenType and to the value in a fresh value commitment, and every output an
// ordinary output proof, all sharing one type randomness, which is revealed.
// The binding signature, under ActionTypeIssueMigration, conserves value
// between the migration value commitments and the outputs, so TotalValue is
// zero and the redeemed values stay hidden. Checking that the openings are
// valid and that the outputs sum to the redeemed total is the caller's
// responsibility; the validator enforces both on the ledger.
func (o *Orchestrator) BuildDLogUpgradeIssueAction(
	ctx context.Context,
	issuer []byte,
	inputs []DLogUpgradeInput,
	outputs []OutputRequest,
	tokenType string,
	publicParams *pp.PublicParams,
	metadata map[string][]byte,
) (*snarktoken.IssueAction, []*snarktoken.Note, error) {
	if len(inputs) == 0 {
		return nil, nil, errors.New("orchestrator: ZKAT-DLOG upgrade issue action requires at least one input")
	}
	if len(outputs) == 0 {
		return nil, nil, errors.New("orchestrator: issue action requires at least one output")
	}
	if o.migrationProver == nil {
		return nil, nil, errors.New("orchestrator: migration prover not configured, call SetMigrationProver")
	}
	for i, in := range inputs {
		if in.Opening == nil {
			return nil, nil, errors.Errorf("orchestrator: input %d has no opening", i)
		}
		if string(in.Opening.Type) != tokenType {
			return nil, nil, errors.Errorf("orchestrator: input %d has type [%s], expected [%s]", i, in.Opening.Type, tokenType)
		}
	}
	for j, out := range outputs {
		if out.TokenType != tokenType {
			return nil, nil, errors.Errorf("orchestrator: output %d has type [%s], expected [%s]", j, out.TokenType, tokenType)
		}
	}

	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, nil, errors.Wrap(err, "orchestrator: curve context failed")
	}
	typeRandomness, err := cc.randomFieldElement()
	if err != nil {
		return nil, nil, errors.Wrap(err, "orchestrator: type randomness generation failed")
	}
	typeCommitment, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomness, publicParams.Curve)
	if err != nil {
		return nil, nil, errors.Wrap(err, "orchestrator: type commitment computation failed")
	}

	migCh := make(chan migrationOutcome, len(inputs))
	for i := range inputs {
		go o.proveMigration(i, inputs[i].Opening, publicParams, migCh)
	}
	outputCh := make(chan outputOutcome, len(outputs))
	for j, req := range outputs {
		go o.proveOutput(j, req, publicParams, typeRandomness, outputCh)
	}

	// drain both channels before returning, so that no goroutine is left
	// blocked on a send
	migOutcomes := make([]migrationOutcome, len(inputs))
	var firstErr error
	for range inputs {
		r := <-migCh
		if r.err != nil && firstErr == nil {
			firstErr = errors.Wrapf(r.err, "orchestrator: migration proof %d failed", r.index)
		}
		migOutcomes[r.index] = r
	}
	outOutcomes := make([]outputOutcome, len(outputs))
	for range outputs {
		r := <-outputCh
		if r.err != nil && firstErr == nil {
			firstErr = errors.Wrapf(r.err, "orchestrator: output proof %d failed", r.index)
		}
		outOutcomes[r.index] = r
	}
	if firstErr != nil {
		return nil, nil, firstErr
	}

	actionInputs := make([]*snarktoken.IssueActionInput, len(inputs))
	inputResults := make([]ProofResult, len(inputs))
	for i, oc := range migOutcomes {
		desc := oc.desc
		actionInputs[i] = &snarktoken.IssueActionInput{
			ID:        inputs[i].ID,
			Token:     inputs[i].Token,
			Migration: &desc,
		}
		inputResults[i] = oc.result
	}
	outputResults := make([]ProofResult, len(outputs))
	outputDescs := make([]snarktoken.OutputDescription, len(outputs))
	notes := make([]*snarktoken.Note, len(outputs))
	for j, oc := range outOutcomes {
		outputResults[j] = oc.result
		outputDescs[j] = oc.desc
		notes[j] = oc.note
	}

	sig, err := ComputeBindingSignature(publicParams.Curve, snarktoken.ActionTypeIssueMigration, typeCommitment, inputResults, outputResults)
	if err != nil {
		return nil, nil, errors.Wrap(err, "orchestrator: binding signature failed")
	}

	return &snarktoken.IssueAction{
		Issuer:           issuer,
		TokenType:        tokenType,
		TypeCommitment:   typeCommitment,
		Outputs:          outputDescs,
		BindingSignature: sig,
		TotalValue:       make([]byte, (cc.modulus.BitLen()+7)/8),
		Metadata:         metadata,
		Inputs:           actionInputs,
		TypeRandomness:   typeRandomness,
	}, notes, nil
}

func (o *Orchestrator) proveMigration(index int, opening *snarktoken.DLogOpening, publicParams *pp.PublicParams, out chan<- migrationOutcome) {
	w, err := BuildMigrationWitness(opening, publicParams)
	if err != nil {
		out <- migrationOutcome{index: index, err: err}

		return
	}
	proof, err := o.migrationProver.Prove(w.Assignment)
	if err != nil {
		out <- migrationOutcome{index: index, err: err}

		return
	}
	out <- migrationOutcome{
		index: index,
		result: ProofResult{
			Commitment:       w.PedersenXY,
			ValueCommitmentX: w.ValueCommitmentX,
			ValueCommitmentY: w.ValueCommitmentY,
			RCV:              w.RCV,
		},
		desc: snarktoken.MigrationDescription{
			ValueCommitInX: w.ValueCommitmentX,
			ValueCommitInY: w.ValueCommitmentY,
			MigrationProof: proof,
		},
	}
}
