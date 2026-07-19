/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"bytes"
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyTokenRequestPreservesOriginalActionOrder(t *testing.T) {
	issue := &dmock.IssueAction{}
	transfer0 := &dmock.TransferAction{}
	transfer1 := &dmock.TransferAction{}
	actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
	actionDeserializer.DeserializeActionsReturns(
		[]driver.IssueAction{issue},
		[]driver.TransferAction{transfer0, transfer1},
		nil,
	)
	validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		&logging.MockLogger{}, nil, nil, driver.DefaultResourceLimits(), actionDeserializer, nil, nil, nil,
	)
	requestWithMixedActions := &driver.TokenRequest{Actions: []*driver.TypedAction{
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer-0")},
		{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue")},
		{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer-1")},
	}}

	actions, _, err := validator.VerifyTokenRequest(
		t.Context(), nil, nil, "anchor", requestWithMixedActions, nil,
	)
	require.NoError(t, err)
	require.Equal(t, []any{transfer0, issue, transfer1}, actions)

	raw, err := requestWithMixedActions.Bytes()
	require.NoError(t, err)
	actions, err = validator.UnmarshalActions(raw)
	require.NoError(t, err)
	require.Equal(t, []any{transfer0, issue, transfer1}, actions)
}

func TestVerifyTokenRequestFromRawScopesSignaturesByActionID(t *testing.T) {
	issue := &dmock.IssueAction{}
	transfer := &dmock.TransferAction{}
	actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
	actionDeserializer.DeserializeActionsReturns(
		[]driver.IssueAction{issue},
		[]driver.TransferAction{transfer},
		nil,
	)
	var validationOrder []string
	consume := func(expected []byte, kind string, provider driver.SignatureProvider) error {
		verifier := &dmock.Verifier{}
		sigma, err := provider.HasBeenSignedBy(context.Background(), nil, verifier)
		if err != nil {
			return err
		}
		if !bytes.Equal(expected, sigma) {
			return errors.Errorf("unexpected %s signature [%x]", kind, sigma)
		}
		validationOrder = append(validationOrder, kind)

		return nil
	}
	validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		&logging.MockLogger{},
		nil,
		nil,
		driver.DefaultResourceLimits(),
		actionDeserializer,
		[]ValidateTransferFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
			func(_ context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
				return consume([]byte("transfer-signature"), "transfer", ctx.SignatureProvider)
			},
		},
		[]ValidateIssueFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
			func(_ context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
				return consume([]byte("issue-signature"), "issue", ctx.SignatureProvider)
			},
		},
		nil,
	)
	tokenRequest := &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue")},
		},
		// Deliberately put the issue signature first. ActionID, not envelope order,
		// determines which validator receives each signature.
		Signatures: []*driver.RequestSignature{
			{Action: &driver.ActionSignature{ActionID: 1, Signature: []byte("issue-signature")}},
			{Action: &driver.ActionSignature{ActionID: 0, Signature: []byte("transfer-signature")}},
		},
	}
	raw, err := tokenRequest.Bytes()
	require.NoError(t, err)

	actions, _, err := validator.VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
	require.NoError(t, err)
	require.Equal(t, []any{transfer, issue}, actions)
	require.Equal(t, []string{"transfer", "issue"}, validationOrder)

	// Relabel the same signatures to the opposite actions. Distinct signer plans
	// make the incorrect association observable during signature verification.
	tokenRequest.Signatures[0].Action.ActionID = 0
	tokenRequest.Signatures[1].Action.ActionID = 1
	raw, err = tokenRequest.Bytes()
	require.NoError(t, err)
	_, _, err = validator.VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
	require.ErrorContains(t, err, "unexpected transfer signature")
}

func TestVerifyTokenRequestFromRawRejectsInvalidSignatureEnvelope(t *testing.T) {
	newValidator := func(consumptionCount int) *Validator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer] {
		actionDeserializer := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
		actionDeserializer.DeserializeActionsReturns(nil, []driver.TransferAction{&dmock.TransferAction{}}, nil)

		return NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			&logging.MockLogger{}, nil, nil, driver.DefaultResourceLimits(), actionDeserializer,
			[]ValidateTransferFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					for range consumptionCount {
						if _, err := ctx.SignatureProvider.HasBeenSignedBy(c, nil, &dmock.Verifier{}); err != nil {
							return err
						}
					}

					return nil
				},
			}, nil, nil,
		)
	}
	toRaw := func(t *testing.T, signatures ...*driver.RequestSignature) []byte {
		t.Helper()
		tokenRequest := &driver.TokenRequest{
			Actions:    []*driver.TypedAction{{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer")}},
			Signatures: signatures,
		}
		raw, err := tokenRequest.Bytes()
		require.NoError(t, err)

		return raw
	}

	t.Run("out of range ActionID", func(t *testing.T) {
		raw := toRaw(t, &driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 1, Signature: []byte("signature")}})
		_, _, err := newValidator(1).VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
		require.ErrorIs(t, err, ErrActionSignatureIDOutOfRange)
	})

	t.Run("missing signature", func(t *testing.T) {
		_, _, err := newValidator(1).VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", toRaw(t))
		require.ErrorContains(t, err, "insufficient number of signatures")
	})

	t.Run("surplus signature", func(t *testing.T) {
		raw := toRaw(t,
			&driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: []byte("required")}},
			&driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: []byte("surplus")}},
		)
		_, _, err := newValidator(1).VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
		require.ErrorIs(t, err, ErrUnconsumedSignatures)
	})

	t.Run("multiple required signatures for one action", func(t *testing.T) {
		raw := toRaw(t,
			&driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: []byte("first")}},
			&driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: []byte("second")}},
		)
		_, _, err := newValidator(2).VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
		require.NoError(t, err)
	})
}

func TestVerifyTokenRequestFromRawRejectsNoActions(t *testing.T) {
	validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		&logging.MockLogger{}, nil, nil, driver.DefaultResourceLimits(), &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}, nil, nil, nil,
	)
	raw, err := (&driver.TokenRequest{}).Bytes()
	require.NoError(t, err)

	_, _, err = validator.VerifyTokenRequestFromRaw(t.Context(), nil, "anchor", raw)
	require.ErrorIs(t, err, ErrNoActions)
}

func TestValidatorPublicMethodsRejectNilWithoutPanic(t *testing.T) {
	validator := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		&logging.MockLogger{}, nil, nil, driver.DefaultResourceLimits(), &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}, nil, nil, nil,
	)
	var issue *dmock.IssueAction
	var transfer *dmock.TransferAction

	require.NotPanics(t, func() {
		_, _, err := validator.VerifyTokenRequest(t.Context(), nil, nil, "anchor", nil, nil)
		require.ErrorIs(t, err, ErrNilTokenRequest)
	})
	require.NotPanics(t, func() {
		err := validator.VerifyIssue(t.Context(), "anchor", &driver.TokenRequest{}, issue, nil, nil, nil)
		require.ErrorIs(t, err, ErrNilAction)
	})
	require.NotPanics(t, func() {
		err := validator.VerifyTransfer(t.Context(), "anchor", &driver.TokenRequest{}, transfer, nil, nil, nil)
		require.ErrorIs(t, err, ErrNilAction)
	})

	ctx := &Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{}
	require.NotPanics(t, func() { ctx.CountMetadataKey("key") })
	assert.Equal(t, 1, ctx.MetadataCounter["key"])
}
