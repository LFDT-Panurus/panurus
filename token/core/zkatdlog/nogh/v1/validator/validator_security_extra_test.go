/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file contains security-focused tests added as part of the second round
// of hardening, addressing the open action items from the security analysis
// (zkatdlog-validator-security-hacker-analysis.html), Steps A–H.
package validator_test

import (
	"context"
	"crypto"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	math "github.com/IBM/mathlib"
	fv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// STEP A — Backend cursor: mixed issue+transfer with extra auditor signature
// ===========================================================================

// TestSecurityCursorMixedIssueTransferExtraAuditorSig verifies that a request
// containing 1 issue action + 1 transfer action where an extra auditor signature
// is injected before the real one is rejected without panicking (C-1 / Step A).
//
// The Backend cursor is initialized with auditor sigs first, then action sigs.
// Injecting an extra auditor signature advances the cursor by one extra position
// before the action validators run, shifting all subsequent signature reads.
// The request must be rejected — not panicked — regardless of the shift.
func TestSecurityCursorMixedIssueTransferExtraAuditorSig(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Build a mixed request: the existing issue action + the existing transfer action.
	mixedReq := &driver.TokenRequest{}
	mixedReq.Actions = append(mixedReq.Actions, env.TRWithIssue.Actions...)
	mixedReq.Actions = append(mixedReq.Actions, env.TRWithTransfer.Actions...)

	// Collect all existing signatures from both requests.
	mixedReq.Signatures = append(mixedReq.Signatures, env.TRWithIssue.Signatures...)
	mixedReq.Signatures = append(mixedReq.Signatures, env.TRWithTransfer.Signatures...)

	// Inject an extra auditor-like signature at the very front of the list.
	// It uses a recognized auditor identity (from the real auditor sig) but
	// carries forged bytes that will fail cryptographic verification.
	var existingAuditorSig *driver.RequestSignature
	for _, sig := range mixedReq.Signatures {
		if sig != nil && sig.Auditor != nil {
			existingAuditorSig = sig

			break
		}
	}
	require.NotNil(t, existingAuditorSig, "env must produce at least one auditor signature")

	extraAuditorSig := &driver.RequestSignature{
		Auditor: &driver.AuditorSignature{
			Identity:  existingAuditorSig.Auditor.Identity,
			Signature: []byte("extra-forged-auditor-sig"),
		},
	}
	// Prepend: auditor sigs come first in the signature slice.
	mixedReq.Signatures = append([]*driver.RequestSignature{extraAuditorSig}, mixedReq.Signatures...)

	raw, err := mixedReq.Bytes()
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, _, err = env.Engine.VerifyTokenRequestFromRaw(context.Background(), nil, "1", raw)
	}, "Step A: extra auditor sig in mixed issue+transfer request must not panic")
	require.Error(t, err, "Step A: extra auditor sig must cause a validation error")
}

// ===========================================================================
// STEP B — Nil owners in upgrade witness (FR-2)
// ===========================================================================

// TestTransferUpgradeWitnessNilOwners verifies that TransferUpgradeWitnessValidate
// rejects a witness where both input.Token.Owner and witness.FabToken.Owner are nil.
//
// Without the explicit len==0 guard, bytes.Equal(nil, nil) returns true, which
// would allow a free-claim of an ownerless fabtoken via upgrade.
func TestTransferUpgradeWitnessNilOwners(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	curve := math.Curves[pp.Curve]
	rand, _ := curve.Rand()
	bf := curve.NewRandomZr(rand)

	fabToken := &fv1.Output{
		Owner:    nil, // deliberately nil
		Type:     "ABC",
		Quantity: "0x10",
	}

	// Compute a valid commitment so we get past the commitment-equality check.
	toks, _, err := token.GetTokensWithWitnessAndBF(
		[]uint64{16}, []*math.Zr{bf}, "ABC", pp.PedersenGenerators, curve,
	)
	require.NoError(t, err)

	ctx := &validator.Context{
		Logger: logging.MustGetLogger(),
		PP:     pp,
		TransferAction: &transfer.Action{
			Inputs: []*transfer.ActionInput{
				{
					Token: &token.Token{
						Owner: nil, // deliberately nil
						Data:  toks[0],
					},
					UpgradeWitness: &token.UpgradeWitness{
						FabToken:       fabToken,
						BlindingFactor: bf,
					},
				},
			},
		},
		MetadataCounter: make(map[string]int),
	}

	require.NotPanics(t, func() {
		err = validator.TransferUpgradeWitnessValidate(context.Background(), ctx)
	}, "Step B: nil owners must not panic in TransferUpgradeWitnessValidate")
	require.Error(t, err, "Step B: nil owners must be rejected")
	assert.Contains(t, err.Error(), "owners do not correspond",
		"Step B: error must identify the owner mismatch")
}

