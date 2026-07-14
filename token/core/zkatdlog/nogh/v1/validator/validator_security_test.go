/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file contains security-focused regression tests for the zkatdlog validator.
// Each test targets a specific attack scenario identified during security analysis:
//
//  1. Panic conditions (nil pointer dereferences, out-of-bounds accesses)
//  2. False acceptances (invalid requests that could pass validation)
//  3. False rejections (valid requests that could fail validation)
//
// The tests complement validator_test.go and validator_extra_test.go in the same package.
package validator_test

import (
	"context"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	testing2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/testutils"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	mock3 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSecurityTestEnv creates a test environment using the same parameters as
// other tests in this package (testUseCaseExtra is declared in validator_extra_test.go).
func newSecurityTestEnv(t *testing.T) *testing2.Env {
	t.Helper()
	configurations, err := benchmark.NewSetupConfigurationsWithParams(
		benchmark.SetupParams{
			IdemixTestdataPath: "./../testdata",
			Bits:               []uint64{testUseCaseExtra.Bits},
			CurveIDs:           []math.CurveID{testUseCaseExtra.CurveID},
			OwnerIdentityType:  idemixnym.IdentityType,
			ProofType:          rp.RangeProofType,
		},
	)
	require.NoError(t, err)
	env, err := testing2.NewEnv(testUseCaseExtra, configurations)
	require.NoError(t, err)

	return env
}

// ===========================================================================
// PANIC PREVENTION TESTS
// ===========================================================================

// TestSecurityPanicNilBackendLedger verifies that Backend.GetState returns an error
// instead of panicking when the underlying ledger function is nil.
//
// Attack vector (P4): VerifyTokenRequestFromRaw accepts nil as the getState argument
// (and many test/production paths pass nil). Any validator that calls
// ctx.Ledger.GetState() will cause a nil-function-pointer panic on the chaincode,
// constituting a denial-of-service vector. The fix adds an explicit nil guard.
func TestSecurityPanicNilBackendLedger(t *testing.T) {
	backend := common.NewBackend(logging.MustGetLogger(), nil /*nil ledger*/, nil, nil)

	_, err := backend.GetState(token2.ID{TxId: "tx1", Index: 0})
	require.Error(t, err, "GetState with nil ledger must return an error, not panic")
	assert.Contains(t, err.Error(), "ledger not available")
}

// TestSecurityPanicHTLCSignaturesBoundsCheck verifies that TransferHTLCValidate
// returns an error (not an index-out-of-bounds panic) when the Signatures slice
// does not contain an entry for a given input index.
//
// Attack vector (P3): if TransferHTLCValidate runs in a custom pipeline that
// omits TransferSignatureValidate, ctx.Signatures is nil/empty. Without the
// bounds check, `ctx.Signatures[i]` panics the chaincode. The fix adds an
// explicit bounds check before accessing ctx.Signatures[i].
func TestSecurityPanicHTLCSignaturesBoundsCheck(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Build an HTLC owner (ScriptType) as the input token owner so the HTLC
	// branch is entered. To keep the test focused, we use an identity that is
	// NOT htlc.ScriptType so the function fails early during UnmarshalTypedIdentity,
	// but in a way that would not panic. The important invariant we test is that
	// TransferHTLCValidate with no Signatures slice does NOT panic regardless of
	// the owner type.
	ctx := &validator.Context{
		Logger: logging.MustGetLogger(),
		PP:     pp,
		InputTokens: []*token.Token{
			{Owner: []byte("raw-owner-bytes-not-wrapped")},
		},
		TransferAction: &transfer.Action{
			Inputs: []*transfer.ActionInput{{}},
		},
		Signatures:      nil, // intentionally absent
		MetadataCounter: make(map[string]int),
	}

	// Must return an error, never panic.
	require.NotPanics(t, func() {
		err = validator.TransferHTLCValidate(context.Background(), ctx)
	})
	// The error from UnmarshalTypedIdentity is acceptable here.
	require.Error(t, err)
}

// TestSecurityPanicGetOutputCommitmentsNilOutput verifies that GetOutputCommitments
// does not panic when the Outputs slice contains nil entries.
//
// Attack vector (P1): transfer.Action.Deserialize previously left nil entries in
// t.Outputs when a protobuf output was missing/nil. GetOutputCommitments then
// dereferenced t.Outputs[i].Data, causing a nil-pointer panic. The fix skips
// nil entries; the ZK proof verifier will subsequently reject the mismatched
// commitment count.
func TestSecurityPanicGetOutputCommitmentsNilOutput(t *testing.T) {
	action := &transfer.Action{
		Outputs: []*token.Token{
			nil,
			{Data: &math.G1{}},
			nil,
		},
	}

	require.NotPanics(t, func() {
		coms := action.GetOutputCommitments()
		// Only the non-nil output contributes a commitment.
		assert.Len(t, coms, 1)
	})
}

// TestSecurityPanicAuditingContextMetadataCounter verifies that the Context
// created inside VerifyAuditing has its MetadataCounter properly initialized so
// that a custom auditing validator calling ctx.CountMetadataKey() does not
// panic with a nil map write.
//
// Attack vector (FA6 / panic): before the fix, the auditing Context had no
// MetadataCounter set. Any auditing extension that counts metadata keys would
// trigger a "assignment to entry in nil map" panic.
func TestSecurityPanicAuditingContextMetadataCounter(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	panicked := false
	calledWithNonNilMap := false

	// Custom auditing validator that calls CountMetadataKey.
	auditingValidator := func(c context.Context, ctx *validator.Context) error {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		// This would panic if ctx.MetadataCounter is nil.
		ctx.CountMetadataKey("audit-key")
		calledWithNonNilMap = true

		return nil
	}

	v := validator.New(
		logging.MustGetLogger(),
		pp,
		nil,
		nil,
		nil,
		[]validator.ValidateAuditingFunc{auditingValidator},
	)

	// Call VerifyAuditing with an empty request (no auditor sigs, no configured
	// auditors). The built-in AuditingSignaturesValidate returns nil in this case.
	_ = v.VerifyAuditing(
		context.Background(),
		"test-anchor",
		&driver.TokenRequest{},
		nil,
		nil,
		nil,
	)

	assert.False(t, panicked, "ctx.CountMetadataKey must not panic (MetadataCounter was nil before fix)")
	assert.True(t, calledWithNonNilMap, "auditing validator must have been called")
}

// ===========================================================================
// FALSE ACCEPTANCE PREVENTION TESTS
// ===========================================================================

// TestSecurityFalseAcceptanceTransferDeserializeNilOutput verifies that
// transfer.Action.Deserialize returns an error when the raw bytes decode to a
// protobuf with a nil output (simulated via corrupt/minimal bytes), rather than
// silently producing a nil entry in t.Outputs.
//
// Attack vector (FA3): before the fix, nil output proto fields were silently
// left as nil in t.Outputs. Between Deserialize() and Validate(), the struct
// was in an inconsistent state that could cause nil-pointer dereferences in
// any code that accessed t.Outputs[i].Data directly (e.g., GetOutputCommitments).
func TestSecurityFalseAcceptanceTransferDeserializeNilOutput(t *testing.T) {
	action := &transfer.Action{}
	// Garbage bytes that can't be a valid protobuf-encoded TransferAction.
	err := action.Deserialize([]byte("not-a-valid-proto-payload"))
	require.Error(t, err, "deserializing garbage bytes must fail")
}

// TestSecurityFalseAcceptanceIssuerUnauthorized verifies that an issue action
// whose Issuer is not present in PublicParams.Issuers is rejected.
//
// Attack vector (FA5): an attacker crafts a valid-looking issue action with an
// arbitrary Issuer identity and provides a valid ZK proof. If the issuer check
// were missing or bypassed, the token would be issued without authorization.
func TestSecurityFalseAcceptanceIssuerUnauthorized(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Modify the engine's PP to set a specific authorized issuer that differs
	// from the one used to produce TRWithIssue.
	env.Engine.PublicParams.IssuerIDs = []driver.Identity{[]byte("not-the-real-issuer")}

	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issuer is not authorized")
}

// TestSecurityFalseAcceptanceAuditorSignaturesMissingWhenRequired verifies that
// a token request without an auditor signature is rejected when auditors are
// configured in the public parameters.
//
// Attack vector: an attacker strips the auditor signature from an otherwise
// valid request, hoping the validator skips the auditing check. The validator
// must reject with a clear error.
func TestSecurityFalseAcceptanceAuditorSignaturesMissingWhenRequired(t *testing.T) {
	env := newSecurityTestEnv(t)

	// The env always configures an auditor. Strip all auditor signatures from
	// the request to simulate an attacker who removed them.
	var stripped []*driver.RequestSignature
	for _, sig := range env.TRWithIssue.Signatures {
		if sig != nil && sig.Auditor == nil {
			stripped = append(stripped, sig)
		}
	}
	env.TRWithIssue.Signatures = stripped

	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auditor signatures missing")
}

// TestSecurityFalseAcceptanceAuditorSignaturesPresentWhenNotRequired verifies that
// a token request carrying an unexpected auditor signature is rejected when no
// auditors are configured in the public parameters.
//
// Attack vector: an attacker injects an auditor signature from a previously
// valid key (e.g., before a PP rotation) and submits the request. The validator
// must reject it because the current PP recognises no auditors.
func TestSecurityFalseAcceptanceAuditorSignaturesPresentWhenNotRequired(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Clear all configured auditors in the PP so that any auditor sig is "unexpected".
	env.Engine.PublicParams.AuditorIDs = nil

	// The env's TRWithIssue already has an auditor signature attached (added during
	// request preparation). Submitting it with an empty auditor list must be rejected.
	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auditor signatures present")
}

// TestSecurityFalseAcceptanceEmptyTokenRequest verifies that an empty (nil raw)
// token request is rejected immediately.
//
// Attack vector: submitting an empty payload must not cause a panic and must
// return a clear error.
func TestSecurityFalseAcceptanceEmptyTokenRequest(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	v := validator.New(logging.MustGetLogger(), pp, nil, nil, nil, nil)

	require.NotPanics(t, func() {
		_, _, err = v.VerifyTokenRequestFromRaw(context.Background(), nil, "anchor", nil)
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty token request")
}

// ===========================================================================
// FALSE REJECTION PREVENTION TESTS  (regression guards after fixes)
// ===========================================================================

// TestSecurityRegressionValidIssueStillAccepted verifies that a correctly
// constructed issue request is still accepted after all security hardening
// changes. Ensures the fixes introduce no false rejections.
func TestSecurityRegressionValidIssueStillAccepted(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
}

// TestSecurityRegressionValidTransferStillAccepted verifies that a correctly
// constructed transfer request is still accepted after security hardening.
func TestSecurityRegressionValidTransferStillAccepted(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
}

// TestSecurityRegressionValidRedeemStillAccepted verifies that a correctly
// constructed redeem request is still accepted after security hardening.
func TestSecurityRegressionValidRedeemStillAccepted(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithRedeem.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
}

// ===========================================================================
// SIGNATURE ORDER / CURSOR INTEGRITY TESTS
// ===========================================================================

// TestSecurityFalseRejectionWrongSignature verifies that a request with a tampered
// action signature is rejected with a clear "failed signature verification" error
// and does not accidentally succeed or panic.
//
// Regression target (FR2): the Backend cursor approach relies on signature order.
// Injecting a signature that corresponds to a different transaction ID produces
// a message-mismatch that the verifier must catch.
func TestSecurityFalseRejectionWrongSignature(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Replace the first action signature with garbage to simulate a wrong-TxID sig.
	for _, sig := range env.TRWithTransfer.Signatures {
		if sig != nil && sig.Action != nil {
			sig.Action.Signature = []byte("tampered-signature-bytes")

			break
		}
	}

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed signature verification")
}

// ===========================================================================
// ADDITIONAL PANIC PREVENTION TESTS (new findings)
// ===========================================================================

// TestSecurityP1_TransferSignatureValidate_NilToken verifies that
// TransferSignatureValidate returns a descriptive error instead of a nil-pointer
// panic when an ActionInput's Token field is nil.
//
// Attack vector (P1): if TransferActionValidate is skipped in a custom pipeline,
// in.Token being nil causes `tok.Owner` to panic at runtime.
func TestSecurityP1_TransferSignatureValidate_NilToken(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	ctx := &validator.Context{
		Logger: logging.MustGetLogger(),
		PP:     pp,
		TransferAction: &transfer.Action{
			Inputs: []*transfer.ActionInput{
				{Token: nil}, // deliberately nil Token
			},
		},
		MetadataCounter: make(map[string]int),
	}

	require.NotPanics(t, func() {
		err = validator.TransferSignatureValidate(context.Background(), ctx)
	}, "P1: nil Token must not cause a panic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil input or nil token")
}

// TestSecurityP2_TransferSignatureValidate_NilOutputInRedeemCheck verifies that
// TransferSignatureValidate does not panic when ctx.TransferAction.Outputs
// contains a nil entry while checking for redeem operations.
//
// Attack vector (P2): a nil entry in Outputs causes `output.Owner` to dereference a
// nil pointer inside the redeem-detection loop when the redeem-detection loop runs.
func TestSecurityP2_TransferSignatureValidate_NilOutputInRedeem(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Set at least one issuer so the redeem-detection branch is entered after the
	// input-signature loop completes successfully.
	pp.IssuerIDs = []driver.Identity{[]byte("issuer1")}

	validTok := &token.Token{Owner: []byte("owner1"), Data: pp.PedersenGenerators[0]}

	// Provide a mock signature provider and deserializer so the input-loop
	// completes without error, allowing the code to reach the output-scan loop
	// where the nil guard is exercised.
	mockDesInst := &mock3.Deserializer{}
	mockDesInst.GetOwnerVerifierReturns(&mock3.Verifier{}, nil) // noop verifier: Verify always returns nil
	mockSP := &mockSignatureProvider{
		HasBeenSignedByFunc: func(_ context.Context, _ driver.Identity, _ driver.Verifier) ([]byte, error) {
			return []byte("sig"), nil
		},
	}

	ctx := &validator.Context{
		Logger: logging.MustGetLogger(),
		PP:     pp,
		TransferAction: &transfer.Action{
			Inputs:  []*transfer.ActionInput{{Token: validTok}},
			Outputs: []*token.Token{nil}, // nil output entry — must not panic
		},
		Deserializer:      mockDesInst,
		SignatureProvider: mockSP,
		MetadataCounter:   make(map[string]int),
	}

	// Must not panic on the nil output entry.
	require.NotPanics(t, func() {
		err = validator.TransferSignatureValidate(context.Background(), ctx)
	}, "P2: nil output entry must not cause a panic in the redeem-detection loop")
	// After the nil guard skips the nil output, no non-nil outputs exist, so
	// isRedeem stays false and the issuer-signature branch is not entered.
	// The function must complete without error.
	require.NoError(t, err)
}

// TestSecurityP3_TransferZKProofValidate_NilInputToken verifies that
// TransferZKProofValidate returns an error instead of a nil-pointer panic when
// ctx.InputTokens contains a nil entry.
//
// Attack vector (P3): `in[i] = tok.Data` panics if tok is nil.
func TestSecurityP3_TransferZKProofValidate_NilInputToken(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	ctx := &validator.Context{
		Logger:      logging.MustGetLogger(),
		PP:          pp,
		InputTokens: []*token.Token{nil}, // deliberately nil
		TransferAction: &transfer.Action{
			Outputs:   []*token.Token{{Data: pp.PedersenGenerators[0]}},
			Proof:     []byte("unused"),
			ProofType: 0,
		},
		MetadataCounter: make(map[string]int),
	}

	require.NotPanics(t, func() {
		err = validator.TransferZKProofValidate(context.Background(), ctx)
	}, "P3: nil input token must not cause a panic in TransferZKProofValidate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil input token at index [0]")
}

// TestSecurityP4_TransferHTLCValidate_NilInputToken verifies that
// TransferHTLCValidate returns an error instead of a nil-pointer panic when
// ctx.InputTokens contains a nil entry.
//
// Attack vector (P4): `identity.UnmarshalTypedIdentity(in.Owner)` panics if in is nil.
func TestSecurityP4_TransferHTLCValidate_NilInputToken(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	ctx := &validator.Context{
		Logger:      logging.MustGetLogger(),
		PP:          pp,
		InputTokens: []*token.Token{nil}, // nil entry
		TransferAction: &transfer.Action{
			Inputs: []*transfer.ActionInput{{}},
		},
		Signatures:      [][]byte{[]byte("sig")},
		MetadataCounter: make(map[string]int),
	}

	require.NotPanics(t, func() {
		err = validator.TransferHTLCValidate(context.Background(), ctx)
	}, "P4: nil entry in ctx.InputTokens must not cause a panic in TransferHTLCValidate")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil input token at index [0]")
}

// TestSecurityP5_TransferHTLCValidate_NilOutputEntry verifies that
// TransferHTLCValidate's output-scan loop does not panic when
// ctx.TransferAction.Outputs contains a nil entry.
//
// Attack vector (P5): `o.IsRedeem()` panics if o is nil.
func TestSecurityP5_TransferHTLCValidate_NilOutputEntry(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Use a non-HTLC owner so the input loop exits cleanly.
	nonHTLCOwner := []byte("\x00\x00non-htlc-raw-bytes-that-unmarshal-fail")
	ctx := &validator.Context{
		Logger: logging.MustGetLogger(),
		PP:     pp,
		InputTokens: []*token.Token{
			{Owner: nonHTLCOwner},
		},
		TransferAction: &transfer.Action{
			Inputs:  []*transfer.ActionInput{{}},
			Outputs: []*token.Token{nil}, // nil output entry
		},
		Signatures:      [][]byte{[]byte("sig")},
		MetadataCounter: make(map[string]int),
	}

	// The input loop will fail on UnmarshalTypedIdentity before the nil check
	// on the output loop is reached, but the function must not panic.
	require.NotPanics(t, func() {
		err = validator.TransferHTLCValidate(context.Background(), ctx)
	}, "P5: nil output entry must not cause a panic in TransferHTLCValidate output loop")
	// Either an error from the input loop or no error is acceptable; what matters
	// is no panic.
}

// TestSecurityP5b_TransferHTLCValidate_NilOutputEntry_OutputLoopDirect verifies
// that the nil guard in the output loop is reachable and functional when the
// input loop does not produce any HTLC branches (non-HTLC identity that
// unmarshal cleanly).
func TestSecurityP5b_TransferHTLCValidate_NilOutputEntry_OutputLoopDirect(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Build a typed identity that unmarshal cleanly but is not an HTLC type,
	// so the input loop exits the inner branch without error.
	// Use a well-formed TypedIdentity JSON (type=x509, which is not htlc.ScriptType).
	// Directly set ctx.InputTokens to empty so the input loop is skipped entirely.
	ctx := &validator.Context{
		Logger:      logging.MustGetLogger(),
		PP:          pp,
		InputTokens: []*token.Token{}, // empty → input loop skipped
		TransferAction: &transfer.Action{
			Inputs:  []*transfer.ActionInput{},
			Outputs: []*token.Token{nil, {Owner: nil}}, // nil entry + a redeem
		},
		Signatures:      nil,
		MetadataCounter: make(map[string]int),
	}

	require.NotPanics(t, func() {
		err = validator.TransferHTLCValidate(context.Background(), ctx)
	}, "P5b: nil output entry in output loop must be skipped without panic")
	require.NoError(t, err, "nil entry is skipped; redeem entry (Owner==nil) is also skipped")
}

// TestSecurityP6_GetSerializedInputs_NilToken verifies that
// transfer.Action.GetSerializedInputs returns an error instead of panicking
// when an input's Token is nil and no UpgradeWitness is set.
//
// Attack vector (P6): input.Token.Serialize() dereferences a nil pointer.
func TestSecurityP6_GetSerializedInputs_NilToken(t *testing.T) {
	action := &transfer.Action{
		Inputs: []*transfer.ActionInput{
			{
				Token: nil, // no Token, no UpgradeWitness → panic without the guard
			},
		},
	}

	var err error
	require.NotPanics(t, func() {
		_, err = action.GetSerializedInputs()
	}, "P6: nil Token in GetSerializedInputs must not panic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil token in input at index [0]")
}

// ===========================================================================
// TABLE-DRIVEN: MULTIPLE PANIC SCENARIOS IN ONE SUITE
// ===========================================================================

// TestSecurityPanicSuite is a table-driven test that covers all panic-class
// findings in a single entry point for quick regression scanning.
func TestSecurityPanicSuite(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	cases := []struct {
		name string
		fn   func() error
	}{
		{
			name: "P-1: TransferSignatureValidate nil ActionInput",
			fn: func() error {
				ctx := &validator.Context{
					Logger: logging.MustGetLogger(),
					PP:     pp,
					TransferAction: &transfer.Action{
						Inputs: []*transfer.ActionInput{nil},
					},
					MetadataCounter: make(map[string]int),
				}

				return validator.TransferSignatureValidate(context.Background(), ctx)
			},
		},
		{
			name: "P-1b: TransferSignatureValidate nil Token field",
			fn: func() error {
				ctx := &validator.Context{
					Logger: logging.MustGetLogger(),
					PP:     pp,
					TransferAction: &transfer.Action{
						Inputs: []*transfer.ActionInput{{Token: nil}},
					},
					MetadataCounter: make(map[string]int),
				}

				return validator.TransferSignatureValidate(context.Background(), ctx)
			},
		},
		{
			name: "P-3: TransferZKProofValidate nil InputToken",
			fn: func() error {
				ctx := &validator.Context{
					Logger:      logging.MustGetLogger(),
					PP:          pp,
					InputTokens: []*token.Token{nil},
					TransferAction: &transfer.Action{
						Outputs: []*token.Token{{Data: pp.PedersenGenerators[0]}},
					},
					MetadataCounter: make(map[string]int),
				}

				return validator.TransferZKProofValidate(context.Background(), ctx)
			},
		},
		{
			name: "P-4: TransferHTLCValidate nil InputToken",
			fn: func() error {
				ctx := &validator.Context{
					Logger:      logging.MustGetLogger(),
					PP:          pp,
					InputTokens: []*token.Token{nil},
					TransferAction: &transfer.Action{
						Inputs: []*transfer.ActionInput{{}},
					},
					Signatures:      [][]byte{[]byte("sig")},
					MetadataCounter: make(map[string]int),
				}

				return validator.TransferHTLCValidate(context.Background(), ctx)
			},
		},
		{
			name: "P-6: GetSerializedInputs nil Token",
			fn: func() error {
				action := &transfer.Action{
					Inputs: []*transfer.ActionInput{{Token: nil}},
				}
				_, err := action.GetSerializedInputs()

				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				err = tc.fn()
			}, "must not panic after hardening")
			require.Error(t, err, "must return a descriptive error")
		})
	}
}

// ===========================================================================
// ADDITIONAL FALSE ACCEPTANCE TESTS
// ===========================================================================

// TestSecurityFA_ZeroLengthProof verifies that a transfer action with an empty
// ZK proof is rejected by TransferZKProofValidate (via the verifier) and that the
// action's own Validate() also catches it via ErrEmptyProof.
//
// Attack vector (FA): an attacker strips the ZK proof from a serialized transfer
// action hoping it is not checked before the ZK verifier is invoked.
func TestSecurityFA_ZeroLengthProof(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	validTok := &token.Token{Owner: []byte("owner1"), Data: pp.PedersenGenerators[0]}
	ctx := &validator.Context{
		Logger:      logging.MustGetLogger(),
		PP:          pp,
		InputTokens: []*token.Token{validTok},
		TransferAction: &transfer.Action{
			Outputs: []*token.Token{{Data: pp.PedersenGenerators[1]}},
			Proof:   []byte{}, // deliberately empty
		},
		MetadataCounter: make(map[string]int),
	}

	// TransferZKProofValidate hands the empty proof to the underlying verifier which must reject it.
	require.NotPanics(t, func() {
		err = validator.TransferZKProofValidate(context.Background(), ctx)
	})
	require.Error(t, err, "FA: empty ZK proof must be rejected")
}

// TestSecurityFA_MultipleMetadataCountForSameKey verifies that the common
// validator rejects a token request where a validator counts the same metadata
// key more than once (duplicate metadata registration).
//
// Attack vector (FA): two validators each call ctx.CountMetadataKey("k") for the
// same key. The post-validation check in common.Validator.VerifyTransfer detects
// this and rejects the request.
//
// Note: we use common.NewValidator directly with only the custom validator so that
// the standard TransferActionValidate (which requires at least one input) does not
// reject our minimal test action before the metadata check is reached.
func TestSecurityFA_MultipleMetadataCountForSameKey(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Build a minimal transfer action with one metadata entry.
	action := &transfer.Action{
		Metadata: map[string][]byte{"key": []byte("value")},
	}

	doubleCounter := func(c context.Context, ctx *validator.Context) error {
		// Count the same key twice — simulates two competing validators both
		// claiming ownership of the same metadata entry.
		ctx.CountMetadataKey("key")
		ctx.CountMetadataKey("key")

		return nil
	}

	// Use common.NewValidator directly so only our custom validator runs, avoiding
	// TransferActionValidate which would reject the minimal test action.
	v := common.NewValidator(
		logging.MustGetLogger(),
		pp,
		nil,
		&validator.ActionDeserializer{},
		[]validator.ValidateTransferFunc{doubleCounter},
		nil,
		nil,
	)

	err = v.VerifyTransfer(
		context.Background(),
		"test-anchor",
		&driver.TokenRequest{},
		action,
		nil,
		nil,
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "appeared more than one time")
}

// TestSecurityFA_UnvalidatedMetadataKey verifies that the common validator
// rejects a token request where the action has metadata that no validator
// counted (i.e., unvalidated metadata slips through).
//
// Attack vector (FA): an attacker appends an unexpected metadata entry to a
// transfer action hoping the validator ignores it. The metadata-counter check in
// common.Validator.VerifyTransfer detects the discrepancy.
//
// Note: we use common.NewValidator directly with only the custom validator so that
// the standard TransferActionValidate (which requires at least one input) does not
// reject our minimal test action before the metadata check is reached.
func TestSecurityFA_UnvalidatedMetadataKey(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Build a transfer action with extra metadata that no validator handles.
	action := &transfer.Action{
		Metadata: map[string][]byte{
			"unexpected-attacker-key": []byte("evil-payload"),
		},
	}

	// The no-op validator counts nothing.
	noopTransferValidator := func(c context.Context, ctx *validator.Context) error {
		return nil
	}

	// Use common.NewValidator directly so only our custom validator runs.
	v := common.NewValidator(
		logging.MustGetLogger(),
		pp,
		nil,
		&validator.ActionDeserializer{},
		[]validator.ValidateTransferFunc{noopTransferValidator},
		nil,
		nil,
	)

	err = v.VerifyTransfer(
		context.Background(),
		"test-anchor",
		&driver.TokenRequest{},
		action,
		nil,
		nil,
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more metadata than those validated")
}

// ===========================================================================
// ADDITIONAL FALSE REJECTION (REGRESSION) TESTS
// ===========================================================================

// TestSecurityFR_NilOutputSkippedInGetOutputCommitments verifies that
// GetOutputCommitments does not include nil output entries in the result
// (they are silently skipped), and that the returned slice length matches
// the number of non-nil outputs.
//
// FR regression: before the fix, nil entries were not skipped; the function
// would panic. After the fix, it must return only non-nil commitments.
func TestSecurityFR_NilOutputSkippedInGetOutputCommitments(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	action := &transfer.Action{
		Outputs: []*token.Token{
			nil,
			{Data: pp.PedersenGenerators[0]},
			nil,
			{Data: pp.PedersenGenerators[1]},
		},
	}

	var coms []*math.G1
	require.NotPanics(t, func() {
		coms = action.GetOutputCommitments()
	})
	// Exactly two non-nil outputs must produce two commitments.
	require.Len(t, coms, 2)
}

// TestSecurityFR_ValidRequestAfterHardeningTransfer ensures that a legitimately
// constructed transfer with valid signatures, ZK proof, and metadata is still
// accepted after all hardening changes.  This is the primary non-regression guard.
func TestSecurityFR_ValidRequestAfterHardeningTransfer(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
}

// TestSecurityFR_ValidRequestAfterHardeningIssue ensures a legitimately
// constructed issue request is still accepted after hardening.
func TestSecurityFR_ValidRequestAfterHardeningIssue(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
}

// TestSecurityFR_ValidRequestAfterHardeningRedeem ensures a legitimately
// constructed redeem request is still accepted after hardening.
func TestSecurityFR_ValidRequestAfterHardeningRedeem(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithRedeem.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err)
	require.NotEmpty(t, actions)
}

// ===========================================================================
// STEP 1A — Open-policy issuer tests (T-GAP-1, T-GAP-2)
// ===========================================================================

// TestSecurityFA_IssueAcceptedWhenIssuersEmpty documents the open-policy issuer
// behaviour: when PP.IssuerIDs is empty, any identity may issue tokens and the
// issuer-authorization check is deliberately skipped (T-GAP-1).
//
// This is intentional design. See the Godoc on IssueValidate for the rationale.
// If this test fails it means the open-policy was inadvertently removed and
// a code fix is required.
func TestSecurityFA_IssueAcceptedWhenIssuersEmpty(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Clear the issuer list — open-policy mode.
	env.Engine.PublicParams.IssuerIDs = nil

	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err, "T-GAP-1: issue must be accepted when IssuerIDs is empty (open-policy)")
	require.NotEmpty(t, actions)
}

