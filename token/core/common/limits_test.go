/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/require"
)

func newTestValidator(limits driver.ResourceLimits) *Validator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer] {
	return NewValidator[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer](
		&logging.MockLogger{}, nil, nil, limits, &dmock.ActionDeserializer[driver.TransferAction, driver.IssueAction]{}, nil, nil, nil,
	)
}

func TestCheckRawRequestSize(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	v := newTestValidator(limits)
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, v.CheckRawRequestSize(make([]byte, limits.MaxRequestBytes-1)))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, v.CheckRawRequestSize(make([]byte, limits.MaxRequestBytes)))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, v.CheckRawRequestSize(make([]byte, limits.MaxRequestBytes+1)), ErrRequestTooLarge)
	})
}

func TestCheckRawRequestSize_CustomLimit(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	limits.MaxRequestBytes = 16
	v := newTestValidator(limits)
	require.NoError(t, v.CheckRawRequestSize(make([]byte, 16)))
	require.ErrorIs(t, v.CheckRawRequestSize(make([]byte, 17)), ErrRequestTooLarge)
}

func actionsOfLen(n int) []*driver.TypedAction {
	actions := make([]*driver.TypedAction, n)
	for i := range actions {
		actions[i] = &driver.TypedAction{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("a")}
	}

	return actions
}

func signaturesOfLen(n int) []*driver.RequestSignature {
	sigs := make([]*driver.RequestSignature, n)
	for i := range sigs {
		sigs[i] = &driver.RequestSignature{Action: &driver.ActionSignature{ActionID: 0, Signature: []byte("s")}}
	}

	return sigs
}

func TestCheckRequestLimits_NilRequest(t *testing.T) {
	v := newTestValidator(driver.DefaultResourceLimits())
	require.ErrorIs(t, v.CheckRequestLimits(nil), ErrNilTokenRequest)
}

func TestCheckRequestLimits_ActionCount(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	v := newTestValidator(limits)
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(limits.MaxActions - 1)}))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(limits.MaxActions)}))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, v.CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(limits.MaxActions + 1)}), ErrTooManyActions)
	})
}

func TestCheckRequestLimits_ActionCount_CustomLimit(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	limits.MaxActions = 2
	v := newTestValidator(limits)
	require.NoError(t, v.CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(2)}))
	require.ErrorIs(t, v.CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(3)}), ErrTooManyActions)
}

func TestCheckRequestLimits_SignatureCount(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	v := newTestValidator(limits)
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(&driver.TokenRequest{Signatures: signaturesOfLen(limits.MaxSignatures - 1)}))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(&driver.TokenRequest{Signatures: signaturesOfLen(limits.MaxSignatures)}))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, v.CheckRequestLimits(&driver.TokenRequest{Signatures: signaturesOfLen(limits.MaxSignatures + 1)}), ErrTooManySignatures)
	})
}

func TestCheckRequestLimits_ActionBytes(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	v := newTestValidator(limits)
	mk := func(n int) *driver.TokenRequest {
		return &driver.TokenRequest{Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, n)},
		}}
	}
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(mk(limits.MaxActionBytes-1)))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(mk(limits.MaxActionBytes)))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, v.CheckRequestLimits(mk(limits.MaxActionBytes+1)), ErrActionTooLarge)
	})
}

func TestCheckRequestLimits_SignatureBytes(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	v := newTestValidator(limits)
	mkAction := func() *driver.TokenRequest {
		return &driver.TokenRequest{}
	}
	mk := func(n int) *driver.TokenRequest {
		tr := mkAction()
		tr.Signatures = []*driver.RequestSignature{{Action: &driver.ActionSignature{ActionID: 0, Signature: make([]byte, n)}}}

		return tr
	}
	t.Run("action signature at limit-1", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(mk(limits.MaxSignatureBytes-1)))
	})
	t.Run("action signature at limit", func(t *testing.T) {
		require.NoError(t, v.CheckRequestLimits(mk(limits.MaxSignatureBytes)))
	})
	t.Run("action signature at limit+1", func(t *testing.T) {
		require.ErrorIs(t, v.CheckRequestLimits(mk(limits.MaxSignatureBytes+1)), ErrSignatureTooLarge)
	})
	t.Run("auditor signature at limit+1", func(t *testing.T) {
		tr := mkAction()
		tr.Signatures = []*driver.RequestSignature{{Auditor: &driver.AuditorSignature{Signature: make([]byte, limits.MaxSignatureBytes+1)}}}
		require.ErrorIs(t, v.CheckRequestLimits(tr), ErrSignatureTooLarge)
	})
}

func TestCheckRequestLimits_ManyTinyActions(t *testing.T) {
	limits := driver.DefaultResourceLimits()
	v := newTestValidator(limits)
	// Many small actions, each individually within limits, but exceeding the count limit overall.
	require.ErrorIs(t, v.CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(limits.MaxActions + 1)}), ErrTooManyActions)
}

func TestCheckRequestLimits_NilEntriesSkipped(t *testing.T) {
	v := newTestValidator(driver.DefaultResourceLimits())
	require.NoError(t, v.CheckRequestLimits(&driver.TokenRequest{
		Actions:    []*driver.TypedAction{nil},
		Signatures: []*driver.RequestSignature{nil},
	}))
}
