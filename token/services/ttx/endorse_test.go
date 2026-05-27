/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/tokenapi"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestEndorseViewContextInput struct {
	IssuerIdentity token.Identity
	// CancelledContext replaces the test context with one that is already cancelled,
	// so that any blocking session.Receive call returns ErrContextDone immediately
	// without having to wait for the hardcoded per-phase deadline (e.g. 1 minute).
	CancelledContext bool
	// SigRequestOnly puts the signature-request message in the channel but not the
	// endorsed-transaction message.  Used to simulate the case where the initiator
	// goes silent after the responder has already signed.
	SigRequestOnly bool
}

type TestEndorseViewContext struct {
	ctx             *mock2.Context
	tx              *ttx.Transaction
	options         []ttx.TxOption
	storageProvider *mock2.StorageProvider
	storage         *mock2.Storage
	session         *mock2.Session
}

func newTestEndorseViewContext(t *testing.T, input *TestEndorseViewContextInput) *TestEndorseViewContext {
	t.Helper()
	if input == nil {
		input = &TestEndorseViewContextInput{}
	}
	if len(input.IssuerIdentity) == 0 {
		input.IssuerIdentity = []byte("an_issuer")
	}

	session := &mock2.Session{}
	ch := make(chan *view.Message, 2)
	session.ReceiveReturns(ch)

	tms := &mock2.TokenManagementServiceWithExtensions{}
	tms.NetworkReturns("a_network")
	tms.ChannelReturns("a_channel")
	tmsID := token.TMSID{
		Network:   "a_network",
		Channel:   "a_channel",
		Namespace: "a_namespace",
	}
	tms.IDReturns(tmsID)
	tokenDes := &mock.Deserializer{}
	tokenIP := &mock.IdentityProvider{}
	tokenIP.IsMeReturns(true)
	tokenSigner := &mock.Signer{}
	tokenSigner.SignReturns([]byte("a_token_sigma"), nil)
	tokenIP.GetSignerReturns(tokenSigner, nil)
	tms.SigServiceReturns(token.NewSignatureService(tokenDes, tokenIP))
	tokenAPITMS := tokenapi.NewMockedManagementService(t, tmsID)
	tms.SetTokenManagementServiceStub = func(arg1 *token.Request) error {
		arg1.SetTokenService(tokenAPITMS)

		return nil
	}
	tmsp := &mock2.TokenManagementServiceProvider{}
	tmsp.TokenManagementServiceReturns(tms, nil)
	network := &mock2.Network{}
	network.ComputeTxIDReturns("an_anchor")
	np := &mock2.NetworkProvider{}
	np.GetNetworkReturns(network, nil)

	req := token.NewRequest(nil, "an_anchor")
	req.Metadata.Actions = []*driver.ActionMetadataEntry{
		{
			ActionID: 0,
			IssueMetadata: &driver.IssueMetadata{
				Issuer: driver.AuditableIdentity{
					Identity: []byte("an_issuer"),
				},
			},
		},
	}
	tms.NewRequestReturns(req, nil)

	storage := &mock2.Storage{}
	storage.AppendReturns(nil)
	storageProvider := &mock2.StorageProvider{}
	storageProvider.GetStorageReturns(storage, nil)

	networkIdentityProvider := &mock2.NetworkIdentityProvider{}
	nis := &mock2.NetworkIdentitySigner{}
	nis.SignReturns([]byte("an_ack_signature"), nil)
	networkIdentityProvider.GetSignerReturns(nis, nil)

	// Resolve services by their requested type rather than by call order, so the
	// stub is robust to additional GetService lookups (e.g. envelope metrics).
	getService := func(v any) (any, error) {
		rt, ok := v.(reflect.Type)
		if !ok {
			return nil, errors.Errorf("unexpected service request [%T]", v)
		}
		switch rt.String() {
		case "*dep.TokenManagementServiceProvider":
			return tmsp, nil
		case "*dep.NetworkProvider":
			return np, nil
		case "*dep.NetworkIdentityProvider":
			return networkIdentityProvider, nil
		case "*endpoint.Service":
			return &endpoint.Service{}, nil
		case "*ttx.StorageProvider":
			return storageProvider, nil
		case "*session.EnvelopeMetrics":
			return nil, errors.New("envelope metrics not registered in test")
		default:
			return nil, errors.Errorf("unexpected service request [%s]", rt.String())
		}
	}

	baseCtx := t.Context()
	if input.CancelledContext {
		var cancel context.CancelFunc
		baseCtx, cancel = context.WithCancel(baseCtx)
		cancel() // immediately cancelled
	}

	ctx := &mock2.Context{}
	ctx.SessionReturns(session)
	ctx.ContextReturns(baseCtx)
	ctx.GetServiceStub = getService
	tx, err := ttx.NewTransaction(ctx, []byte("a_signer"))
	require.NoError(t, err)

	ctx = &mock2.Context{}
	ctx.SessionReturns(session)
	ctx.ContextReturns(baseCtx)
	ctx.GetServiceStub = getService

	txRaw, err := tx.Bytes()
	require.NoError(t, err)

	// Whether to queue messages in the channel depends on the scenario being tested:
	//
	//  CancelledContext=true,  SigRequestOnly=false:
	//    → no messages queued; both phase-1 and phase-2 receives unblock immediately
	//      via ctx.Done() (tests phase-1 timeout / context-cancellation).
	//
	//  CancelledContext=true,  SigRequestOnly=true:
	//    → only the sig-request is queued so phase-1 reads from the buffer without
	//      blocking; phase-2 finds an empty channel + done context
	//      (tests phase-2 timeout / context-cancellation).
	//
	//  CancelledContext=false, SigRequestOnly=false  (default "success" path):
	//    → both messages queued.
	//
	//  CancelledContext=false, SigRequestOnly=true:
	//    → only sig-request queued; phase-2 blocks until timer fires (not useful in
	//      unit tests, reserved for live testing).
	if !input.CancelledContext || input.SigRequestOnly {
		signatureRequest := &ttx.SignatureRequest{
			Signer: input.IssuerIdentity,
		}
		ch <- &view.Message{
			Payload: mustEnvelopeBytes(t, ttx.TypeSignatureRequest, signatureRequest),
		}
	}
	if !input.CancelledContext && !input.SigRequestOnly {
		// then the transaction
		ch <- &view.Message{
			Payload: mustEnvelopeBytes(t, ttx.TypeTransaction, &ttx.TransactionPayload{Raw: txRaw}),
		}
	}

	ctx.RunViewStub = func(v view.View, option ...view.RunViewOption) (any, error) {
		return v.Call(ctx)
	}

	c := &TestEndorseViewContext{
		ctx:             ctx,
		tx:              tx,
		storage:         storage,
		storageProvider: storageProvider,
		session:         session,
	}

	return c
}