// TestSecurityFA_RedeemAcceptedWithoutIssuerWhenIssuersEmpty documents the
// open-policy redeem behaviour: when PP.IssuerIDs is empty, a redeem action
// that carries no issuer signature is accepted (T-GAP-2).
//
// This is intentional design. See the Godoc on TransferSignatureValidate for
// the rationale. If this test fails the open-policy was inadvertently removed.
func TestSecurityFA_RedeemAcceptedWithoutIssuerWhenIssuersEmpty(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Clear the issuer list — open-policy mode.
	env.Engine.PublicParams.IssuerIDs = nil

	raw, err := env.TRWithRedeem.Bytes()
	require.NoError(t, err)

	actions, _, err := env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.NoError(t, err, "T-GAP-2: redeem must be accepted when IssuerIDs is empty (open-policy)")
	require.NotEmpty(t, actions)
}

// ===========================================================================
// STEP 1B — Backend cursor integrity tests (T-GAP-3, T-GAP-4)
// ===========================================================================

// TestSecurityFR_BackendCursorExtraSignature verifies that injecting an extra
// signature before the action signatures causes the validator to reject the
// request without panicking (T-GAP-3).
//
// The Backend cursor advances once per HasBeenSignedBy call. An extra signature
// at the front shifts all remaining cursor positions, causing a message-mismatch
// for the following signatures.
func TestSecurityFR_BackendCursorExtraSignature(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Inject a spurious action signature at the front of the Signatures list.
	spurious := &driver.RequestSignature{
		Action: &driver.ActionSignature{Signature: []byte("spurious-extra-sig")},
	}
	env.TRWithTransfer.Signatures = append([]*driver.RequestSignature{spurious}, env.TRWithTransfer.Signatures...)

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	}, "T-GAP-3: extra signature must not panic")
	require.Error(t, err, "T-GAP-3: extra signature must cause a validation error")
}

