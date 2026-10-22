/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// Orchestrator drives parallel proof generation for a single action and
// assembles the resulting wire-format Action. Construct once at driver
// startup, holding already-loaded provers, and reuse across every
// transaction.
type Orchestrator struct {
	spendProver     *SpendProver
	outputProver    *OutputProver
	migrationProver *MigrationProver // nil when migration is not configured
}

// NewOrchestrator creates a new Orchestrator over already-prepared public
// parameters for a proving role. The spendProver and outputProver must have
// been built from the same prepared.Prover.
func NewOrchestrator(spendProver *SpendProver, outputProver *OutputProver) *Orchestrator {
	return &Orchestrator{spendProver: spendProver, outputProver: outputProver}
}

// SetMigrationProver configures migration support. Call this after
// NewOrchestrator and SetupMigration have both completed. The orchestrator
// does not require a migration prover for non-migration actions.
func (o *Orchestrator) SetMigrationProver(mp *MigrationProver) {
	o.migrationProver = mp
}

type SpendRequest struct {
	Note *snarktoken.Note
}

type OutputRequest struct {
	Value     uint64
	TokenType string
	Recipient []byte
}

type spendOutcome struct {
	index  int
	result ProofResult
	desc   snarktoken.SpendDescription
	err    error
}

type outputOutcome struct {
	index  int
	result ProofResult
	desc   snarktoken.OutputDescription
	note   *snarktoken.Note
	err    error
}

// MigrationRequest describes a single zkatdlog token to migrate.
type MigrationRequest struct {
	Opening   snarktoken.PedersenOpening
	Recipient []byte
}

type migrationOutcome struct {
	index  int
	action snarktoken.MigrationAction
	note   *snarktoken.Note
	err    error
}

// BuildTransferAction generates every Spend and Output proof concurrently,
// computes the per-action binding signature, and assembles the resulting
// TransferAction. Returns the newly created output Notes alongside the
// action, persisting or transmitting them is the caller's responsibility.
// All inputs and outputs must share tokenType.
//
// A single typeRandomness is generated per action and shared across all
// inputs and outputs, ensuring they all produce the same TypeCommitment.
func (o *Orchestrator) BuildTransferAction(
	ctx context.Context,
	inputs []SpendRequest,
	outputs []OutputRequest,
	tokenType string,
	publicParams *pp.PublicParams,
	metadata map[string][]byte,
) (*snarktoken.TransferAction, []*snarktoken.Note, error) {
	if len(inputs) == 0 && len(outputs) == 0 {
		return nil, nil, errors.New("orchestrator: transfer action requires at least one input or output")
	}

	// Generate a single typeRandomness for the entire action.
	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: %w", err)
	}
	typeRandomnessBytes, err := cc.randomFieldElement()
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: type randomness generation failed: %w", err)
	}

	// Compute the shared TypeCommitment once for the action envelope.
	typeCommitment, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomnessBytes, publicParams.Curve)
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: type commitment computation failed: %w", err)
	}

	spendCh := make(chan spendOutcome, len(inputs))
	outputCh := make(chan outputOutcome, len(outputs))

	for i, req := range inputs {
		go o.proveSpend(i, req, typeRandomnessBytes, publicParams, spendCh)
	}

	for j, req := range outputs {
		go o.proveOutput(j, req, publicParams, typeRandomnessBytes, outputCh)
	}

	spendOutcomes := make([]spendOutcome, len(inputs))
	for range inputs {
		r := <-spendCh
		if r.err != nil {
			return nil, nil, fmt.Errorf("orchestrator: spend proof %d failed: %w", r.index, r.err)
		}
		spendOutcomes[r.index] = r
	}

	outputOutcomes := make([]outputOutcome, len(outputs))
	for range outputs {
		r := <-outputCh
		if r.err != nil {
			return nil, nil, fmt.Errorf("orchestrator: output proof %d failed: %w", r.index, r.err)
		}
		outputOutcomes[r.index] = r
	}

	inputResults := make([]ProofResult, len(spendOutcomes))
	inputDescs := make([]snarktoken.SpendDescription, len(spendOutcomes))
	for i, oc := range spendOutcomes {
		inputResults[i] = oc.result
		inputDescs[i] = oc.desc
	}

	outputResults := make([]ProofResult, len(outputOutcomes))
	outputDescs := make([]snarktoken.OutputDescription, len(outputOutcomes))
	newNotes := make([]*snarktoken.Note, len(outputOutcomes))
	for j, oc := range outputOutcomes {
		outputResults[j] = oc.result
		outputDescs[j] = oc.desc
		newNotes[j] = oc.note
	}

	sigBytes, err := ComputeBindingSignature(publicParams.Curve, snarktoken.ActionTypeTransfer, typeCommitment, inputResults, outputResults)
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: binding signature failed: %w", err)
	}

	action := &snarktoken.TransferAction{
		TypeCommitment:   typeCommitment,
		Inputs:           inputDescs,
		Outputs:          outputDescs,
		BindingSignature: sigBytes,
		Metadata:         metadata,
	}

	return action, newNotes, nil
}

