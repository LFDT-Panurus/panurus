/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnchorInContext checks that the anchor is set in the context
func TestAnchorInContext(t *testing.T) {
	anchor := driver.TokenRequestAnchor("hello world")
	anotherAnchor := driver.TokenRequestAnchor("another anchor")
	v := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		&logging.MockLogger{},
		nil,
		nil,
		nil,
		[]ValidateTransferFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
			func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
				if anchor != ctx.Anchor {
					return fmt.Errorf("transfer, anchor does not match, expected %s, got %s", anchor, ctx.Anchor)
				}

				return nil
			},
		},
		[]ValidateIssueFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
			func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
				if anchor != ctx.Anchor {
					return fmt.Errorf("issue, anchor does not match, expected %s, got %s", anchor, ctx.Anchor)
				}

				return nil
			},
		},
		[]ValidateAuditingFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
			func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
				if anchor != ctx.Anchor {
					return fmt.Errorf("audit, anchor does not match, expected %s, got %s", anchor, ctx.Anchor)
				}

				return nil
			},
		},
	)

	// check anchor in the context for an issue action
	ctx := t.Context()
	err := v.VerifyIssue(ctx, anchor, nil, &dmock.IssueAction{}, nil, nil, nil)
	require.NoError(t, err)
	err = v.VerifyIssue(ctx, anotherAnchor, nil, &dmock.IssueAction{}, nil, nil, nil)
	require.Error(t, err)
	require.EqualError(t, err, "issue, anchor does not match, expected hello world, got another anchor")

	// check anchor in the context for a transfer action
	err = v.VerifyTransfer(ctx, anchor, nil, &dmock.TransferAction{}, nil, nil, nil)
	require.NoError(t, err)
	err = v.VerifyTransfer(ctx, anotherAnchor, nil, &dmock.TransferAction{}, nil, nil, nil)
	require.Error(t, err)
	require.EqualError(t, err, "transfer, anchor does not match, expected hello world, got another anchor")

	// check anchor in the context for a transfer action
	err = v.VerifyAuditing(ctx, anchor, nil, nil, nil, nil)
	require.NoError(t, err)
	err = v.VerifyAuditing(ctx, anotherAnchor, nil, nil, nil, nil)
	require.Error(t, err)
	require.EqualError(t, err, "audit, anchor does not match, expected hello world, got another anchor")
}

