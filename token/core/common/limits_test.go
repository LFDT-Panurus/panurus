/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/stretchr/testify/require"
)

func TestCheckRawRequestSize(t *testing.T) {
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, CheckRawRequestSize(make([]byte, MaxRequestBytes-1)))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, CheckRawRequestSize(make([]byte, MaxRequestBytes)))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, CheckRawRequestSize(make([]byte, MaxRequestBytes+1)), ErrRequestTooLarge)
	})
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
	require.ErrorIs(t, CheckRequestLimits(nil), ErrNilTokenRequest)
}

func TestCheckRequestLimits_ActionCount(t *testing.T) {
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(MaxActions - 1)}))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(MaxActions)}))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(MaxActions + 1)}), ErrTooManyActions)
	})
}

func TestCheckRequestLimits_SignatureCount(t *testing.T) {
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(&driver.TokenRequest{Signatures: signaturesOfLen(MaxSignatures - 1)}))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(&driver.TokenRequest{Signatures: signaturesOfLen(MaxSignatures)}))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, CheckRequestLimits(&driver.TokenRequest{Signatures: signaturesOfLen(MaxSignatures + 1)}), ErrTooManySignatures)
	})
}

func TestCheckRequestLimits_ActionBytes(t *testing.T) {
	mk := func(n int) *driver.TokenRequest {
		return &driver.TokenRequest{Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: make([]byte, n)},
		}}
	}
	t.Run("at limit-1", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(mk(MaxActionBytes-1)))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(mk(MaxActionBytes)))
	})
	t.Run("at limit+1", func(t *testing.T) {
		require.ErrorIs(t, CheckRequestLimits(mk(MaxActionBytes+1)), ErrActionTooLarge)
	})
}

func TestCheckRequestLimits_SignatureBytes(t *testing.T) {
	mkAction := func() *driver.TokenRequest {
		return &driver.TokenRequest{}
	}
	mk := func(n int) *driver.TokenRequest {
		tr := mkAction()
		tr.Signatures = []*driver.RequestSignature{{Action: &driver.ActionSignature{ActionID: 0, Signature: make([]byte, n)}}}

		return tr
	}
	t.Run("action signature at limit-1", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(mk(MaxSignatureBytes-1)))
	})
	t.Run("action signature at limit", func(t *testing.T) {
		require.NoError(t, CheckRequestLimits(mk(MaxSignatureBytes)))
	})
	t.Run("action signature at limit+1", func(t *testing.T) {
		require.ErrorIs(t, CheckRequestLimits(mk(MaxSignatureBytes+1)), ErrSignatureTooLarge)
	})
	t.Run("auditor signature at limit+1", func(t *testing.T) {
		tr := mkAction()
		tr.Signatures = []*driver.RequestSignature{{Auditor: &driver.AuditorSignature{Signature: make([]byte, MaxSignatureBytes+1)}}}
		require.ErrorIs(t, CheckRequestLimits(tr), ErrSignatureTooLarge)
	})
}

func TestCheckRequestLimits_ManyTinyActions(t *testing.T) {
	// Many small actions, each individually within limits, but exceeding the count limit overall.
	require.ErrorIs(t, CheckRequestLimits(&driver.TokenRequest{Actions: actionsOfLen(MaxActions + 1)}), ErrTooManyActions)
}

func TestCheckRequestLimits_NilEntriesSkipped(t *testing.T) {
	require.NoError(t, CheckRequestLimits(&driver.TokenRequest{
		Actions:    []*driver.TypedAction{nil},
		Signatures: []*driver.RequestSignature{nil},
	}))
}