// BuildIssueAction generates proofs for a set of newly issued tokens and
// assembles the resulting IssueAction. Structurally identical to
// BuildTransferAction with zero inputs.
func (o *Orchestrator) BuildIssueAction(
	ctx context.Context,
	issuer []byte,
	outputs []OutputRequest,
	tokenType string,
	publicParams *pp.PublicParams,
	metadata map[string][]byte,
) (*snarktoken.IssueAction, []*snarktoken.Note, error) {
	if len(outputs) == 0 {
		return nil, nil, errors.New("orchestrator: issue action requires at least one output")
	}

	// Generate a single typeRandomness for the entire action.
	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: %w", err)
	}
	typeRandomnessBytes, err := cc.randomFieldElement()
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: type randomness generation failed: %w", err)
	}

	// Compute the shared TypeCommitment once for the action envelope.
	typeCommitment, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomnessBytes, publicParams.Curve)
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: type commitment computation failed: %w", err)
	}

	var totalValue uint64
	outputCh := make(chan outputOutcome, len(outputs))
	for j, req := range outputs {
		go o.proveOutput(j, req, publicParams, typeRandomnessBytes, outputCh)
		totalValue += req.Value
	}

	outputOutcomes := make([]outputOutcome, len(outputs))
	for range outputs {
		r := <-outputCh
		if r.err != nil {
			return nil, nil, fmt.Errorf("orchestrator: output proof %d failed: %w", r.index, r.err)
		}
		outputOutcomes[r.index] = r
	}

	outputResults := make([]ProofResult, len(outputOutcomes))
	outputDescs := make([]snarktoken.OutputDescription, len(outputOutcomes))
	newNotes := make([]*snarktoken.Note, len(outputOutcomes))
	for j, oc := range outputOutcomes {
		outputResults[j] = oc.result
		outputDescs[j] = oc.desc
		newNotes[j] = oc.note
	}

	sigBytes, err := ComputeBindingSignature(publicParams.Curve, snarktoken.ActionTypeIssue, typeCommitment, nil, outputResults)
	if err != nil {
		return nil, nil, fmt.Errorf("orchestrator: binding signature failed: %w", err)
	}

	totalValueBytes := new(big.Int).SetUint64(totalValue).FillBytes(make([]byte, (cc.modulus.BitLen()+7)/8))

	action := &snarktoken.IssueAction{
		Issuer:           issuer,
		TokenType:        tokenType,
		TypeCommitment:   typeCommitment,
		Outputs:          outputDescs,
		BindingSignature: sigBytes,
		TotalValue:       totalValueBytes,
		Metadata:         metadata,
	}

	return action, newNotes, nil
}

// BuildMigrationAction generates proofs for a batch of zkatdlog token
// migrations concurrently, producing one MigrationAction per token.
// No binding signature is computed (Decision C: the shared Value variable
// in each circuit is the conservation proof).
//
// Returns the newly created Notes alongside the actions; persisting or
// transmitting them is the caller's responsibility.
func (o *Orchestrator) BuildMigrationAction(
	ctx context.Context,
	requests []MigrationRequest,
	publicParams *pp.PublicParams,
) ([]snarktoken.MigrationAction, []*snarktoken.Note, error) {
	if len(requests) == 0 {
		return nil, nil, errors.New("orchestrator: migration action requires at least one request")
	}

	if o.migrationProver == nil {
		return nil, nil, errors.New("orchestrator: migration prover not configured — call SetMigrationProver")
	}

	migCh := make(chan migrationOutcome, len(requests))
	for i, req := range requests {
		go o.proveMigration(i, req, publicParams, migCh)
	}

	outcomes := make([]migrationOutcome, len(requests))
	for range requests {
		r := <-migCh
		if r.err != nil {
			return nil, nil, fmt.Errorf("orchestrator: migration proof %d failed: %w", r.index, r.err)
		}

		outcomes[r.index] = r
	}

	actions := make([]snarktoken.MigrationAction, len(outcomes))
	notes := make([]*snarktoken.Note, len(outcomes))
	for i, oc := range outcomes {
		actions[i] = oc.action
		notes[i] = oc.note
	}

	return actions, notes, nil
}

