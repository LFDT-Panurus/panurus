/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- minimal fakes ---

// fakeEntry is a listener entry that only needs to report the namespace its key belongs to,
// which is all queryByID reads from the evicted map.
type fakeEntry struct {
	ns driver.Namespace
}

func (f *fakeEntry) Namespace() driver.Namespace                      { return f.ns }
func (f *fakeEntry) OnStatus(context.Context, lookup.KeyInfo)         {}
func (f *fakeEntry) Equals(events.ListenerEntry[lookup.KeyInfo]) bool { return false }

type querierResult struct {
	raw []byte
	err error
}

type fakeQuerier struct {
	results map[driver.Namespace]querierResult
	queried []driver.Namespace
}

func (f *fakeQuerier) QueryStates(ns driver.Namespace, _ []byte) ([]byte, error) {
	f.queried = append(f.queried, ns)
	r, ok := f.results[ns]
	if !ok {
		return nil, errors.New("no stub for namespace " + ns)
	}

	return r.raw, r.err
}

type fakeScanner struct {
	called     bool
	startBlock uint64
}

func (f *fakeScanner) ScanFromBlock(_ context.Context, block uint64, _ fabric.DeliveryCallback) error {
	f.called = true
	f.startBlock = block

	return nil
}

// fakeVault is only reached from inside the scan callback, which these tests never invoke.
type fakeVault struct{}

func (fakeVault) InspectRWSet(context.Context, []byte, ...driver.Namespace) (*fabric.RWSet, error) {
	return nil, errors.New("InspectRWSet not expected in these tests")
}

// --- helpers ---

// evictedFor builds the evicted map queryByID expects, one key per namespace so that the
// order of the chaincode response is unambiguous.
func evictedFor(byNS map[driver.Namespace]driver.PKey) map[driver.PKey][]events.ListenerEntry[lookup.KeyInfo] {
	m := make(map[driver.PKey][]events.ListenerEntry[lookup.KeyInfo], len(byNS))
	for ns, key := range byNS {
		m[key] = []events.ListenerEntry[lookup.KeyInfo]{&fakeEntry{ns: ns}}
	}

	return m
}

// values marshals a chaincode QueryStates response.
func values(t *testing.T, vals ...[]byte) []byte {
	t.Helper()
	raw, err := json.Marshal(vals)
	require.NoError(t, err)

	return raw
}

func drain(ch <-chan []lookup.KeyInfo) []lookup.KeyInfo {
	var all []lookup.KeyInfo
	for batch := range ch {
		all = append(all, batch...)
	}

	return all
}

func newQuery(querier *fakeQuerier, scanner *fakeScanner) *lookup.DeliveryScanQueryByID {
	return &lookup.DeliveryScanQueryByID{
		Delivery: scanner,
		Querier:  querier,
		Vault:    fakeVault{},
	}
}

// --- tests ---

// When every namespace resolves, all keys are delivered and no block scan is needed.
func TestQueryByID_AllNamespacesResolve(t *testing.T) {
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
		"ns1": {raw: values(t, []byte("v1"))},
		"ns2": {raw: values(t, []byte("v2"))},
	}}
	scanner := &fakeScanner{}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, evictedFor(map[driver.Namespace]driver.PKey{
		"ns1": "k1",
		"ns2": "k2",
	}))
	require.NoError(t, err)

	got := drain(ch)
	require.Len(t, got, 2)
	assert.ElementsMatch(t, []lookup.KeyInfo{
		{Namespace: "ns1", Key: "k1", Value: []byte("v1")},
		{Namespace: "ns2", Key: "k2", Value: []byte("v2")},
	}, got)
	assert.False(t, scanner.called, "no block scan should start when every key resolves")
}

// Regression test for #1990. A namespace whose chaincode query fails must not take the rest
// of the batch down with it, and must fall back to the block scan.
func TestQueryByID_FailingNamespaceDoesNotDropOthers(t *testing.T) {
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
		"bad":  {err: errors.New("chaincode unreachable")},
		"good": {raw: values(t, []byte("v"))},
	}}
	scanner := &fakeScanner{}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, evictedFor(map[driver.Namespace]driver.PKey{
		"bad":  "kbad",
		"good": "kgood",
	}))
	require.NoError(t, err)

	got := drain(ch)
	assert.Equal(t, []lookup.KeyInfo{{Namespace: "good", Key: "kgood", Value: []byte("v")}}, got,
		"the healthy namespace must still be delivered")
	assert.Len(t, querier.queried, 2, "both namespaces must be attempted")
	assert.True(t, scanner.called, "the failed namespace must fall back to the block scan")
	assert.Equal(t, uint64(90), scanner.startBlock)
}

