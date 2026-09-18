/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package regression provides end-to-end regression tests for the zkatsnark
// token driver. It constructs token requests (issue, transfer) using the real
// prover and validates them using the real validator, ensuring the full
// pipeline works correctly.
//
// These tests deliberately exercise the validator pipeline as the common
// framework would invoke it, including signature verification and consumption.
package regression

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/validator"
)

// ── Shared expensive setup ────────────────────────────────────────────────

var (
	setupOnce    sync.Once
	sharedPP     *pp.PublicParams
	orch         *prover.Orchestrator
	sharedSetErr error
)

func setupShared(t *testing.T) {
	t.Helper()
	setupOnce.Do(func() {
		p := pp.DefaultPublicParams()
		var err error
		p, err = setup.SetupAll(p)
		if err != nil {
			sharedSetErr = errors.Wrap(err, "setup.SetupAll")

			return
		}
		sharedPP = p

		// Build provers
		spendCS, err := setup.CompileSpendCircuit(p)
		if err != nil {
			sharedSetErr = errors.Wrap(err, "CompileSpendCircuit")

			return
		}
		spendPK, err := setup.LoadProvingKey(p, setup.CircuitSpend)
		if err != nil {
			sharedSetErr = errors.Wrap(err, "LoadProvingKey spend")

			return
		}
		outputCS, err := setup.CompileOutputCircuit(p)
		if err != nil {
			sharedSetErr = errors.Wrap(err, "CompileOutputCircuit")

			return
		}
		outputPK, err := setup.LoadProvingKey(p, setup.CircuitOutput)
		if err != nil {
			sharedSetErr = errors.Wrap(err, "LoadProvingKey output")

			return
		}

		orch = prover.NewOrchestrator(
			prover.NewSpendProver(spendCS, spendPK),
			prover.NewOutputProver(outputCS, outputPK),
		)
	})

	if sharedSetErr != nil {
		t.Fatalf("shared setup failed: %v", sharedSetErr)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────

// buildIssueAction creates an IssueAction via the real prover.
func buildIssueAction(
	t *testing.T,
	issuer []byte,
	tokenType string,
	values []uint64,
	recipients [][]byte,
) (*snarktoken.IssueAction, []*snarktoken.Note) {
	t.Helper()
	setupShared(t)

	outputs := make([]prover.OutputRequest, len(values))
	for i, v := range values {
		outputs[i] = prover.OutputRequest{
			Value:     v,
			TokenType: tokenType,
			Recipient: recipients[i],
		}
	}

	action, notes, err := orch.BuildIssueAction(
		context.Background(),
		issuer,
		outputs,
		tokenType,
		sharedPP,
	)
	require.NoError(t, err, "BuildIssueAction")

	return action, notes
}

// buildTransferAction creates a TransferAction via the real prover.
func buildTransferAction(
	t *testing.T,
	inputNotes []*snarktoken.Note,
	tokenType string,
	outputValues []uint64,
	outputRecipients [][]byte,
) (*snarktoken.TransferAction, []*snarktoken.Note) {
	t.Helper()
	setupShared(t)

	inputs := make([]prover.SpendRequest, len(inputNotes))
	for i, note := range inputNotes {
		inputs[i] = prover.SpendRequest{Note: note}
	}

	outputs := make([]prover.OutputRequest, len(outputValues))
	for i, v := range outputValues {
		outputs[i] = prover.OutputRequest{
			Value:     v,
			TokenType: tokenType,
			Recipient: outputRecipients[i],
		}
	}

	action, notes, err := orch.BuildTransferAction(
		context.Background(),
		inputs,
		outputs,
		tokenType,
		sharedPP,
	)
	require.NoError(t, err, "BuildTransferAction")

	return action, notes
}

// testLedger is an in-memory ledger that stores serialized OutputDescriptions
// keyed by token ID.
type testLedger struct {
	tokens map[string][]byte
}

// GetState returns the stored token bytes for a given token ID.
func (l *testLedger) GetState(id token2.ID) ([]byte, error) {
	key := id.String()
	raw, ok := l.tokens[key]
	if !ok {
		return nil, errors.Errorf("token not found: %s", key)
	}

	return raw, nil
}

// storeLedgerToken stores a serialized OutputDescription in the test ledger.
func storeLedgerToken(t *testing.T, ledger *testLedger, txID string, index uint64, desc snarktoken.OutputDescription) *token2.ID {
	t.Helper()
	raw, err := json.Marshal(desc)
	require.NoError(t, err)

	id := &token2.ID{TxId: txID, Index: index}
	ledger.tokens[id.String()] = raw

	return id
}

// testDeserializer is a stub deserializer for testing. It accepts any
// identity bytes and returns a no-op verifier.
type testDeserializer struct{}

// GetOwnerVerifier returns a stub verifier that always succeeds.
func (d *testDeserializer) GetOwnerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return &alwaysValidVerifier{}, nil
}

// GetIssuerVerifier returns a stub verifier that always succeeds.
func (d *testDeserializer) GetIssuerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return &alwaysValidVerifier{}, nil
}

