/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testFinalityViewContext struct {
	ctx                   *mock.Context
	transactionDBProvider *mock.TransactionDBProvider
	transactionDB         *mock.TransactionDB
	auditDBProvider       *mock.AuditDBProvider
	auditDB               *mock.AuditDB
}

func newTestFinalityViewContext(t *testing.T) *testFinalityViewContext {
	t.Helper()

	ctx := &mock.Context{}
	ctx.ContextReturns(t.Context())

	transactionDB := &mock.TransactionDB{}
	transactionDBProvider := &mock.TransactionDBProvider{}
	transactionDBProvider.TransactionDBReturns(transactionDB, nil)

	auditDB := &mock.AuditDB{}
	auditDBProvider := &mock.AuditDBProvider{}
	auditDBProvider.AuditDBReturns(auditDB, nil)

	ctx.GetServiceReturnsOnCall(0, transactionDBProvider, nil)
	ctx.GetServiceReturnsOnCall(1, auditDBProvider, nil)

	return &testFinalityViewContext{
		ctx:                   ctx,
		transactionDBProvider: transactionDBProvider,
		transactionDB:         transactionDB,
		auditDBProvider:       auditDBProvider,
		auditDB:               auditDB,
	}
}

func TestFinalityView(t *testing.T) {
	testCases := []struct {
		name          string
		prepare       func(*testFinalityViewContext)
		opts          []ttx.TxOption
		expectError   bool
		errorContains string
		expectErr     error
	}{
		{
			name: "transaction unknown",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Unknown, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			expectError:   true,
			errorContains: "transaction [tx_id] is unknown for [network,channel,namespace]",
			expectErr:     ttx.ErrTransactionUnknown,
		},
		{
			name: "transaction confirmed in transaction db",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Confirmed, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			expectError: false,
		},
		{
			name: "transaction confirmed in audit db",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Unknown, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Confirmed, "", nil)
			},
			expectError: false,
		},
		{
			name: "transaction deleted in transaction db",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Deleted, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			expectError:   true,
			errorContains: "transaction [tx_id] is not valid",
			expectErr:     ttx.ErrFinalityInvalidTransaction,
		},
		{
			name: "transaction deleted in audit db",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Unknown, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Deleted, "", nil)
			},
			expectError:   true,
			errorContains: "transaction [tx_id] is not valid",
			expectErr:     ttx.ErrFinalityInvalidTransaction,
		},
		{
			name: "wait for event confirmed",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Pending, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
				c.transactionDB.AddStatusListenerStub = func(txID string, ch chan db.TransactionStatusEvent) {
					go func() {
						ch <- db.TransactionStatusEvent{
							TxID:           txID,
							ValidationCode: ttxdb.Confirmed,
							Ctx:            t.Context(),
						}
					}()
				}
			},
			expectError: false,
		},
		{
			name: "wait for event invalid",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Pending, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
				c.transactionDB.AddStatusListenerStub = func(txID string, ch chan db.TransactionStatusEvent) {
					go func() {
						ch <- db.TransactionStatusEvent{
							TxID:           txID,
							ValidationCode: ttxdb.Deleted,
							Ctx:            t.Context(),
						}
					}()
				}
			},
			expectError:   true,
			errorContains: "transaction [tx_id] is not valid",
			expectErr:     ttx.ErrFinalityInvalidTransaction,
		},
		{
			name: "timeout waiting for event",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Pending, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
				// No event sent
			},
			expectError:   true,
			errorContains: "failed to listen to transaction [tx_id] for timeout",
			expectErr:     ttx.ErrFinalityTimeout,
		},
		{
			name: "failed to compile options",
			opts: []ttx.TxOption{
				func(o *ttx.TxOptions) error {
					return errors.New("boom")
				},
			},
			expectError:   true,
			errorContains: "failed to compile options",
		},
		{
			name: "failed to get transaction db",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDBProvider.TransactionDBReturns(nil, errors.New("db error"))
			},
			expectError:   true,
			errorContains: "db error",
		},
		{
			name: "failed to get audit db",
			prepare: func(c *testFinalityViewContext) {
				c.auditDBProvider.AuditDBReturns(nil, errors.New("db error"))
			},
			expectError:   true,
			errorContains: "db error",
		},
		{
			name: "timeout tick then confirmed",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturnsOnCall(0, ttxdb.Pending, "", nil)   // call check
				c.transactionDB.GetStatusReturnsOnCall(1, ttxdb.Pending, "", nil)   // dbFinality initial check
				c.transactionDB.GetStatusReturnsOnCall(2, ttxdb.Confirmed, "", nil) // dbFinality timeout check
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			opts:        []ttx.TxOption{ttx.WithTimeout(1500 * time.Millisecond)},
			expectError: false,
		},
		{
			name: "timeout tick then deleted",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturnsOnCall(0, ttxdb.Pending, "", nil) // call check
				c.transactionDB.GetStatusReturnsOnCall(1, ttxdb.Pending, "", nil) // dbFinality initial check
				c.transactionDB.GetStatusReturnsOnCall(2, ttxdb.Deleted, "", nil) // dbFinality timeout check
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			opts:          []ttx.TxOption{ttx.WithTimeout(1500 * time.Millisecond)},
			expectError:   true,
			errorContains: "transaction [tx_id] is not valid",
			expectErr:     ttx.ErrFinalityInvalidTransaction,
		},
		{
			name: "timeout tick then error then confirmed",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturnsOnCall(0, ttxdb.Pending, "", nil)                           // call check
				c.transactionDB.GetStatusReturnsOnCall(1, ttxdb.Pending, "", nil)                           // dbFinality initial check
				c.transactionDB.GetStatusReturnsOnCall(2, ttxdb.Unknown, "", errors.New("transient error")) // dbFinality timeout check 1
				c.transactionDB.GetStatusReturnsOnCall(3, ttxdb.Confirmed, "", nil)                         // dbFinality timeout check 2
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			opts:        []ttx.TxOption{ttx.WithTimeout(2500 * time.Millisecond)},
			expectError: false,
		},
		{
			name: "initial get status error then confirmed",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturnsOnCall(0, ttxdb.Pending, "", nil)                         // call check
				c.transactionDB.GetStatusReturnsOnCall(1, ttxdb.Unknown, "", errors.New("initial error")) // dbFinality initial check
				c.transactionDB.GetStatusReturnsOnCall(2, ttxdb.Confirmed, "", nil)                       // dbFinality timeout check
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			opts:        []ttx.TxOption{ttx.WithTimeout(1500 * time.Millisecond)},
			expectError: false,
		},
		{
			name: "empty transaction id",
			opts: []ttx.TxOption{
				ttx.WithTxID(""),
				ttx.WithTMSID(token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}),
			},
			expectError:   true,
			errorContains: "transaction ID cannot be empty",
			expectErr:     ttx.ErrInvalidInput,
		},
		{
			name: "negative timeout",
			opts: []ttx.TxOption{
				ttx.WithTxID("tx_id"),
				ttx.WithTMSID(token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}),
				ttx.WithTimeout(-1 * time.Second),
			},
			expectError:   true,
			errorContains: "timeout cannot be negative",
			expectErr:     ttx.ErrInvalidInput,
		},
		{
			name: "timeout exceeds 24 hours",
			opts: []ttx.TxOption{
				ttx.WithTxID("tx_id"),
				ttx.WithTMSID(token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}),
				ttx.WithTimeout(25 * time.Hour),
			},
			expectError:   true,
			errorContains: "timeout cannot exceed 24 hours",
			expectErr:     ttx.ErrInvalidInput,
		},
		{
			name: "custom polling timeout",
			prepare: func(c *testFinalityViewContext) {
				c.transactionDB.GetStatusReturns(ttxdb.Confirmed, "", nil)
				c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)
			},
			opts: []ttx.TxOption{
				ttx.WithPollingTimeout(500 * time.Millisecond),
			},
			expectError: false,
		},
		{
			name: "invalid polling timeout",
			opts: []ttx.TxOption{
				ttx.WithTxID("tx_id"),
				ttx.WithTMSID(token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}),
				ttx.WithPollingTimeout(0),
			},
			expectError:   true,
			errorContains: "polling timeout must be positive",
			expectErr:     ttx.ErrInvalidInput,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testCtx := newTestFinalityViewContext(t)
			if tc.prepare != nil {
				tc.prepare(testCtx)
			}

			opts := []ttx.TxOption{
				ttx.WithTxID("tx_id"),
				ttx.WithTMSID(token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}),
				ttx.WithTimeout(100 * time.Millisecond),
			}
			if len(tc.opts) > 0 {
				opts = append(opts, tc.opts...)
			}

			v := ttx.NewFinalityWithOpts(opts...)

			_, err := v.Call(testCtx.ctx)

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
			}
		})
	}
}