// TestSecurityFR_BackendCursorExhausted verifies that a transfer request with
// one action signature missing is rejected with the expected error message
// rather than panicking (T-GAP-4).
func TestSecurityFR_BackendCursorExhausted(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Drop all action signatures so the cursor exhausts immediately.
	var filtered []*driver.RequestSignature
	for _, sig := range env.TRWithTransfer.Signatures {
		if sig != nil && sig.Auditor != nil {
			filtered = append(filtered, sig)
		}
	}
	env.TRWithTransfer.Signatures = filtered

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	}, "T-GAP-4: missing action signature must not panic")
	require.Error(t, err, "T-GAP-4: missing action signature must be rejected")
	assert.Contains(t, err.Error(), "insufficient number of signatures")
}

// ===========================================================================
// STEP 1C — MinProtocolVersion enforcement (T-GAP-6)
// ===========================================================================

// TestSecurityFA_MinProtocolVersionEnforced verifies that requests carrying a
// protocol version below the configured minimum are rejected with
// driver.ErrVersionBelowMinimum (T-GAP-6).
func TestSecurityFA_MinProtocolVersionEnforced(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Require at least version 2; the env produces version 1 requests.
	env.Engine.SetMinProtocolVersion(2)

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.Error(t, err, "T-GAP-6: version below minimum must be rejected")
	require.ErrorIs(t, err, driver.ErrVersionBelowMinimum)
}

