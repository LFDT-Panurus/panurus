/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"crypto"
	"encoding/json"
	"testing"
	"time"

	fbactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	driverv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

func FuzzActionDeserializerNoPanic(f *testing.F) {
	issueRaw, err := (&actions.IssueAction{Issuer: []byte("issuer")}).Serialize()
	require.NoError(f, err)
	transferRaw, err := (&actions.TransferAction{}).Serialize()
	require.NoError(f, err)
	f.Add(uint8(0), issueRaw)
	f.Add(uint8(1), transferRaw)
	f.Add(uint8(1), []byte("malformed"))

	limits := driver.DefaultResourceLimits()
	f.Fuzz(func(t *testing.T, actionKind uint8, raw []byte) {
		if len(raw) > limits.MaxActionBytes {
			t.Skip()
		}
		typeID := request.ActionType_ACTION_TYPE_ISSUE
		if actionKind%2 == 1 {
			typeID = request.ActionType_ACTION_TYPE_TRANSFER
		}
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: typeID, Raw: raw}}}

		require.NotPanics(t, func() {
			_, _, _ = (&validator.ActionDeserializer{Limits: limits}).DeserializeActions(tokenRequest)
		})
	})
}

// marshalFuzzedIssueAction builds the raw protobuf bytes of an issue action with the given
// output count, bypassing IssueAction.Serialize so that out-of-limit shapes (which Serialize's
// own caller never produces) can still be exercised.
func marshalFuzzedIssueAction(outputs int) []byte {
	ia := &fbactions.IssueAction{
		Version: actions.ProtocolV1,
		Issuer:  &driverv1.Identity{Raw: []byte("issuer")},
	}
	for range outputs {
		ia.Outputs = append(ia.Outputs, &fbactions.IssueActionOutput{Token: &fbactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, _ := proto.Marshal(ia)

	return raw
}

// marshalFuzzedTransferAction mirrors marshalFuzzedIssueAction for transfer actions.
func marshalFuzzedTransferAction(inputs, outputs int) []byte {
	ta := &fbactions.TransferAction{
		Version: actions.ProtocolV1,
	}
	for range inputs {
		ta.Inputs = append(ta.Inputs, &fbactions.TransferActionInput{Input: &fbactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	for range outputs {
		ta.Outputs = append(ta.Outputs, &fbactions.TransferActionOutput{Token: &fbactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, _ := proto.Marshal(ta)

	return raw
}

// boundInt clamps n into [lo, hi], using n's magnitude modulo the range width so that fuzzed
// values (including negatives) still exercise the full range deterministically.
func boundInt(n, lo, hi int) int {
	if hi <= lo {
		return lo
	}
	width := hi - lo + 1
	if n < 0 {
		n = -n
	}

	return lo + n%width
}

// FuzzActionResourceLimits fuzzes issue and transfer actions shaped by their resource
// dimensions (input/output counts) and asserts that Deserialize never panics and rejects
// any dimension that exceeds its configured limit with the corresponding typed error.
func FuzzActionResourceLimits(f *testing.F) {
	limits := driver.DefaultResourceLimits()
	f.Add(true, 1, 1)
	f.Add(true, 1, limits.MaxOutputs)
	f.Add(true, 1, limits.MaxOutputs+1)
	f.Add(false, 1, 1)
	f.Add(false, limits.MaxInputs, 1)
	f.Add(false, limits.MaxInputs+1, 1)
	f.Add(false, 1, limits.MaxOutputs)
	f.Add(false, 1, limits.MaxOutputs+1)

	f.Fuzz(func(t *testing.T, isIssue bool, inputs, outputs int) {
		inputs = boundInt(inputs, 1, 512)
		outputs = boundInt(outputs, 1, 512)

		var raw []byte
		var err error
		if isIssue {
			raw = marshalFuzzedIssueAction(outputs)
			require.NotPanics(t, func() {
				err = (&actions.IssueAction{}).Deserialize(raw)
			})
			if outputs > limits.MaxOutputs {
				require.ErrorIs(t, err, actions.ErrTooManyOutputs)
			}

			return
		}

		raw = marshalFuzzedTransferAction(inputs, outputs)
		require.NotPanics(t, func() {
			err = (&actions.TransferAction{}).Deserialize(raw)
		})
		switch {
		case inputs > limits.MaxInputs:
			require.ErrorIs(t, err, actions.ErrTooManyInputs)
		case outputs > limits.MaxOutputs:
			require.ErrorIs(t, err, actions.ErrTooManyOutputs)
		}
	})
}

// isHTLCOwner reports whether raw is a typed identity wrapping an HTLC script.
func isHTLCOwner(raw []byte) bool {
	owner, err := identity.UnmarshalTypedIdentity(raw)
	if err != nil {
		return false
	}

	return owner.Type == htlc.ScriptType
}

// FuzzTransferHTLCValidateNoPanic fuzzes the attacker-controlled owner bytes of the inputs of a
// transfer action and asserts two properties of TransferHTLCValidate:
//  1. it never panics, whatever the owner bytes and however many inputs there are;
//  2. it never accepts an action in which an HTLC-owned input is accompanied by any other input.
//
// Property 2 is the regression guard for #2025, where every check in the HTLC branch was
// hardcoded to InputTokens[0], so inputs 1..n of a multi-input HTLC transfer were never
// validated and a structurally invalid action was accepted with a nil error.
func FuzzTransferHTLCValidateNoPanic(f *testing.F) {
	sender, err := identity.WrapWithType(x509.IdentityType, []byte("sender"))
	require.NoError(f, err)
	recipient, err := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
	require.NoError(f, err)

	preimage := []byte("preimage")
	hash := crypto.SHA256.New()
	hash.Write(preimage)
	img := hash.Sum(nil)
	script := &htlc.Script{
		Sender:    sender,
		Recipient: recipient,
		Deadline:  time.Now().Add(-1 * time.Hour), // expired -> Reclaim branch
		HashInfo: htlc.HashInfo{
			Hash:         img,
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Base64,
		},
	}
	scriptBytes, err := json.Marshal(script)
	require.NoError(f, err)
	htlcOwner, err := identity.WrapWithType(htlc.ScriptType, scriptBytes)
	require.NoError(f, err)

	// WrapWithType returns identity.Identity, a named []byte; f.Add requires the exact
	// parameter types of the fuzz target, hence the explicit conversions.
	htlcRaw, senderRaw := []byte(htlcOwner), []byte(sender)
	f.Add(1, htlcRaw, senderRaw)   // valid single-input reclaim
	f.Add(2, htlcRaw, htlcRaw)     // #2025 reproduction shape
	f.Add(2, htlcRaw, senderRaw)   // htlc input plus an unrelated input
	f.Add(2, senderRaw, htlcRaw)   // htlc input at a non-zero index
	f.Add(1, senderRaw, senderRaw) // no htlc at all
	f.Add(1, []byte{}, senderRaw)  // empty owner
	f.Add(1, []byte("trunc"), senderRaw)
	f.Add(3, htlcRaw, scriptBytes) // unwrapped script bytes as an owner

	f.Fuzz(func(t *testing.T, inputs int, owner0, owner1 []byte) {
		n := boundInt(inputs, 1, 8)

		inputTokens := make([]*actions.Output, 0, n)
		signatures := make([][]byte, 0, n)
		for i := range n {
			owner := owner0
			if i%2 == 1 {
				owner = owner1
			}
			inputTokens = append(inputTokens, &actions.Output{Owner: owner, Type: "ABC", Quantity: "100"})
			signatures = append(signatures, []byte("sig"))
		}

		c := &validator.Context{
			TransferAction: &actions.TransferAction{
				Outputs: []*actions.Output{{Owner: sender, Type: "ABC", Quantity: "100"}},
			},
			InputTokens:     inputTokens,
			Signatures:      signatures,
			MetadataCounter: make(map[string]int),
		}

		var err error
		require.NotPanics(t, func() {
			err = validator.TransferHTLCValidate(context.Background(), c)
		})

		// An HTLC-owned input may only be spent by a 1-to-1 transfer: as soon as there is
		// more than one input, the action must be rejected.
		if n > 1 && (isHTLCOwner(owner0) || isHTLCOwner(owner1)) {
			require.Error(t, err, "multi-input transfer with an htlc-owned input must be rejected")
		}
	})
}

// FuzzTransferValidationStepsNoPanic deserializes a transfer action from fuzzed bytes and runs the
// four transfer validation steps in a fuzz-chosen order, asserting that no ordering of the steps
// can panic. Deserializing attacker-controlled bytes can legitimately produce an action carrying
// nil input tokens and nil output entries (a nil token inside a protobuf input or output is
// preserved as a nil entry), so every step must guard those on its own rather than rely on
// TransferActionValidate — which rejects them — having run first.
func FuzzTransferValidationStepsNoPanic(f *testing.F) {
	transferRaw, err := (&actions.TransferAction{
		Inputs: []*actions.TransferActionInput{
			{ID: &token.ID{TxId: "tx1"}, Input: &actions.Output{Owner: []byte("owner1"), Type: "ABC", Quantity: "0x64"}},
		},
		Outputs: []*actions.Output{{Owner: []byte("owner2"), Type: "ABC", Quantity: "0x64"}},
	}).Serialize()
	require.NoError(f, err)
	f.Add(uint8(0), transferRaw)
	f.Add(uint8(1), transferRaw)
	// an input and an output both carrying a nil token: both deserialize to nil entries
	f.Add(uint8(0), marshalFuzzedNilEntriesTransferAction())
	f.Add(uint8(0), []byte{})
	f.Add(uint8(0), []byte("malformed"))

	// every permutation of the four transfer steps of the default pipeline, so that no step can
	// assume any other one ran before it
	steps := []validator.ValidateTransferFunc{
		validator.TransferActionValidate,
		validator.TransferSignatureValidate,
		validator.TransferBalanceValidate,
		validator.TransferHTLCValidate,
	}
	orders := permutations(len(steps))
	logger := logging.MustGetLogger("fuzz")
	limits := driver.DefaultResourceLimits()

	f.Fuzz(func(t *testing.T, orderIdx uint8, raw []byte) {
		if len(raw) > limits.MaxActionBytes {
			t.Skip()
		}
		ta := &actions.TransferAction{}
		ta.SetLimits(limits)
		if err := ta.Deserialize(raw); err != nil {
			t.Skip()
		}

		deserializer := &mock.Deserializer{}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		deserializer.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider := &mock.SignatureProvider{}
		sigProvider.HasBeenSignedByReturns([]byte("sig"), nil)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                &setup.PublicParams{QuantityPrecision: 64, IssuerIDs: []driver.Identity{[]byte("issuer1")}},
			MetadataCounter:   make(map[string]int),
		}

		order := orders[int(orderIdx)%len(orders)]
		require.NotPanics(t, func() {
			for _, i := range order {
				if err := steps[i](context.Background(), c); err != nil {
					// a validation error is a valid outcome for any step at any position
					return
				}
			}
		})
	})
}

// marshalFuzzedNilEntriesTransferAction builds the raw bytes of a transfer action whose input and
// output both carry a nil token, which Deserialize turns into nil entries in the action.
func marshalFuzzedNilEntriesTransferAction() []byte {
	ta := &fbactions.TransferAction{
		Version: actions.ProtocolV1,
		Inputs:  []*fbactions.TransferActionInput{{TokenId: &driverv1.TokenID{TxId: "tx1"}}},
		Outputs: []*fbactions.TransferActionOutput{{}},
	}
	raw, _ := proto.Marshal(ta)

	return raw
}

// permutations returns every permutation of the indices [0, n).
func permutations(n int) [][]int {
	if n <= 1 {
		return [][]int{make([]int, n)}
	}
	var res [][]int
	for _, sub := range permutations(n - 1) {
		for pos := range n {
			p := make([]int, 0, n)
			p = append(p, sub[:pos]...)
			p = append(p, n-1)
			p = append(p, sub[pos:]...)
			res = append(res, p)
		}
	}

	return res
}
