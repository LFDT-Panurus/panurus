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

const (
	maxFuzzRequestBytes   = 256 << 10
	maxFuzzSignatureBytes = 4 << 10
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
		if len(raw) > maxFuzzRequestBytes {
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
			&logging.MockLogger{}, nil, nil, actionDeserializer, nil, nil, nil,
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
		if len(signature) == 0 || len(signature) > maxFuzzSignatureBytes {
			t.Skip()
		}
		actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
		actionDeserializer.DeserializeActionsReturns(nil, []driver.TransferAction{&dmock.TransferAction{}}, nil)
		validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			&logging.MockLogger{}, nil, nil, actionDeserializer,
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
