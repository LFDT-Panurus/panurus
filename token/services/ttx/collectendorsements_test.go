/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	sessionmock "github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCleanupExternalWallets_Success tests that CleanupExternalWallets calls Done() on all wallets
func TestCleanupExternalWallets_Success(t *testing.T) {
	wallet := &mock.ExternalWalletSigner{}

	externalWallets := map[string]ttx.ExternalWalletSigner{
		"wallet1": wallet,
	}

	// Create a minimal view and context
	view := &ttx.CollectEndorsementsView{}
	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())

	// Call the cleanup method
	view.CleanupExternalWallets(ctx, externalWallets)

	assert.Equal(t, 1, wallet.DoneCallCount(), "Done() should have been called once on the wallet")
}

// TestCleanupExternalWallets_MultipleWallets tests that Done() is called on all wallets
func TestCleanupExternalWallets_MultipleWallets(t *testing.T) {
	wallet1 := &mock.ExternalWalletSigner{}
	wallet2 := &mock.ExternalWalletSigner{}
	wallet3 := &mock.ExternalWalletSigner{}

	externalWallets := map[string]ttx.ExternalWalletSigner{
		"wallet1": wallet1,
		"wallet2": wallet2,
		"wallet3": wallet3,
	}

	view := &ttx.CollectEndorsementsView{}
	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())

	view.CleanupExternalWallets(ctx, externalWallets)

	assert.Equal(t, 1, wallet1.DoneCallCount(), "Done() should have been called once on wallet1")
	assert.Equal(t, 1, wallet2.DoneCallCount(), "Done() should have been called once on wallet2")
	assert.Equal(t, 1, wallet3.DoneCallCount(), "Done() should have been called once on wallet3")
}

// TestCleanupExternalWallets_MultipleWalletsDoneError tests that errors from Done() don't stop cleanup of multiple wallets
func TestCleanupExternalWallets_MultipleWalletsDoneError(t *testing.T) {
	wallet1 := &mock.ExternalWalletSigner{}
	wallet1.DoneReturns(errors.New("wallet1 done failed"))

	wallet2 := &mock.ExternalWalletSigner{}

	wallet3 := &mock.ExternalWalletSigner{}
	wallet3.DoneReturns(errors.New("wallet3 done failed"))

	externalWallets := map[string]ttx.ExternalWalletSigner{
		"wallet1": wallet1,
		"wallet2": wallet2,
		"wallet3": wallet3,
	}

	view := &ttx.CollectEndorsementsView{}
	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())

	// Should not panic even if Done() returns errors
	view.CleanupExternalWallets(ctx, externalWallets)

	assert.Equal(t, 1, wallet1.DoneCallCount(), "Done() should have been called once on wallet1 despite error")
	assert.Equal(t, 1, wallet2.DoneCallCount(), "Done() should have been called once on wallet2")
	assert.Equal(t, 1, wallet3.DoneCallCount(), "Done() should have been called once on wallet3 despite error")
}

// TestCleanupExternalWallets_EmptyMap tests cleanup with no wallets
func TestCleanupExternalWallets_EmptyMap(t *testing.T) {
	externalWallets := map[string]ttx.ExternalWalletSigner{}

	view := &ttx.CollectEndorsementsView{}
	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())

	// Should not panic with empty map
	view.CleanupExternalWallets(ctx, externalWallets)
}

// ---------------------------------------------------------------------------
// Timeout / context-cancellation tests for the endorsement-flow receive calls
// ---------------------------------------------------------------------------
//
// CollectEndorsementsView.signRemote and CollectEndorsementsView.distributeTxToParty
// both call TypedSession.ReceiveTypedWithTimeout, which ultimately delegates to
// session.S.ReceiveRawWithTimeout.  The select inside that helper simultaneously
// watches the channel for a message, a per-phase timer, and ctx.Done().
//
// The tests below exercise the contract at that boundary using the same message
// types used by the actual implementation (TypeSignature for signatures /
// acknowledgements, TypeTransaction for the endorsed transaction).

