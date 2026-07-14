/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReceiveTx(t *testing.T) {
	testCases := []struct {
		name          string
		prepare       func() (view.Context, []ttx.TxOption)
		expectError   bool
		errorContains string
		expectErr     error
		verify        func(tx *ttx.Transaction)
	}{
		{
			name: "received empty message, session closed",
			prepare: func() (view.Context, []ttx.TxOption) {
				session := &mock2.Session{}
				ch := make(chan *view.Message, 1)
				ch <- &view.Message{}
				session.ReceiveReturns(ch)
				ctx := &mock2.Context{}
				ctx.SessionReturns(session)
				ctx.ContextReturns(t.Context())

				return ctx, nil
			},
			expectError:   true,
			errorContains: "received empty message, session closed",
		},
		{
			name: "received empty message, session closed",
			prepare: func() (view.Context, []ttx.TxOption) {
				session := &mock2.Session{}
				ch := make(chan *view.Message, 1)
				session.ReceiveReturns(ch)
				ctx := &mock2.Context{}
				ctx.SessionReturns(session)
				ctx.ContextReturns(t.Context())

				return ctx, []ttx.TxOption{ttx.WithTimeout(1 * time.Second)}
			},
			expectError: true,
			expectErr:   ttx.ErrTimeout,
		},
		{
			name: "invalid tx payload",
			prepare: func() (view.Context, []ttx.TxOption) {
				session := &mock2.Session{}
				ch := make(chan *view.Message, 1)
				ch <- &view.Message{
					Payload: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
				}
				session.ReceiveReturns(ch)
				ctx := &mock2.Context{}
				ctx.SessionReturns(session)
				ctx.ContextReturns(t.Context())
				np := &mock2.NetworkProvider{}
				ctx.GetServiceReturnsOnCall(0, np, nil)

				return ctx, []ttx.TxOption{ttx.WithTimeout(1 * time.Second)}
			},
			expectError: true,
			expectErr:   jsession.ErrInvalidEnvelope,
		},
		{
			name: "valid tx payload",
			prepare: func() (view.Context, []ttx.TxOption) {
				// create a new transaction

				session := &mock2.Session{}
				ch := make(chan *view.Message, 1)
				session.ReceiveReturns(ch)
				ctx := &mock2.Context{}
				ctx.SessionReturns(session)
				ctx.ContextReturns(t.Context())
				tms := &mock2.TokenManagementServiceWithExtensions{}
				tms.NetworkReturns("a_network")
				tms.ChannelReturns("a_channel")
				tms.IDReturns(token.TMSID{
					Network:   "a_network",
					Channel:   "a_channel",
					Namespace: "a_namespace",
				})
				req := token.NewRequest(nil, "an_anchor")
				tms.NewRequestReturns(req, nil)
				tmsp := &mock2.TokenManagementServiceProvider{}
				tmsp.TokenManagementServiceReturns(tms, nil)
				ctx.GetServiceReturnsOnCall(0, tmsp, nil)

				network := &mock2.Network{}
				network.ComputeTxIDReturns("an_anchor")
				np := &mock2.NetworkProvider{}
				np.GetNetworkReturns(network, nil)
				ctx.GetServiceReturnsOnCall(1, np, nil)
				ctx.GetServiceReturnsOnCall(2, &endpoint.Service{}, nil)
				ctx.GetServiceReturnsOnCall(3, np, nil)
				ctx.GetServiceReturnsOnCall(4, tmsp, nil)

				tx, err := ttx.NewTransaction(ctx, []byte("a_signer"))
				require.NoError(t, err)
				txRaw, err := tx.Bytes()
				require.NoError(t, err)

				ch <- &view.Message{
					Payload: mustEnvelopeBytes(t, ttx.TypeTransaction, &ttx.TransactionPayload{Raw: txRaw}),
				}

				return ctx, []ttx.TxOption{ttx.WithTimeout(1 * time.Second)}
			},
			expectError: false,
			verify: func(tx *ttx.Transaction) {
				assert.Equal(t, "a_network", tx.Network())
			},
		},
		{
			name: "valid signature request payload",
			prepare: func() (view.Context, []ttx.TxOption) {
				// create a new transaction
				session := &mock2.Session{}
				ch := make(chan *view.Message, 1)
				session.ReceiveReturns(ch)
				ctx := &mock2.Context{}
				ctx.SessionReturns(session)
				ctx.ContextReturns(t.Context())
				tms := &mock2.TokenManagementServiceWithExtensions{}
				tms.NetworkReturns("a_network")
				tms.ChannelReturns("a_channel")
				tms.IDReturns(token.TMSID{
					Network:   "a_network",
					Channel:   "a_channel",
					Namespace: "a_namespace",
				})
				req := token.NewRequest(nil, "an_anchor")
				tms.NewRequestReturns(req, nil)
				tmsp := &mock2.TokenManagementServiceProvider{}
				tmsp.TokenManagementServiceReturns(tms, nil)
				network := &mock2.Network{}
				network.ComputeTxIDReturns("an_anchor")
				np := &mock2.NetworkProvider{}
				np.GetNetworkReturns(network, nil)
				ctx.GetServiceReturnsOnCall(0, tmsp, nil)
				ctx.GetServiceReturnsOnCall(1, np, nil)
				ctx.GetServiceReturnsOnCall(2, &endpoint.Service{}, nil)
				ctx.GetServiceReturnsOnCall(3, np, nil)
				ctx.GetServiceReturnsOnCall(4, tmsp, nil)

				tx, err := ttx.NewTransaction(ctx, []byte("a_signer"))
				require.NoError(t, err)
				txRaw, err := tx.Bytes()
				require.NoError(t, err)

				sr := &ttx.SignatureRequest{
					TX:     txRaw,
					Signer: view.Identity("a_signer"),
				}
				ch <- &view.Message{
					Payload: mustEnvelopeBytes(t, ttx.TypeSignatureRequest, sr),
				}

				return ctx, []ttx.TxOption{ttx.WithTimeout(1 * time.Second)}
			},
			expectError: false,
			verify: func(tx *ttx.Transaction) {
				assert.Equal(t, "a_network", tx.Network())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, opts := tc.prepare()
			v := ttx.NewReceiveTransactionView(opts...)
			txBoxed, err := v.Call(ctx)
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
				tc.verify(txBoxed.(*ttx.Transaction))
			}
		})
	}
}
