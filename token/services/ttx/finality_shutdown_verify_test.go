/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscview "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/stretchr/testify/require"
)

// TestFinalityView_ShutdownIsNotTimeout asserts the desired behaviour of #2639:
// a finality wait cancelled by node shutdown must surface the shutdown cause,
// not ErrFinalityTimeout.
func TestFinalityView_ShutdownIsNotTimeout(t *testing.T) {
	c := newTestFinalityViewContext(t)
	c.transactionDB.GetStatusReturns(ttxdb.Pending, "", nil)
	c.auditDB.GetStatusReturns(ttxdb.Unknown, "", nil)

	viewCtx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	c.ctx.ContextReturns(viewCtx)

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel(fscview.ErrCancelledByShutdown)
	}()

	v := ttx.NewFinalityWithOpts(
		ttx.WithTxID("tx_id"),
		ttx.WithTMSID(token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}),
		ttx.WithTimeout(5*time.Second),
		ttx.WithPollingTimeout(time.Second),
	)
	_, err := v.Call(c.ctx)
	require.Error(t, err)
	t.Logf("finality error: %v", err)
	require.True(t, errors.Is(err, fscview.ErrCancelledByShutdown), "shutdown cause must be preserved, got: %v", err)
	require.False(t, errors.Is(err, ttx.ErrFinalityTimeout), "shutdown must not be reported as a timeout, got: %v", err)
}