// GetAuditorVerifier returns a stub verifier that always succeeds.
func (d *testDeserializer) GetAuditorVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return &alwaysValidVerifier{}, nil
}

// Recipients returns the identity itself as the sole recipient.
func (d *testDeserializer) Recipients(raw driver.Identity) ([]driver.Identity, error) {
	return []driver.Identity{raw}, nil
}

// GetAuditInfoMatcher returns a stub matcher that always succeeds.
func (d *testDeserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return &alwaysValidMatcher{}, nil
}

// MatchIdentity always returns nil (match succeeds).
func (d *testDeserializer) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	return nil
}

// GetAuditInfo returns empty audit info.
func (d *testDeserializer) GetAuditInfo(ctx context.Context, id driver.Identity, p driver.AuditInfoProvider) ([]byte, error) {
	return []byte("test-audit-info"), nil
}

// alwaysValidVerifier is a verifier that always returns nil (valid).
type alwaysValidVerifier struct{}

// Verify always returns nil.
func (v *alwaysValidVerifier) Verify(message, sigma []byte) error {
	return nil
}

// alwaysValidMatcher is a matcher that always returns nil (match succeeds).
type alwaysValidMatcher struct{}

// Match always returns nil.
func (m *alwaysValidMatcher) Match(ctx context.Context, identity []byte) error {
	return nil
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestIssueAction_ValidatesSuccessfully builds a real issue action via
// the prover and validates it using the standalone Validator.ValidateIssue.
func TestIssueAction_ValidatesSuccessfully(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	owner := []byte("test-owner")

	action, _ := buildIssueAction(t, issuer, "USD", []uint64{100}, [][]byte{owner})

	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{})
	require.NoError(t, err)

	err = val.ValidateIssue(action)
	require.NoError(t, err, "ValidateIssue should succeed for a properly constructed action")
}

// TestIssueAction_TwoOutputs_ValidatesSuccessfully tests issue with
// multiple outputs.
func TestIssueAction_TwoOutputs_ValidatesSuccessfully(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	action, _ := buildIssueAction(
		t, issuer, "EUR",
		[]uint64{50, 75},
		[][]byte{[]byte("alice"), []byte("bob")},
	)

	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{})
	require.NoError(t, err)

	err = val.ValidateIssue(action)
	require.NoError(t, err, "ValidateIssue should succeed for 2-output issue")
}

// TestTransferAction_ValidatesSuccessfully builds a real transfer action
// and validates it using the standalone Validator.ValidateTransfer.
func TestTransferAction_ValidatesSuccessfully(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	owner := []byte("test-owner")

	// Issue 100 tokens to owner
	_, issuedNotes := buildIssueAction(t, issuer, "USD", []uint64{100}, [][]byte{owner})

	// Transfer: spend the 100, produce 60 + 40
	action, _ := buildTransferAction(
		t,
		issuedNotes,
		"USD",
		[]uint64{60, 40},
		[][]byte{[]byte("bob"), owner},
	)

	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{})
	require.NoError(t, err)

	err = val.ValidateTransfer(action)
	require.NoError(t, err, "ValidateTransfer should succeed for a properly constructed action")
}