func (o *Orchestrator) proveSpend(index int, req SpendRequest, typeRandomness []byte, publicParams *pp.PublicParams, out chan<- spendOutcome) {
	witnessRes, err := BuildSpendWitness(req.Note, typeRandomness, publicParams)
	if err != nil {
		out <- spendOutcome{index: index, err: err}

		return
	}

	proofBytes, err := o.spendProver.Prove(witnessRes.Assignment)
	if err != nil {
		out <- spendOutcome{index: index, err: err}

		return
	}

	out <- spendOutcome{
		index: index,
		result: ProofResult{
			Commitment:       witnessRes.Commitment,
			ValueCommitmentX: witnessRes.ValueCommitmentX,
			ValueCommitmentY: witnessRes.ValueCommitmentY,
			RCV:              witnessRes.RCV,
		},
		desc: snarktoken.SpendDescription{
			CommitmentIn:   witnessRes.Commitment,
			ValueCommitInX: witnessRes.ValueCommitmentX,
			ValueCommitInY: witnessRes.ValueCommitmentY,
			TypeCommitment: witnessRes.TypeCommitment,
			SpendProof:     proofBytes,
		},
	}
}

func (o *Orchestrator) proveOutput(index int, req OutputRequest, publicParams *pp.PublicParams, typeRandomness []byte, out chan<- outputOutcome) {
	witnessRes, err := BuildOutputWitness(req.Value, req.TokenType, publicParams, typeRandomness)
	if err != nil {
		out <- outputOutcome{index: index, err: err}

		return
	}

	proofBytes, err := o.outputProver.Prove(witnessRes.Assignment)
	if err != nil {
		out <- outputOutcome{index: index, err: err}

		return
	}

	out <- outputOutcome{
		index: index,
		result: ProofResult{
			Commitment:       witnessRes.Commitment,
			ValueCommitmentX: witnessRes.ValueCommitmentX,
			ValueCommitmentY: witnessRes.ValueCommitmentY,
			RCV:              witnessRes.RCV,
		},
		desc: snarktoken.OutputDescription{
			CommitmentOut:   witnessRes.Commitment,
			ValueCommitOutX: witnessRes.ValueCommitmentX,
			ValueCommitOutY: witnessRes.ValueCommitmentY,
			TypeCommitment:  witnessRes.TypeCommitment,
			OutputProof:     proofBytes,
			Recipient:       req.Recipient,
		},
		note: witnessRes.Note,
	}
}

func (o *Orchestrator) proveMigration(index int, req MigrationRequest, publicParams *pp.PublicParams, out chan<- migrationOutcome) {
	// Each migration request gets its own typeRandomness since
	// each produces an independent MigrationAction.
	cc, err := newCurveCtx(publicParams.Curve)
	if err != nil {
		out <- migrationOutcome{index: index, err: err}

		return
	}
	typeRandomnessBytes, err := cc.randomFieldElement()
	if err != nil {
		out <- migrationOutcome{index: index, err: fmt.Errorf("type randomness generation failed: %w", err)}

		return
	}

	witnessRes, err := BuildMigrationWitness(req.Opening, publicParams, typeRandomnessBytes)
	if err != nil {
		out <- migrationOutcome{index: index, err: err}

		return
	}

	proofBytes, err := o.migrationProver.Prove(witnessRes.Assignment)
	if err != nil {
		out <- migrationOutcome{index: index, err: err}

		return
	}

	out <- migrationOutcome{
		index: index,
		action: snarktoken.MigrationAction{
			CommitmentPedersenX: witnessRes.PedersenCommitX[:],
			CommitmentPedersenY: witnessRes.PedersenCommitY[:],
			CommitmentMiMC:      witnessRes.Commitment,
			ValueCommitOutX:     witnessRes.ValueCommitmentX,
			ValueCommitOutY:     witnessRes.ValueCommitmentY,
			TypeCommitment:      witnessRes.TypeCommitment,
			MigrationProof:      proofBytes,
			Recipient:           req.Recipient,
		},
		note: witnessRes.Note,
	}
}
