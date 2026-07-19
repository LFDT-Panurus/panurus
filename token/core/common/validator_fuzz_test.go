/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/require"
)

func FuzzVerifyTokenRequestFromRawNoPanic(f *testing.F) {
	valid, err := (&driver.TokenRequest{Actions: []*driver.TypedAction{
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer")},
	}}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte("malformed"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		limits := driver.DefaultResourceLimits()
		if len(raw) > limits.MaxRequestBytes {
			t.Skip()
		}
		actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
		actionDeserializer.DeserializeActionsCalls(func(tr *driver.TokenRequest) ([]driver.IssueAction, []driver.TransferAction, error) {
			issues := make([]driver.IssueAction, 0, tr.NumIssues())
			transfers := make([]driver.TransferAction, 0, tr.NumTransfers())
			for _, action := range tr.Actions {
				switch action.Type {
				case request.ActionType_ACTION_TYPE_ISSUE:
					issues = append(issues, &dmock.IssueAction{})
				case request.ActionType_ACTION_TYPE_TRANSFER:
					transfers = append(transfers, &dmock.TransferAction{})
				}
			}

			return issues, transfers, nil
		})
		validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			&logging.MockLogger{}, nil, nil, limits, actionDeserializer, nil, nil, nil,
		)

		require.NotPanics(t, func() {
			actions, _, validationErr := validator.VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
			if validationErr == nil {
				decoded := &driver.TokenRequest{}
				require.NoError(t, decoded.FromBytes(raw))
				require.Len(t, actions, len(decoded.Actions))
			}
		})
	})
}

func FuzzStructuredTokenRequestSignatureEnvelope(f *testing.F) {
	f.Add(uint32(0), []byte("signature"), false)
	f.Add(uint32(1), []byte("signature"), false)
	f.Add(uint32(0), []byte("signature"), true)

	f.Fuzz(func(t *testing.T, actionID uint32, signature []byte, appendExtra bool) {
		limits := driver.DefaultResourceLimits()
		if len(signature) == 0 || len(signature) > limits.MaxSignatureBytes {
			t.Skip()
		}
		actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
		actionDeserializer.DeserializeActionsReturns(nil, []driver.TransferAction{&dmock.TransferAction{}}, nil)
		validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			&logging.MockLogger{}, nil, nil, limits, actionDeserializer,
			[]ValidateTransferFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					_, err := ctx.SignatureProvider.HasBeenSignedBy(c, nil, &dmock.Verifier{})

					return err
				},
			}, nil, nil,
		)
		tokenRequest := &driver.TokenRequest{
			Actions: []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer")}},
			Signatures: []*driver.RequestSignature{{
				Action: &driver.ActionSignature{ActionID: actionID, Signature: signature},
			}},
		}
		if appendExtra {
			tokenRequest.Signatures = append(tokenRequest.Signatures, &driver.RequestSignature{
				Action: &driver.ActionSignature{ActionID: actionID, Signature: signature},
			})
		}
		raw, err := tokenRequest.Bytes()
		require.NoError(t, err)

		require.NotPanics(t, func() {
			_, _, _ = validator.VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
		})
	})
}

// FuzzRequestResourceLimits fuzzes token requests shaped by their resource dimensions (action
// count, signature count, per-action bytes, per-signature bytes) and asserts that
// VerifyTokenRequestFromRaw never panics, and that any request exceeding a configured limit is
// rejected with the corresponding typed error rather than reaching signature verification.
func FuzzRequestResourceLimits(f *testing.F) {
	limits := driver.DefaultResourceLimits()
	f.Add(1, 0, 8, 8)
	f.Add(limits.MaxActions, 0, 8, 8)
	f.Add(limits.MaxActions+1, 0, 8, 8)
	f.Add(1, 1, 8, 8)
	f.Add(1, limits.MaxSignatures, 8, 8)
	f.Add(1, limits.MaxSignatures+1, 8, 8)
	f.Add(1, 0, limits.MaxActionBytes, 8)
	f.Add(1, 0, limits.MaxActionBytes+1, 8)
	f.Add(1, 1, 8, limits.MaxSignatureBytes)
	f.Add(1, 1, 8, limits.MaxSignatureBytes+1)

	f.Fuzz(func(t *testing.T, numActions, numSignatures, actionBytes, sigBytes int) {
		// Bound fuzzed counts/sizes to a range that keeps the test fast while still crossing
		// every configured limit; the values above MaxActions/MaxSignatures/MaxActionBytes/
		// MaxSignatureBytes are the ones this test cares about observing rejection for.
		numActions = boundInt(numActions, 0, limits.MaxActions+8)
		numSignatures = boundInt(numSignatures, 0, limits.MaxSignatures+8)
		// Actions and signatures must carry at least one byte: TokenRequest.Validate rejects
		// empty Raw/Signature bytes before any resource-limit check runs.
		actionBytes = boundInt(actionBytes, 1, limits.MaxActionBytes+8)
		sigBytes = boundInt(sigBytes, 1, limits.MaxSignatureBytes+8)

		tr := &driver.TokenRequest{
			Actions: actionsOfLen(numActions),
		}
		for i := range tr.Actions {
			tr.Actions[i].Raw = make([]byte, actionBytes)
		}
		tr.Signatures = make([]*driver.RequestSignature, numSignatures)
		for i := range tr.Signatures {
			tr.Signatures[i] = &driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: make([]byte, sigBytes)}}
		}

		raw, err := tr.Bytes()
		require.NoError(t, err)

		actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
		actionDeserializer.DeserializeActionsCalls(func(tr *driver.TokenRequest) ([]driver.IssueAction, []driver.TransferAction, error) {
			transfers := make([]driver.TransferAction, len(tr.Actions))
			for i := range transfers {
				transfers[i] = &dmock.TransferAction{}
			}

			return nil, transfers, nil
		})
		validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			&logging.MockLogger{}, nil, nil, limits, actionDeserializer, nil, nil, nil,
		)

		var validationErr error
		require.NotPanics(t, func() {
			_, _, validationErr = validator.VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
		})

		switch {
		case len(raw) > limits.MaxRequestBytes:
			// The raw envelope size gate runs before the request is even parsed, so an
			// oversized action or signature payload may be caught here first.
			require.ErrorIs(t, validationErr, ErrRequestTooLarge)
		case numActions == 0:
			require.ErrorIs(t, validationErr, ErrNoActions)
		case numActions > limits.MaxActions:
			require.ErrorIs(t, validationErr, ErrTooManyActions)
		case numSignatures > limits.MaxSignatures:
			require.ErrorIs(t, validationErr, ErrTooManySignatures)
		case actionBytes > limits.MaxActionBytes:
			require.ErrorIs(t, validationErr, ErrActionTooLarge)
		case numSignatures > 0 && sigBytes > limits.MaxSignatureBytes:
			require.ErrorIs(t, validationErr, ErrSignatureTooLarge)
		}
	})
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