// A malformed chaincode response is isolated to its own namespace, same as a query error.
func TestQueryByID_MalformedResponseDoesNotDropOthers(t *testing.T) {
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
		"bad":  {raw: []byte("not json")},
		"good": {raw: values(t, []byte("v"))},
	}}
	scanner := &fakeScanner{}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, evictedFor(map[driver.Namespace]driver.PKey{
		"bad":  "kbad",
		"good": "kgood",
	}))
	require.NoError(t, err)

	got := drain(ch)
	assert.Equal(t, []lookup.KeyInfo{{Namespace: "good", Key: "kgood", Value: []byte("v")}}, got)
	assert.True(t, scanner.called, "the malformed namespace must fall back to the block scan")
}

// A key the chaincode reports as absent still triggers the scan, which is the pre-existing
// behaviour this change must not disturb.
func TestQueryByID_MissingValueTriggersScan(t *testing.T) {
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
		"ns1": {raw: values(t, []byte{})},
	}}
	scanner := &fakeScanner{}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, evictedFor(map[driver.Namespace]driver.PKey{
		"ns1": "k1",
	}))
	require.NoError(t, err)

	assert.Empty(t, drain(ch))
	assert.True(t, scanner.called)
}

// A peer that returns more values than keys requested
// must not index the keys slice out of range and crash the process.
// (From Issue #2055.)
func TestQueryByID_OversizedResponse(t *testing.T) {
	// 2 values returned, but only 1 key requested.
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
		"ns1": {raw: values(t, []byte("v1"), []byte("v2-unexpected"))},
	}}
	scanner := &fakeScanner{}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, evictedFor(map[driver.Namespace]driver.PKey{
		"ns1": "k1",
	}))
	require.NoError(t, err)

	// Nothing is delivered for the oversized namespace.
	// It should fall back to the block scan rather than crashing.
	assert.Empty(t, drain(ch))
	assert.True(t, scanner.called, "the oversized response must fall back to the block scan")
}

// A namespace holding several keys where only some resolve must deliver the resolved ones and
// fall back to the block scan for the rest, keeping the still-missing keys grouped under their
// namespace (the notFound branch that rewrites keysByNS rather than deleting it).
func TestQueryByID_PartialResolutionWithinNamespace(t *testing.T) {
	// One value present, one absent. Key<->value pairing follows the (map-random) request order,
	// so assert on the shape rather than a fixed key.
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
		"ns1": {raw: values(t, []byte("v"), []byte{})},
	}}
	scanner := &fakeScanner{}

	evicted := map[driver.PKey][]events.ListenerEntry[lookup.KeyInfo]{
		"k1": {&fakeEntry{ns: "ns1"}},
		"k2": {&fakeEntry{ns: "ns1"}},
	}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, evicted)
	require.NoError(t, err)

	got := drain(ch)
	require.Len(t, got, 1, "exactly the resolved key must be delivered")
	assert.Equal(t, driver.Namespace("ns1"), got[0].Namespace)
	assert.Contains(t, []driver.PKey{"k1", "k2"}, got[0].Key)
	assert.Equal(t, []byte("v"), got[0].Value)
	assert.True(t, scanner.called, "the unresolved key must fall back to the block scan")
	assert.Equal(t, uint64(90), scanner.startBlock)
}

// An empty batch has nothing to query and nothing to scan: the channel simply closes empty.
func TestQueryByID_EmptyEvicted(t *testing.T) {
	querier := &fakeQuerier{results: map[driver.Namespace]querierResult{}}
	scanner := &fakeScanner{}

	ch, err := newQuery(querier, scanner).QueryByID(t.Context(), 100, nil)
	require.NoError(t, err)

	assert.Empty(t, drain(ch))
	assert.Empty(t, querier.queried, "no namespace should be queried")
	assert.False(t, scanner.called, "an empty batch must not start a block scan")
}

// The fallback block scan must never start from an underflowed block number: on a chain younger
// than NumberPastBlocks it starts from FirstBlock instead of wrapping around to a block near
// MaxUint64, which would silently find nothing and surface no error.
func TestQueryByID_FallbackScanStartingBlock(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lastBlock driver.BlockNum
		expected  uint64
	}{
		{name: "young chain clamps to first block", lastBlock: 5, expected: lookup.FirstBlock},
		{name: "mature chain rewinds by the full window", lastBlock: 100, expected: 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			querier := &fakeQuerier{results: map[driver.Namespace]querierResult{
				"ns1": {raw: values(t, []byte{})},
			}}
			scanner := &fakeScanner{}

			ch, err := newQuery(querier, scanner).QueryByID(t.Context(), tc.lastBlock, evictedFor(map[driver.Namespace]driver.PKey{
				"ns1": "k1",
			}))
			require.NoError(t, err)

			assert.Empty(t, drain(ch))
			require.True(t, scanner.called, "a missing value must trigger the block scan")
			assert.Equal(t, tc.expected, scanner.startBlock)
		})
	}
}