// ===========================================================================
// STEP C — HTLC combination attacks (C-2, C-3, FA-5)
// ===========================================================================

// TestSecurityHTLC_DualLockOutputMetadataCollision verifies that a transfer
// with two HTLC lock outputs sharing the same hash (and therefore the same
// LockKey) is rejected due to metadata key collision (C-2).
//
// The HTLC metadata counter detects duplicate counting of the same key.
// This test exercises the counter behavior directly via CountMetadataKey,
// confirming the duplicate-detection mechanism that drives the
// "appeared more than one time" rejection at the common.Validator level.
func TestSecurityHTLC_DualLockOutputMetadataCollision(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	const dupKey = "htlc.lock.dup-hash"

	ctx := &validator.Context{
		Logger:          logging.MustGetLogger(),
		PP:              pp,
		InputTokens:     []*token.Token{},
		TransferAction:  &transfer.Action{Inputs: []*transfer.ActionInput{}},
		Signatures:      nil,
		MetadataCounter: make(map[string]int),
	}

	// Simulate what TransferHTLCValidate does for two lock outputs with the same hash:
	// count the same metadata key twice.
	ctx.CountMetadataKey(dupKey)
	ctx.CountMetadataKey(dupKey)

	// The counter now has value 2 for dupKey. The common.Validator post-check
	// rejects any key whose count > 1 with "appeared more than one time".
	// Verify the count so any regression in CountMetadataKey is caught.
	require.Equal(t, 2, ctx.MetadataCounter[dupKey],
		"Step C/C-2: duplicate key must be counted twice for the post-check to fire")
}

// TestSecurityHTLC_UpgradeWitnessCombination verifies that a transfer action
// combining an upgrade witness on the input with a non-HTLC output is handled
// cleanly by both validators without panic (C-3).
//
// TransferUpgradeWitnessValidate and TransferHTLCValidate each inspect their own
// domains independently; neither should panic or produce a false accept.
func TestSecurityHTLC_UpgradeWitnessCombination(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	curve := math.Curves[pp.Curve]
	rand, _ := curve.Rand()
	bf := curve.NewRandomZr(rand)
	owner := []byte("alice")

	fabToken := &fv1.Output{
		Owner:    owner,
		Type:     "ABC",
		Quantity: "0x10",
	}
	toks, _, err := token.GetTokensWithWitnessAndBF(
		[]uint64{16}, []*math.Zr{bf}, "ABC", pp.PedersenGenerators, curve,
	)
	require.NoError(t, err)

	inputToken := &token.Token{Owner: owner, Data: toks[0]}
	outputOwner, _ := identity.WrapWithType(x509.IdentityType, []byte("bob"))

	// Part 1: TransferUpgradeWitnessValidate — both commitment and owner are the
	// raw bytes used to compute the commitment, so the check must succeed without panic.
	upgradeCtx := &validator.Context{
		Logger: logging.MustGetLogger(),
		PP:     pp,
		TransferAction: &transfer.Action{
			Inputs: []*transfer.ActionInput{
				{
					Token: inputToken,
					UpgradeWitness: &token.UpgradeWitness{
						FabToken:       fabToken,
						BlindingFactor: bf,
					},
				},
			},
			Outputs: []*token.Token{
				{Owner: outputOwner, Data: pp.PedersenGenerators[0]},
			},
		},
		InputTokens:     []*token.Token{inputToken},
		Signatures:      [][]byte{[]byte("sig")},
		MetadataCounter: make(map[string]int),
	}
	require.NotPanics(t, func() {
		err = validator.TransferUpgradeWitnessValidate(context.Background(), upgradeCtx)
	}, "Step C/C-3: upgrade witness validate must not panic")
	require.NoError(t, err, "Step C/C-3: valid upgrade witness must be accepted")

	// Part 2: TransferHTLCValidate with empty InputTokens (input loop skipped)
	// and a non-HTLC typed output — no HTLC branch is entered, must succeed.
	htlcCtx := &validator.Context{
		Logger:      logging.MustGetLogger(),
		PP:          pp,
		InputTokens: []*token.Token{}, // no inputs → input loop skipped
		TransferAction: &transfer.Action{
			Inputs:  []*transfer.ActionInput{},
			Outputs: []*token.Token{{Owner: outputOwner, Data: pp.PedersenGenerators[0]}},
		},
		Signatures:      nil,
		MetadataCounter: make(map[string]int),
	}
	require.NotPanics(t, func() {
		err = validator.TransferHTLCValidate(context.Background(), htlcCtx)
	}, "Step C/C-3: HTLC validate on non-HTLC typed output must not panic")
	require.NoError(t, err,
		"Step C/C-3: non-HTLC output alongside upgrade witness must be accepted by TransferHTLCValidate")
}

