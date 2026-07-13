/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"errors"
	"sync"
	"testing"

	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFilter(ttxFetch, auditFetch *fakeStatusFetcher) *AcceptTxInDBsFilter {
	return &AcceptTxInDBsFilter{
		ttxBatcher:   newStatusBatcher(ttxFetch),
		auditBatcher: newStatusBatcher(auditFetch),
	}
}

func TestAcceptTxInDBsFilter_TtxHitShortCircuitsAudit(t *testing.T) {
	ttxFetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{"tx1": dbdriver.Confirmed}}
	auditFetch := &fakeStatusFetcher{}
	f := newTestFilter(ttxFetch, auditFetch)

	ok, err := f.Accept("tx1", nil)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 0, auditFetch.callCount(), "a tx already known to ttxDB must not trigger an auditDB lookup")
}

func TestAcceptTxInDBsFilter_FallsBackToAudit(t *testing.T) {
	ttxFetch := &fakeStatusFetcher{}
	auditFetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{"tx1": dbdriver.Pending}}
	f := newTestFilter(ttxFetch, auditFetch)

	ok, err := f.Accept("tx1", nil)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestAcceptTxInDBsFilter_UnknownEverywhere(t *testing.T) {
	f := newTestFilter(&fakeStatusFetcher{}, &fakeStatusFetcher{})

	ok, err := f.Accept("tx1", nil)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestAcceptTxInDBsFilter_PropagatesTtxError(t *testing.T) {
	wantErr := errors.New("ttx down")
	auditFetch := &fakeStatusFetcher{}
	f := newTestFilter(&fakeStatusFetcher{err: wantErr}, auditFetch)

	_, err := f.Accept("tx1", nil)
	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, 0, auditFetch.callCount(), "should not fall through to audit when the ttx lookup errors")
}

func TestAcceptTxInDBsFilter_PropagatesAuditError(t *testing.T) {
	wantErr := errors.New("audit down")
	f := newTestFilter(&fakeStatusFetcher{}, &fakeStatusFetcher{err: wantErr})

	_, err := f.Accept("tx1", nil)
	assert.ErrorIs(t, err, wantErr)
}

// TestAcceptTxInDBsFilter_ConcurrentAcceptsAreBounded is the direct
// regression test for the issue this filter was built to fix: N concurrent
// Accept calls for unrelated, unknown transactions must resolve via one
// batched ttx query and one batched audit query, not N query pairs.
func TestAcceptTxInDBsFilter_ConcurrentAcceptsAreBounded(t *testing.T) {
	ids := []string{"tx1", "tx2", "tx3", "tx4", "tx5"}
	ttxFetch := &fakeStatusFetcher{}
	auditFetch := &fakeStatusFetcher{}
	f := newTestFilter(ttxFetch, auditFetch)

	results := make([]bool, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			ok, err := f.Accept(id, nil)
			assert.NoError(t, err)
			results[i] = ok
		}(i, id)
	}
	wg.Wait()

	for _, ok := range results {
		assert.False(t, ok)
	}
	assert.Equal(t, 1, ttxFetch.callCount(), "concurrent Accept calls should issue one batched ttx query, not one per tx")
	assert.Equal(t, 1, auditFetch.callCount(), "concurrent Accept calls should issue one batched audit query, not one per tx")
}
