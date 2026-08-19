/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The functions exercised here (getActions, extractActions, deleteTokens) are
// unexported, so these tests live in the internal `tokens` package. That rules
// out the counterfeiter mocks (the mock package imports tokens, which would form
// an import cycle), so the tiny Cache and TMSProvider interfaces are stubbed by
// hand below.

// stubCache is a minimal, hand-rolled implementation of the Cache interface that
// records the keys it is queried with and how often it is mutated.
type stubCache struct {
	entry   *CacheEntry
	ok      bool
	getKeys []string
	adds    int
	deletes int
}

func (c *stubCache) Get(key string) (*CacheEntry, bool) {
	c.getKeys = append(c.getKeys, key)

	return c.entry, c.ok
}

func (c *stubCache) Add(string, *CacheEntry) { c.adds++ }

func (c *stubCache) Delete(string) { c.deletes++ }

// errTMSProvider is a TMSProvider whose GetManagementService always fails, used
// to drive the error branches of extractActions/getActions without constructing
// a real *token.ManagementService.
type errTMSProvider struct{ err error }

func (p errTMSProvider) GetManagementService(...token.ServiceOption) (*token.ManagementService, error) {
	return nil, p.err
}

// TestGetActions_CacheHit verifies that a populated cache entry short-circuits
// getActions: it returns the cached spend/append sets without touching the TMS.
func TestGetActions_CacheHit(t *testing.T) {
	ctx := context.Background()

	spend := []*token2.ID{{TxId: "in", Index: 0}}
	appendTokens := []TokenToAppend{{TxID: "tx1", Index: 0}}
	cache := &stubCache{entry: &CacheEntry{ToSpend: spend, ToAppend: appendTokens}, ok: true}

	// TMSProvider intentionally nil: a cache hit must not reach it.
	s := &Service{RequestsCache: cache}

	gotSpend, gotAppend, err := s.getActions(ctx, "tx1", nil)
	require.NoError(t, err)
	assert.Equal(t, spend, gotSpend)
	assert.Equal(t, appendTokens, gotAppend)
	assert.Equal(t, []string{"tx1"}, cache.getKeys)
}

// TestGetActions_CacheMiss_DelegatesToExtractActions verifies that a cache miss
// falls through to extractActions, so a TMS failure there surfaces to the caller.
func TestGetActions_CacheMiss_DelegatesToExtractActions(t *testing.T) {
	ctx := context.Background()

	cache := &stubCache{ok: false}
	s := &Service{RequestsCache: cache, TMSProvider: errTMSProvider{err: assert.AnError}}

	_, _, err := s.getActions(ctx, "tx1", &token.Request{Anchor: "tx1"})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed getting token management service")
	assert.Equal(t, []string{"tx1"}, cache.getKeys)
}

// TestExtractActions_TMSProviderError verifies that extractActions wraps and
// returns the error from the very first dependency it consults, the TMS provider.
func TestExtractActions_TMSProviderError(t *testing.T) {
	ctx := context.Background()

	s := &Service{TMSProvider: errTMSProvider{err: assert.AnError}}

	_, _, err := s.extractActions(ctx, "tx1", &token.Request{Anchor: "tx1"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed getting token management service")
}

// TestDeleteTokens_EmptyInput verifies the early return of deleteTokens: with no
// tokens to inspect it reports nothing deleted and never touches the network or
// TMS (both passed as nil here).
func TestDeleteTokens_EmptyInput(t *testing.T) {
	ctx := context.Background()

	s := &Service{}

	ids, err := s.deleteTokens(ctx, nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, ids)
}