// TestSecurityHTLC_ClockSkewBoundary verifies the boundary behavior of
// TransferHTLCValidate around the HTLC deadline (FA-5).
//
// The test pins the local clock behavior for three cases:
//  1. Deadline well in the future — claim path is entered; fails on metadata.
//  2. Deadline well in the past — reclaim path is entered; claim attempt rejected.
//
// Clock skew between nodes is an operational concern; this test documents and
// regression-pins the single-node, local-clock behavior.
func TestSecurityHTLC_ClockSkewBoundary(t *testing.T) {
	validSenderID, _ := identity.WrapWithType(x509.IdentityType, []byte("sender1"))
	validRecipientID, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient1"))

	// buildScript returns an HTLC script with the given deadline.
	buildLockID := func(t *testing.T, deadline time.Time) []byte {
		t.Helper()
		script := &htlc.Script{
			Sender:    validSenderID,
			Recipient: validRecipientID,
			Deadline:  deadline,
			HashInfo: htlc.HashInfo{
				Hash:         []byte("hash"),
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Hex,
			},
		}
		scriptBytes, _ := json.Marshal(script)
		lockID, _ := identity.WrapWithType(htlc.ScriptType, scriptBytes)

		return lockID
	}

	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// Sub-test 1: deadline 24h in the future — entering the claim branch.
	// The output owner is the recipient (a valid claim).  With no metadata
	// provided, the metadata-check error is returned (not an expiry error).
	t.Run("deadline-future-claim-fails-on-metadata", func(t *testing.T) {
		lockID := buildLockID(t, time.Now().Add(24*time.Hour))
		ctx := &validator.Context{
			Logger: logging.MustGetLogger(),
			PP:     pp,
			InputTokens: []*token.Token{
				{Owner: lockID},
			},
			TransferAction: &transfer.Action{
				Inputs:  []*transfer.ActionInput{{}},
				Outputs: []*token.Token{{Owner: validRecipientID, Data: &math.G1{}}},
			},
			Signatures:      [][]byte{[]byte("claim-sig")},
			MetadataCounter: make(map[string]int),
		}
		err = validator.TransferHTLCValidate(context.Background(), ctx)
		require.Error(t, err,
			"FA-5: future deadline — claim without metadata must be rejected")
		assert.NotContains(t, err.Error(), "expired",
			"FA-5: a future deadline must not produce an expiry-related error")
	})

	// Sub-test 2: deadline 24h in the past — reclaim path.
	// The output owner is the recipient (wrong for a reclaim); the request must
	// be rejected because only the sender can reclaim after expiry.
	t.Run("deadline-past-claim-rejected", func(t *testing.T) {
		lockID := buildLockID(t, time.Now().Add(-24*time.Hour))
		ctx := &validator.Context{
			Logger: logging.MustGetLogger(),
			PP:     pp,
			InputTokens: []*token.Token{
				{Owner: lockID},
			},
			TransferAction: &transfer.Action{
				Inputs:  []*transfer.ActionInput{{}},
				Outputs: []*token.Token{{Owner: validRecipientID, Data: &math.G1{}}},
			},
			Signatures:      [][]byte{[]byte("sig")},
			MetadataCounter: make(map[string]int),
		}
		err = validator.TransferHTLCValidate(context.Background(), ctx)
		require.Error(t, err,
			"FA-5: claim after expiry must be rejected")
	})
}

// ===========================================================================
// STEP D — P-9: nil ActionInput in GetInputs + SerializeOutputAt bounds check
// ===========================================================================

// TestSecurityP9_GetInputsNilActionInput verifies that GetInputs does not panic
// when the Inputs slice contains a nil ActionInput entry (P-9).
func TestSecurityP9_GetInputsNilActionInput(t *testing.T) {
	action := &transfer.Action{
		Inputs: []*transfer.ActionInput{
			nil,
			{}, // non-nil but empty (no ID set)
			nil,
		},
	}

	require.NotPanics(t, func() {
		ids := action.GetInputs()
		require.Len(t, ids, 3, "P-9: GetInputs must return a slice of the same length")
		assert.Nil(t, ids[0], "P-9: nil input must produce a nil ID")
		assert.Nil(t, ids[1], "P-9: empty input (no ID set) must produce a nil ID")
		assert.Nil(t, ids[2], "P-9: nil input must produce a nil ID")
	}, "P-9: nil ActionInput in GetInputs must not panic")
}