// TestRemoteSignerTimeoutViaCancelledContext verifies that when the remote signer
// never responds, a cancelled context causes the receive to return immediately
// with ErrContextDone.  This mirrors the signRemote call site in
// CollectEndorsementsView.  A 1-ms explicit timeout is also tested to confirm
// the timer path.
func TestRemoteSignerTimeoutViaCancelledContext(t *testing.T) {
	t.Run("context cancelled", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – no message will arrive
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "remote-signer-session"})

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		s := utilsession.New(ms, cancelledCtx, jsession.JSONMarshaller{})

		var sig ttx.SignaturePayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeSignature, &sig, time.Minute)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrContextDone,
			"cancelled context must unblock the receive immediately with ErrContextDone")
	})

	t.Run("per-phase timer fires", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – no message will arrive
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "remote-signer-session"})

		s := utilsession.New(ms, context.Background(), jsession.JSONMarshaller{})

		var sig ttx.SignaturePayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeSignature, &sig, 1*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrTimeout,
			"expired per-phase timer must unblock the receive with ErrTimeout")
	})
}

// TestDistributionAckTimeoutViaCancelledContext verifies that when the receiving
// party never sends the acknowledgement (TypeSignature) after the endorsed
// transaction is distributed, a cancelled context unblocks immediately.
// This mirrors the distributeTxToParty call site in CollectEndorsementsView.
func TestDistributionAckTimeoutViaCancelledContext(t *testing.T) {
	t.Run("context cancelled", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – no ack will arrive
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "distribution-ack-session"})

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		s := utilsession.New(ms, cancelledCtx, jsession.JSONMarshaller{})

		var ack ttx.SignaturePayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeSignature, &ack, time.Minute)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrContextDone,
			"cancelled context must unblock distribution-ack receive immediately")
	})

	t.Run("per-phase timer fires", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – no ack will arrive
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "distribution-ack-session"})

		s := utilsession.New(ms, context.Background(), jsession.JSONMarshaller{})

		var ack ttx.SignaturePayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeSignature, &ack, 1*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrTimeout,
			"expired per-phase timer must unblock distribution-ack receive with ErrTimeout")
	})
}

// TestEndorsedTxDistributionTimeoutViaCancelledContext verifies that when the
// endorsed transaction is never distributed (e.g., CollectEndorsementsView
// delays or dies), a cancelled context surfaces immediately.  This mirrors the
// ReceiveTransactionView / ReceiveRawWithTimeout call path.
func TestEndorsedTxDistributionTimeoutViaCancelledContext(t *testing.T) {
	t.Run("context cancelled", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – no tx will arrive
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "endorsed-tx-session"})

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		s := utilsession.New(ms, cancelledCtx, jsession.JSONMarshaller{})

		var tx ttx.TransactionPayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeTransaction, &tx, time.Minute)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrContextDone,
			"cancelled context must unblock endorsed-tx receive immediately")
	})

	t.Run("per-phase timer fires", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – no tx will arrive
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "endorsed-tx-session"})

		s := utilsession.New(ms, context.Background(), jsession.JSONMarshaller{})

		var tx ttx.TransactionPayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeTransaction, &tx, 1*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrTimeout,
			"expired per-phase timer must unblock endorsed-tx receive with ErrTimeout")
	})
}

// TestAuditorSignatureTimeoutViaCancelledContext verifies that when the auditor
// never replies with its signature, a cancelled context unblocks immediately.
// The auditing initiator view in auditingViewInitiator uses the same
// ReceiveTypedWithTimeout(TypeSignature, …) pattern.
func TestAuditorSignatureTimeoutViaCancelledContext(t *testing.T) {
	t.Run("context cancelled", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – auditor never responds
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "auditor-session"})

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		s := utilsession.New(ms, cancelledCtx, jsession.JSONMarshaller{})

		var sig ttx.SignaturePayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeSignature, &sig, time.Minute)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrContextDone,
			"cancelled context must unblock auditor-signature receive immediately")
	})

	t.Run("per-phase timer fires", func(t *testing.T) {
		ms := &sessionmock.Session{}
		ch := make(chan *view.Message) // unbuffered – auditor never responds
		ms.ReceiveReturns(ch)
		ms.InfoReturns(view.SessionInfo{ID: "auditor-session"})

		s := utilsession.New(ms, context.Background(), jsession.JSONMarshaller{})

		var sig ttx.SignaturePayload
		err := jsession.ReceiveTypedWithTimeout(s, ttx.TypeSignature, &sig, 1*time.Millisecond)
		require.Error(t, err)
		assert.ErrorIs(t, err, utilsession.ErrTimeout,
			"expired per-phase timer must unblock auditor-signature receive with ErrTimeout")
	})
}