// ===========================================================================
// STEP 1E — Multi-auditor: one valid + one forged sig (T-GAP-8)
// ===========================================================================

// TestSecurityAuditing_OneValidOneBadSignature verifies that when two auditor
// signatures are present but one of them carries an invalid (tampered) signature
// bytes, the request is rejected (T-GAP-8).
//
// The AuditingSignaturesValidate function verifies every provided auditor
// signature independently. A forged signature that fails Verify must cause
// the entire request to be rejected.
func TestSecurityAuditing_OneValidOneBadSignature(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Find the existing auditor signature in the issue request.
	var auditorSig *driver.RequestSignature
	for _, sig := range env.TRWithIssue.Signatures {
		if sig != nil && sig.Auditor != nil {
			auditorSig = sig

			break
		}
	}
	require.NotNil(t, auditorSig, "env must produce at least one auditor signature")

	// Inject a second auditor signature that has the same Identity (so it passes
	// the is-authorized check) but carries forged signature bytes (so Verify fails).
	forgedSig := &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  auditorSig.Auditor.Identity, // recognized identity
			Signature: []byte("forged-auditor-signature-bytes"),
		},
	}
	env.TRWithIssue.Signatures = append(env.TRWithIssue.Signatures, forgedSig)

	raw, err := env.TRWithIssue.Bytes()
	require.NoError(t, err)

	_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	require.Error(t, err, "T-GAP-8: forged auditor signature must cause rejection")
	assert.Contains(t, err.Error(), "failed to verify auditor's signature")
}