// TestTransferAction_MultipleInputs_ValidatesSuccessfully tests a transfer
// with multiple inputs being merged into fewer outputs.
func TestTransferAction_MultipleInputs_ValidatesSuccessfully(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	owner := []byte("test-owner")

	// Issue two separate tokens
	_, notes1 := buildIssueAction(t, issuer, "USD", []uint64{50}, [][]byte{owner})
	_, notes2 := buildIssueAction(t, issuer, "USD", []uint64{30}, [][]byte{owner})

	// Transfer: spend both (50+30=80), produce single output of 80
	action, _ := buildTransferAction(
		t,
		[]*snarktoken.Note{notes1[0], notes2[0]},
		"USD",
		[]uint64{80},
		[][]byte{[]byte("bob")},
	)

	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{})
	require.NoError(t, err)

	err = val.ValidateTransfer(action)
	require.NoError(t, err, "ValidateTransfer should succeed for multi-input transfer")
}

// TestTransferAction_CommonValidator_SignaturesConsumed verifies that a
// transfer action going through the common.Validator pipeline correctly
// consumes all owner signatures. This is the specific regression test for
// the "unconsumed signatures" bug.
//
// It exercises the full VerifyTokenRequestFromRaw path, which parses
// TokenRequest.Signatures into per-action Backends, calls VerifyTransfer
// with the scoped Backend, and invokes EnsureExhausted, the same sequence
// a Fabric peer runs during endorsement.
func TestTransferAction_CommonValidator_SignaturesConsumed(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	owner := []byte("test-owner")

	// Issue 100 tokens to owner
	issueAction, issuedNotes := buildIssueAction(t, issuer, "USD", []uint64{100}, [][]byte{owner})

	// Transfer: spend the 100, produce 60 + 40
	transferAction, _ := buildTransferAction(
		t,
		issuedNotes,
		"USD",
		[]uint64{60, 40},
		[][]byte{[]byte("bob"), owner},
	)

	// Set InputIDs and InputTokens on the transfer action (as TransferService would)
	inputTokenID := &token2.ID{TxId: "issue-tx-0", Index: 0}
	transferAction.InputIDs = []*token2.ID{inputTokenID}
	outputDescRaw, err := json.Marshal(issueAction.Outputs[0])
	require.NoError(t, err)
	transferAction.InputTokens = [][]byte{outputDescRaw}

	// Store the issued token on the test ledger so the validator can look up owner
	ledger := &testLedger{tokens: make(map[string][]byte)}
	storeLedgerToken(t, ledger, "issue-tx-0", 0, issueAction.Outputs[0])

	// Build token request with proper action-scoped signatures
	transferRaw, err := transferAction.Serialize()
	require.NoError(t, err)

	anchor := driver.TokenRequestAnchor("test-tx")
	tr := &driver.TokenRequest{
		Version: driver.ProtocolV1,
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferRaw},
		},
		Signatures: []*driver.RequestSignature{
			{
				Action: &driver.ActionSignature{
					ActionID:  0,
					Signature: []byte("owner-sig-for-input-0"),
				},
			},
		},
	}

	raw, err := tr.Bytes()
	require.NoError(t, err)

	// Create validator via the common framework
	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{}.WithDefaults())
	require.NoError(t, err)

	// VerifyTokenRequestFromRaw exercises the full framework path:
	// parse signatures → scope to per-action Backends → VerifyTransfer → EnsureExhausted
	_, _, err = val.VerifyTokenRequestFromRaw(
		context.Background(),
		ledger.GetState,
		anchor,
		raw,
	)
	require.NoError(t, err, "VerifyTokenRequestFromRaw should succeed and consume all signatures")
}