func TestEndorseView(t *testing.T) {
	testCases := []struct {
		name          string
		prepare       func() *TestEndorseViewContext
		expectError   bool
		errorContains string
		expectErr     error
		verify        func(*TestEndorseViewContext, any)
	}{
		{
			name: "transaction is nil",
			prepare: func() *TestEndorseViewContext {
				c := newTestEndorseViewContext(t, nil)
				c.tx = nil
				c.options = nil

				return c
			},
			expectError:   true,
			errorContains: "transaction is nil",
			expectErr:     ttx.ErrInvalidInput,
			verify: func(ctx *TestEndorseViewContext, _ any) {
				assert.Equal(t, 0, ctx.session.SendWithContextCallCount())
			},
		},
		{
			name: "success",
			prepare: func() *TestEndorseViewContext {
				c := newTestEndorseViewContext(t, nil)

				return c
			},
			expectError: false,
			verify: func(ctx *TestEndorseViewContext, _ any) {
				assert.Equal(t, 2, ctx.session.SendWithContextCallCount())
				_, msg := ctx.session.SendWithContextArgsForCall(0)
				var signaturePayload ttx.SignaturePayload
				require.NoError(t, json.Unmarshal(mustUnwrapBody(t, msg, ttx.TypeSignature), &signaturePayload))
				assert.Equal(t, []byte("a_token_sigma"), signaturePayload.Signature)
				_, msg = ctx.session.SendWithContextArgsForCall(1)
				require.NoError(t, json.Unmarshal(mustUnwrapBody(t, msg, ttx.TypeSignature), &signaturePayload))
				assert.Equal(t, []byte("an_ack_signature"), signaturePayload.Signature)
			},
		},
		{
			name: "failed storage append",
			prepare: func() *TestEndorseViewContext {
				c := newTestEndorseViewContext(t, nil)
				c.storage.AppendReturns(errors.Errorf("pineapple"))

				return c
			},
			expectError:   true,
			errorContains: "pineapple",
			expectErr:     ttx.ErrStorage,
			verify: func(ctx *TestEndorseViewContext, _ any) {
				assert.Equal(t, 0, ctx.session.SendWithContextCallCount())
			},
		},
		{
			name: "wrong identity in signature request",
			prepare: func() *TestEndorseViewContext {
				c := newTestEndorseViewContext(t, &TestEndorseViewContextInput{
					IssuerIdentity: []byte("another_issuer"),
				})

				return c
			},
			expectError:   true,
			errorContains: "signature request's signer does not match the expected signer",
			expectErr:     ttx.ErrSignerIdentityMismatch,
			verify: func(ctx *TestEndorseViewContext, _ any) {
				assert.Equal(t, 0, ctx.session.SendWithContextCallCount())
			},
		},
		{
			name: "received transaction is different",
			prepare: func() *TestEndorseViewContext {
				c := newTestEndorseViewContext(t, &TestEndorseViewContextInput{
					IssuerIdentity: []byte("another_issuer"),
				})

				return c
			},
			expectError:   true,
			errorContains: "signature request's signer does not match the expected signer",
			expectErr:     ttx.ErrSignerIdentityMismatch,
			verify: func(ctx *TestEndorseViewContext, _ any) {
				assert.Equal(t, 0, ctx.session.SendWithContextCallCount())
			},
		},
		// --- Timeout / context-cancellation tests ---
		//
		// Each of the two blocking receive points inside EndorseView is guarded by
		// a session.S select that also watches ctx.Done().  By passing an already-
		// cancelled context the tests run instantly instead of waiting the hardcoded
		// 1-minute / 4-minute per-phase deadlines.
		{
			// Phase 1: waiting for the signature request from the initiator.
			// The channel is empty and the context is cancelled → ErrContextDone.
			name: "context cancelled while waiting for signature request",
			prepare: func() *TestEndorseViewContext {
				c := newTestEndorseViewContext(t, &TestEndorseViewContextInput{
					CancelledContext: true,
				})

				return c
			},
			expectError: true,
			// The error is wrapped as "failed reading signature request: ctx done …"
			// and joined with ErrHandlingSignatureRequests.
			expectErr: ttx.ErrHandlingSignatureRequests,
			verify: func(ctx *TestEndorseViewContext, _ any) {
				// No signature was sent back because the receive failed before signing.
				assert.Equal(t, 0, ctx.session.SendWithContextCallCount())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testCtx := tc.prepare()
			v := ttx.NewEndorseView(testCtx.tx)
			txBoxed, err := v.Call(testCtx.ctx)
			if tc.expectError {
				require.Error(t, err)
				if len(tc.errorContains) != 0 {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
				if tc.expectErr != nil {
					require.ErrorIs(t, err, tc.expectErr)
				}
			} else {
				require.NoError(t, err)
				if tc.verify != nil {
					tc.verify(testCtx, txBoxed)
				}
			}
		})
	}
}

// TestEndorseView_EndorsedTxContextCancellation verifies phase 2 of EndorseView:
// after the responder has signed and sent back the signature, the initiator goes
// silent (never distributes the endorsed transaction).  A context that is
// cancelled while the view is blocking on the second receive must unblock the
// view and return an error.
//
// Implementation note: EndorseView.receiveTransaction calls ReceiveTransaction
// which ultimately calls session.S.ReceiveRawWithTimeout.  That helper's select
// simultaneously watches the message channel and ctx.Done(), so cancelling the
// context is sufficient to surface the error without waiting for the 4-minute
// hardcoded deadline.
//
// We cancel the context from a separate goroutine, after the sig-request
// message has been consumed from the buffered channel, to avoid the non-
// deterministic select race that would arise if ctx.Done() were already closed
// when the first receive executes.
func TestEndorseView_EndorsedTxContextCancellation(t *testing.T) {
	// Build the context with a live (non-cancelled) base context; SigRequestOnly
	// ensures only the signature-request message is placed in the channel.
	testCtx := newTestEndorseViewContext(t, &TestEndorseViewContextInput{
		SigRequestOnly: true,
	})

	// Replace the view context's context.Context() with one we can cancel.
	cancelCtx, cancel := context.WithCancel(t.Context())

	// Cancel after the first send (the token signature) has been recorded on the
	// mock session.  We watch the call count from a goroutine and cancel as soon
	// as phase 1 completes; phase 2 will then find a done context.
	go func() {
		for testCtx.session.SendWithContextCallCount() == 0 {
			// busy-spin with a yield so the main goroutine can progress
			// (in practice this exits after microseconds)
		}
		cancel()
	}()

	testCtx.ctx.ContextReturns(cancelCtx)

	v := ttx.NewEndorseView(testCtx.tx)
	_, err := v.Call(testCtx.ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed receiving transaction",
		"error should indicate failure during endorsed-tx receive phase")
	// Phase 1 signature was sent before the context was cancelled.
	assert.GreaterOrEqual(t, testCtx.session.SendWithContextCallCount(), 1,
		"token signature must have been sent before context cancellation")
}