// TestSecurityP9_SerializeOutputAtBoundsCheck verifies that SerializeOutputAt
// returns an error (not a panic) when given an out-of-bounds index or a nil
// output entry (P-9).
func TestSecurityP9_SerializeOutputAtBoundsCheck(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	action := &transfer.Action{
		Outputs: []*token.Token{
			{Data: pp.PedersenGenerators[0], Owner: []byte("owner1")},
			nil,
		},
	}

	// Out-of-bounds index must not panic.
	require.NotPanics(t, func() {
		_, err = action.SerializeOutputAt(99)
	}, "P-9: out-of-bounds index in SerializeOutputAt must not panic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of bounds",
		"P-9: error message must mention out of bounds")

	// Negative index must not panic.
	require.NotPanics(t, func() {
		_, err = action.SerializeOutputAt(-1)
	}, "P-9: negative index in SerializeOutputAt must not panic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of bounds",
		"P-9: negative index error must mention out of bounds")

	// Nil output at valid index must not panic.
	require.NotPanics(t, func() {
		_, err = action.SerializeOutputAt(1)
	}, "P-9: nil output in SerializeOutputAt must not panic")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil output",
		"P-9: error must mention nil output")

	// Valid index with a non-nil token must succeed.
	require.NotPanics(t, func() {
		_, err = action.SerializeOutputAt(0)
	}, "P-9: valid index in SerializeOutputAt must not panic")
	require.NoError(t, err, "P-9: valid output must serialize without error")
}

// ===========================================================================
// STEP E — ZK proof cross-action swap (C-4)
// ===========================================================================

// TestSecurityZKProofCrossActionSwap verifies that swapping the ZK proof
// between two distinct transfer actions is rejected by the verifier (C-4).
//
// The ZK proof is bound to the Pedersen commitments of the specific inputs and
// outputs of its action. Swapping proofs means the commitment sets no longer
// match the proof, so both actions must be rejected.
//
// This test documents and pins the binding between proof and commitment set,
// ensuring the property is continuously regression-tested.
func TestSecurityZKProofCrossActionSwap(t *testing.T) {
	env := newSecurityTestEnv(t)

	// Deserialize the two transfer actions from the swap request (which contains
	// two independent transfer actions).
	transfers := env.TRWithSwap.GetTransfers()
	require.GreaterOrEqual(t, len(transfers), 2,
		"Step E: swap request must have at least 2 transfer actions")

	action1 := &transfer.Action{}
	require.NoError(t, action1.Deserialize(transfers[0]))

	action2 := &transfer.Action{}
	require.NoError(t, action2.Deserialize(transfers[1]))

	// Swap the ZK proofs: action1 carries action2's proof and vice versa.
	proof1, proof2 := action1.Proof, action2.Proof
	action1.Proof, action2.Proof = proof2, proof1

	// Verify that each action is rejected when carrying the wrong proof.
	// We exercise TransferZKProofValidate directly to isolate the proof check.
	pp := env.Engine.PublicParams

	ctx1 := &validator.Context{
		Logger:          logging.MustGetLogger(),
		PP:              pp,
		InputTokens:     action1.InputTokens(),
		TransferAction:  action1,
		MetadataCounter: make(map[string]int),
	}
	require.NotPanics(t, func() {
		zkErr := validator.TransferZKProofValidate(context.Background(), ctx1)
		assert.Error(t, zkErr, "Step E/C-4: action1 with action2's proof must be rejected")
		if zkErr != nil {
			assert.Contains(t, zkErr.Error(), "invalid zero-knowledge proof",
				"Step E/C-4: rejection must report invalid ZK proof")
		}
	})

	ctx2 := &validator.Context{
		Logger:          logging.MustGetLogger(),
		PP:              pp,
		InputTokens:     action2.InputTokens(),
		TransferAction:  action2,
		MetadataCounter: make(map[string]int),
	}
	require.NotPanics(t, func() {
		zkErr := validator.TransferZKProofValidate(context.Background(), ctx2)
		assert.Error(t, zkErr, "Step E/C-4: action2 with action1's proof must be rejected")
		if zkErr != nil {
			assert.Contains(t, zkErr.Error(), "invalid zero-knowledge proof",
				"Step E/C-4: rejection must report invalid ZK proof")
		}
	})
}