// TestTransferAction_CommonValidator_TwoInputs_SignaturesConsumed verifies
// that a multi-input transfer correctly consumes all owner signatures
// through the full framework pipeline.
func TestTransferAction_CommonValidator_TwoInputs_SignaturesConsumed(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	owner := []byte("test-owner")

	// Issue two tokens
	issueAction1, notes1 := buildIssueAction(t, issuer, "USD", []uint64{50}, [][]byte{owner})
	issueAction2, notes2 := buildIssueAction(t, issuer, "USD", []uint64{30}, [][]byte{owner})

	// Transfer: spend both (50+30=80), produce 80
	transferAction, _ := buildTransferAction(
		t,
		[]*snarktoken.Note{notes1[0], notes2[0]},
		"USD",
		[]uint64{80},
		[][]byte{[]byte("bob")},
	)

	// Set InputIDs and InputTokens
	inputID0 := &token2.ID{TxId: "issue-tx-0", Index: 0}
	inputID1 := &token2.ID{TxId: "issue-tx-1", Index: 0}
	transferAction.InputIDs = []*token2.ID{inputID0, inputID1}
	outputDescRaw0, err := json.Marshal(issueAction1.Outputs[0])
	require.NoError(t, err)
	outputDescRaw1, err := json.Marshal(issueAction2.Outputs[0])
	require.NoError(t, err)
	transferAction.InputTokens = [][]byte{outputDescRaw0, outputDescRaw1}

	// Store tokens on ledger
	ledger := &testLedger{tokens: make(map[string][]byte)}
	storeLedgerToken(t, ledger, "issue-tx-0", 0, issueAction1.Outputs[0])
	storeLedgerToken(t, ledger, "issue-tx-1", 0, issueAction2.Outputs[0])

	// Build token request with two action-scoped signatures
	transferRaw, err := transferAction.Serialize()
	require.NoError(t, err)

	anchor := driver.TokenRequestAnchor("test-tx")
	tr := &driver.TokenRequest{
		Version: driver.ProtocolV1,
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferRaw},
		},
		Signatures: []*driver.RequestSignature{
			{
				Action: &driver.ActionSignature{
					ActionID:  0,
					Signature: []byte("sig-for-input-0"),
				},
			},
			{
				Action: &driver.ActionSignature{
					ActionID:  0,
					Signature: []byte("sig-for-input-1"),
				},
			},
		},
	}

	raw, err := tr.Bytes()
	require.NoError(t, err)

	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{}.WithDefaults())
	require.NoError(t, err)

	_, _, err = val.VerifyTokenRequestFromRaw(
		context.Background(),
		ledger.GetState,
		anchor,
		raw,
	)
	require.NoError(t, err, "VerifyTokenRequestFromRaw should succeed and consume all 2 owner signatures")
}

// TestTransferAction_MissingInputIDs_Fails verifies that a transfer action
// without InputIDs fails validation with a clear error.
func TestTransferAction_MissingInputIDs_Fails(t *testing.T) {
	setupShared(t)

	issuer := []byte("test-issuer")
	owner := []byte("test-owner")

	_, issuedNotes := buildIssueAction(t, issuer, "USD", []uint64{100}, [][]byte{owner})

	transferAction, _ := buildTransferAction(
		t,
		issuedNotes,
		"USD",
		[]uint64{60, 40},
		[][]byte{[]byte("bob"), owner},
	)

	// Deliberately do NOT set InputIDs
	transferAction.InputIDs = nil

	ledger := &testLedger{tokens: make(map[string][]byte)}

	transferRaw, err := transferAction.Serialize()
	require.NoError(t, err)

	anchor := driver.TokenRequestAnchor("test-tx")
	tr := &driver.TokenRequest{
		Version: driver.ProtocolV1,
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: transferRaw},
		},
		Signatures: []*driver.RequestSignature{
			{
				Action: &driver.ActionSignature{
					ActionID:  0,
					Signature: []byte("sig"),
				},
			},
		},
	}

	raw, err := tr.Bytes()
	require.NoError(t, err)

	val, err := validator.NewValidator(sharedPP, &testDeserializer{}, driver.ResourceLimits{}.WithDefaults())
	require.NoError(t, err)

	_, _, err = val.VerifyTokenRequestFromRaw(
		context.Background(),
		ledger.GetState,
		anchor,
		raw,
	)
	require.Error(t, err, "VerifyTokenRequestFromRaw should fail when InputIDs are missing")
	require.ErrorIs(t, err, validator.ErrMissingInputIDs)
}