func TestValidatorWithCounterfeiter(t *testing.T) {
	logger := &logging.MockLogger{}
	pp := &dmock.PublicParameters{}
	des := &dmock.Deserializer{}
	ad := &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}
	ctx := context.Background()
	anchor := driver.TokenRequestAnchor("anchor")

	v := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		logger, pp, des, ad, nil, nil, nil,
	)

	t.Run("VerifyTokenRequest_Success", func(t *testing.T) {
		tr := &driver.TokenRequest{}
		ia := []driver.IssueAction{&dmock.IssueAction{}}
		ta := []driver.TransferAction{&dmock.TransferAction{}}
		ad.DeserializeActionsReturns(ia, ta, nil)

		actions, attrs, err := v.VerifyTokenRequest(ctx, nil, nil, anchor, tr, nil)
		require.NoError(t, err)
		assert.Len(t, actions, 2)
		assert.Nil(t, attrs)
	})

	t.Run("VerifyTokenRequest_AuditingError", func(t *testing.T) {
		vErr := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			logger, pp, des, ad, nil, nil, []ValidateAuditingFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					return errors.New("audit failed")
				},
			},
		)
		_, _, err := vErr.VerifyTokenRequest(ctx, nil, nil, anchor, &driver.TokenRequest{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "audit failed")
	})

	t.Run("VerifyTokenRequest_DeserializeActionsError", func(t *testing.T) {
		ad.DeserializeActionsReturns(nil, nil, errors.New("deserialization failed"))
		_, _, err := v.VerifyTokenRequest(ctx, nil, nil, anchor, &driver.TokenRequest{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deserialization failed")
	})

	t.Run("VerifyTokenRequest_VerifyIssueError", func(t *testing.T) {
		vErr := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			logger, pp, des, ad, nil, []ValidateIssueFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					return errors.New("issue validation failed")
				},
			}, nil,
		)
		ad.DeserializeActionsReturns([]driver.IssueAction{&dmock.IssueAction{}}, nil, nil)
		_, _, err := vErr.VerifyTokenRequest(ctx, nil, nil, anchor, &driver.TokenRequest{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "issue validation failed")
	})

	t.Run("VerifyTokenRequest_VerifyTransferError", func(t *testing.T) {
		vErr := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			logger, pp, des, ad, []ValidateTransferFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					return errors.New("transfer validation failed")
				},
			}, nil, nil,
		)
		ad.DeserializeActionsReturns(nil, []driver.TransferAction{&dmock.TransferAction{}}, nil)
		_, _, err := vErr.VerifyTokenRequest(ctx, nil, nil, anchor, &driver.TokenRequest{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transfer validation failed")
	})

	t.Run("UnmarshalActions", func(t *testing.T) {
		tr := &driver.TokenRequest{}
		raw, err := tr.Bytes()
		require.NoError(t, err)

		ad.DeserializeActionsReturns([]driver.IssueAction{&dmock.IssueAction{}}, nil, nil)
		actions, err := v.UnmarshalActions(raw)
		require.NoError(t, err)
		assert.Len(t, actions, 1)

		_, err = v.UnmarshalActions([]byte("invalid"))
		require.Error(t, err)

		ad.DeserializeActionsReturns(nil, nil, errors.New("bad actions"))
		_, err = v.UnmarshalActions(raw)
		require.Error(t, err)
	})

	t.Run("VerifyIssue_MetadataError", func(t *testing.T) {
		issue := &dmock.IssueAction{}
		issue.GetMetadataReturns(map[string][]byte{"k1": nil})

		err := v.VerifyIssue(ctx, anchor, &driver.TokenRequest{}, issue, nil, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more metadata than those validated")

		vCount := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			logger, pp, des, ad, nil, []ValidateIssueFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					ctx.CountMetadataKey("k1")
					ctx.CountMetadataKey("k1")

					return nil
				},
			}, nil,
		)
		err = vCount.VerifyIssue(ctx, anchor, &driver.TokenRequest{}, issue, nil, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "appeared more than one time")
	})

	t.Run("VerifyTransfer_MetadataError", func(t *testing.T) {
		transfer := &dmock.TransferAction{}
		transfer.GetMetadataReturns(map[string][]byte{"k1": nil})

		err := v.VerifyTransfer(ctx, anchor, &driver.TokenRequest{}, transfer, nil, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more metadata than those validated")

		vCount := NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
			logger, pp, des, ad, []ValidateTransferFunc[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]{
				func(c context.Context, ctx *Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]) error {
					ctx.CountMetadataKey("k1")
					ctx.CountMetadataKey("k1")

					return nil
				},
			}, nil, nil,
		)
		err = vCount.VerifyTransfer(ctx, anchor, &driver.TokenRequest{}, transfer, nil, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "appeared more than one time")
	})

	t.Run("VerifyTokenRequestFromRaw", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
			Signatures: []*driver.RequestSignature{
				{
					Auditor: &driver.AuditorSignature{
						Identity:  []byte("auditor"),
						Signature: []byte("sig"),
					},
				},
				{
					Action: &driver.ActionSignature{
						ActionID:  0,
						Signature: []byte("sig2"),
					},
				},
			},
		}
		raw, err := tr.Bytes()
		require.NoError(t, err)

		ad.DeserializeActionsReturns([]driver.IssueAction{&dmock.IssueAction{}}, nil, nil)

		actions, attrs, err := v.VerifyTokenRequestFromRaw(ctx, nil, anchor, raw)
		require.NoError(t, err)
		assert.Len(t, actions, 1)
		assert.NotNil(t, attrs)
		assert.Contains(t, attrs, TokenRequestToSign)
		assert.Contains(t, attrs, TokenRequestSignatures)

		_, _, err = v.VerifyTokenRequestFromRaw(ctx, nil, anchor, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty token request")

		_, _, err = v.VerifyTokenRequestFromRaw(ctx, nil, anchor, []byte("invalid"))
		require.Error(t, err)
	})
}

func TestIsAnyNil(t *testing.T) {
	var a *int
	b := 1
	assert.True(t, IsAnyNil(a))
	assert.False(t, IsAnyNil(&b))
	assert.True(t, IsAnyNil(&b, a))
}