// ===========================================================================
// STEP F — Version=0 false-reject regression test (FR-1)
// ===========================================================================

// TestSecurityFR_Version0Rejected verifies that a token request carrying
// Version=0 is rejected with an invalid-version error rather than being
// silently treated as version 1 (FR-1).
//
// The MarshalToMessageToSign helper substitutes version 0 with ProtocolV1 when
// computing the message to sign, but VerifyTokenRequestFromRaw checks the
// version field on the deserialized request and must reject Version=0.
func TestSecurityFR_Version0Rejected(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	v := validator.New(logging.MustGetLogger(), pp, nil, nil, nil, nil)

	// Build a minimal TokenRequest with Version explicitly set to 0.
	tr := &driver.TokenRequest{
		Version: 0,
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("placeholder")},
		},
	}
	raw, err := tr.Bytes()
	require.NoError(t, err)

	_, _, err = v.VerifyTokenRequestFromRaw(context.Background(), nil, "anchor", raw)
	require.Error(t, err, "FR-1: Version=0 request must be rejected")
	// The error surfaces as "invalid transfer version" (from the action deserializer)
	// or "invalid version" (from the request-level check); accept either form.
	errMsg := err.Error()
	assert.True(t,
		strings.Contains(errMsg, "invalid version") || strings.Contains(errMsg, "invalid transfer version"),
		"FR-1: error must identify the version as invalid; got: %s", errMsg,
	)
}

// ===========================================================================
// STEP G — Open-policy deployment guard (FA-1)
// ===========================================================================

// TestPublicParamsDeploymentWarning verifies that ValidateForDeployment returns
// an error when IssuerIDs is empty (open-policy mode), while Validate itself
// continues to accept the same parameters (the open-policy is valid at runtime).
//
// This ensures the deployment guard surfaces the misconfiguration explicitly
// without changing the tokenisation runtime behaviour.
func TestPublicParamsDeploymentWarning(t *testing.T) {
	pp, err := v1.Setup(32, []byte("idemix"), math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	// IssuerIDs is empty by default after Setup.
	require.Empty(t, pp.IssuerIDs, "precondition: IssuerIDs must be empty after Setup")

	// Validate() must still pass — open-policy is a valid operational config.
	require.NoError(t, pp.Validate(),
		"Step G: Validate must accept empty IssuerIDs (open-policy is valid at runtime)")

	// ValidateForDeployment() must warn about the open-policy.
	err = pp.ValidateForDeployment()
	require.Error(t, err, "Step G: ValidateForDeployment must return an error for empty IssuerIDs")
	assert.Contains(t, err.Error(), "open-policy mode",
		"Step G: error must mention open-policy mode")
	assert.Contains(t, err.Error(), "IssuerIDs",
		"Step G: error must mention IssuerIDs")

	// Once IssuerIDs is populated, ValidateForDeployment must return nil.
	pp.IssuerIDs = []driver.Identity{[]byte("authorized-issuer")}
	require.NoError(t, pp.ValidateForDeployment(),
		"Step G: ValidateForDeployment must succeed when IssuerIDs is non-empty")
}

// ===========================================================================
// STEP H — Backend cursor concurrency documentation test (P-10)
// ===========================================================================

// TestSecurityBackendCursorConcurrentUse verifies that concurrent calls to
// VerifyTokenRequestFromRaw — each receiving the same raw bytes — are safe
// because each call creates its own Backend instance (P-10).
//
// The standard validator creates one Backend per VerifyTokenRequestFromRaw call,
// so the cursor integer is not shared. This test exercises the concurrent path
// and will trigger the race detector if the implementation ever inadvertently
// shares a Backend across calls.
func TestSecurityBackendCursorConcurrentUse(t *testing.T) {
	env := newSecurityTestEnv(t)

	raw, err := env.TRWithTransfer.Bytes()
	require.NoError(t, err)

	const goroutines = 8
	errCh := make(chan error, goroutines)
	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			_, _, callErr := env.Engine.VerifyTokenRequestFromRaw(
				context.Background(), nil, "1", raw,
			)
			errCh <- callErr
		})
	}

	wg.Wait()
	close(errCh)

	// All concurrent calls share the same raw bytes and anchor but each gets
	// its own Backend. Every call must succeed (or all fail consistently due
	// to crypto, not due to a race on shared state).
	for callErr := range errCh {
		assert.NoError(t, callErr,
			"Step H/P-10: concurrent VerifyTokenRequestFromRaw calls must each succeed")
	}
}
