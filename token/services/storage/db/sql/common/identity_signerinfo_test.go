/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	cache2 "github.com/LFDT-Panurus/panurus/token/services/utils/cache"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/sets"
	"github.com/stretchr/testify/require"
)

type recordingCache struct {
	entries map[string]bool
	adds    []string

	// onGet runs once, after the afterGets'th Get. See storeBeforeQuery.
	onGet     func()
	afterGets int
	gets      int
}

func newRecordingCache() *recordingCache {
	return &recordingCache{entries: map[string]bool{}}
}

// storeBeforeQuery arms store to run between GetExistingSignerInfo collecting its cache misses
// and issuing its query: it does one Get per identity there, so the batchSize'th is the last
// one before the database is consulted.
//
// That models the #2586 case the cached-positive check answers - a store whose row the query
// does not return, as under read-replica lag. A store landing mid-query is after the counter is
// sampled and only reachable from inside sqlmock's matching, which deadlocks against the
// store's own Exec; TestCacheSignerInfoVerdicts_* covers that half directly.
func (c *recordingCache) storeBeforeQuery(batchSize int, store func()) {
	c.afterGets = batchSize
	c.onGet = store
}

func (c *recordingCache) Get(key string) (bool, bool) {
	c.gets++
	v, ok := c.entries[key]
	if c.onGet != nil && c.gets >= c.afterGets {
		hook := c.onGet
		c.onGet = nil
		hook()
	}

	return v, ok
}

func (c *recordingCache) GetOrLoad(key string, loader func() (bool, error)) (bool, bool, error) {
	if v, ok := c.entries[key]; ok {
		return v, true, nil
	}
	v, err := loader()
	if err != nil {
		return false, false, err
	}
	c.Add(key, v)

	return v, false, nil
}

func (c *recordingCache) Add(key string, value bool) {
	c.entries[key] = value
	c.adds = append(c.adds, key)
}

func (c *recordingCache) Delete(key string) {
	delete(c.entries, key)
}

const signersSelect = "SELECT identity_hash FROM SIGNERS WHERE \\(identity_hash\\) IN \\(\\(\\$1\\), \\(\\$2\\), \\(\\$3\\)\\)"

func newSignerInfoStore(t *testing.T, signerInfoCache cache[bool]) *sqlFixture[*IdentityStore] {
	t.Helper()

	return newSQLFixture(t, func(db *sql.DB, _ sqlmock.Sqlmock) *IdentityStore {
		return &IdentityStore{
			readDB:          db,
			writeDB:         db,
			table:           identityTables{IdentityInfo: "ID_INFO", Signers: "SIGNERS"},
			ci:              newTestInterpreter(),
			signerInfoCache: signerInfoCache,
			auditInfoCache:  cache2.NewNoCache[[]byte](),
			errorWrapper:    passthroughErrorWrapper{},
		}
	})
}

func threeSigners() ([]tdriver.Identity, []string) {
	ids := []tdriver.Identity{[]byte("alice"), []byte("bob"), []byte("carol")}
	hashes := make([]string, len(ids))
	for i, id := range ids {
		hashes[i] = id.UniqueID()
	}

	return ids, hashes
}

// Regression test for #2562: the scan must fail, not the query.
func TestGetExistingSignerInfo_TruncatedScanIsAnError(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	ids, hashes := threeSigners()

	f.mock.ExpectQuery(signersSelect).
		WithArgs(hashes[0], hashes[1], hashes[2]).
		WillReturnRows(sqlmock.NewRows([]string{"identity_hash"}).
			AddRow(hashes[0]).
			AddRow(hashes[1]).
			AddRow(hashes[2]).
			RowError(1, errors.New("connection reset by peer"))).
		RowsWillBeClosed()

	existing, err := f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.Error(t, err, "a scan truncated by a connection failure must be reported, not read as an empty tail")
	require.ErrorContains(t, err, "connection reset by peer")
	require.Nil(t, existing)
	require.Empty(t, signerInfoCache.adds,
		"nothing may be cached from a truncated scan: caching false for an unreached row makes a real signer permanently invisible")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestGetExistingSignerInfo_CachesVerdictsOnCompleteScan(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	ids, hashes := threeSigners()

	f.mock.ExpectQuery(signersSelect).
		WithArgs(hashes[0], hashes[1], hashes[2]).
		WillReturnRows(sqlmock.NewRows([]string{"identity_hash"}).
			AddRow(hashes[0]).
			AddRow(hashes[2]))

	existing, err := f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.NoError(t, err)
	require.ElementsMatch(t, []string{hashes[0], hashes[2]}, existing)
	require.Equal(t, map[string]bool{hashes[0]: true, hashes[1]: false, hashes[2]: true}, signerInfoCache.entries)

	// Served from the cache: ExpectationsWereMet fails if a second query is issued.
	existing, err = f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.NoError(t, err)
	require.ElementsMatch(t, []string{hashes[0], hashes[2]}, existing)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// Regression test for #2586. A StoreSignerInfo commits and caches true for bob while the
// existence query is in flight, so the query's snapshot does not contain his row. The read
// must not then overwrite that true with its own stale false: the row is committed, and a
// cached false makes a genuine signer invisible to SignerInfoExists and identity.Provider.AreMe
// until the entry is evicted.
func TestGetExistingSignerInfo_ConcurrentStoreIsNotOverwritten(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	ids, hashes := threeSigners()

	f.mock.ExpectExec(signerInfoInsert).
		WithArgs(hashes[1], []byte(ids[1]), []byte("info")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectQuery(signersSelect).
		WithArgs(hashes[0], hashes[1], hashes[2]).
		WillReturnRows(sqlmock.NewRows([]string{"identity_hash"}))
	signerInfoCache.storeBeforeQuery(len(ids), func() {
		require.NoError(t, f.store.StoreSignerInfo(t.Context(), ids[1], []byte("info")))
	})

	existing, err := f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.NoError(t, err)
	require.Empty(t, existing, "the query's snapshot predates the store, so finding nothing is correct")
	require.Equal(t, map[string]bool{hashes[0]: false, hashes[1]: true, hashes[2]: false}, signerInfoCache.entries,
		"bob keeps the store's verdict; the identities no one stored keep the query's")
	require.NoError(t, f.mock.ExpectationsWereMet())

	// The next lookup is served entirely from the cache and must report bob as existing.
	exists, err := f.store.SignerInfoExists(t.Context(), ids[1])

	require.NoError(t, err)
	require.True(t, exists, "a committed signer must not read as absent")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// The store counter, exercised on cacheSignerInfoVerdicts directly. A store that lands while
// the query is in flight is counted, and the read must then cache none of its negatives.
//
// This is tested here rather than through GetExistingSignerInfo because the window only opens
// once the query has been issued: the cache seam fires before that, and firing from inside
// sqlmock's matching deadlocks on the expectation lock the store's own Exec needs.
func TestCacheSignerInfoVerdicts_CountedStoreSuppressesNegatives(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	_, hashes := threeSigners()

	storesAtQuery := f.store.signerInfoStoreCount()

	// A store commits while the query is in flight, and its cached positive is then evicted
	// before the read reaches its cache loop. This is the case the cached-positive check alone
	// cannot see: the production cache holds 5000 entries and evicts under churn, so with that
	// check alone the read writes a stale false over a committed row.
	f.store.cacheSignerInfoStored(hashes[1])
	signerInfoCache.Delete(hashes[1])

	f.store.cacheSignerInfoVerdicts(t.Context(), hashes, sets.New[string](), storesAtQuery)

	require.Empty(t, signerInfoCache.entries,
		"a store was counted, so no negative of this batch may be cached - the counter is never evicted")
}

// With no store counted, the negatives are authoritative and must be cached, or an identity
// that genuinely has no signer would be re-queried on every lookup.
func TestCacheSignerInfoVerdicts_UncountedBatchCachesNegatives(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	_, hashes := threeSigners()

	f.store.cacheSignerInfoVerdicts(
		t.Context(), hashes, sets.New(hashes[0]), f.store.signerInfoStoreCount(),
	)

	require.Equal(t, map[string]bool{hashes[0]: true, hashes[1]: false, hashes[2]: false}, signerInfoCache.entries)
}

// The cached-positive check still covers a store the counter cannot attribute to this call:
// one that committed before the count was sampled but whose row the query missed, as under
// read-replica lag.
func TestCacheSignerInfoVerdicts_CachedPositiveSurvivesUncountedStore(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	_, hashes := threeSigners()

	f.store.cacheSignerInfoStored(hashes[1])
	storesAtQuery := f.store.signerInfoStoreCount()

	f.store.cacheSignerInfoVerdicts(t.Context(), hashes, sets.New[string](), storesAtQuery)

	require.Equal(t, map[string]bool{hashes[0]: false, hashes[1]: true, hashes[2]: false}, signerInfoCache.entries,
		"bob's stored positive is kept even though no store was counted after the query")
}

// The same race through RegisterIdentityDescriptor, which caches an identity and its alias
// together: both verdicts must survive the read's cache loop.
func TestRegisterIdentityDescriptor_ConcurrentReadDoesNotOverwrite(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	ids, hashes := threeSigners()
	identity, alias := ids[0], ids[1]

	f.mock.ExpectBegin()
	f.mock.ExpectExec("SAVEPOINT " + savepointIdentitySignerInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec(signerInfoInsert).
		WithArgs(hashes[0], []byte(identity), []byte("info")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec("RELEASE SAVEPOINT " + savepointIdentitySignerInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec("SAVEPOINT " + savepointAliasSignerInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectExec(signerInfoInsert).
		WithArgs(hashes[1], []byte(alias), []byte("info")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec("RELEASE SAVEPOINT " + savepointAliasSignerInfo).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectCommit()
	f.mock.ExpectQuery(signersSelect).
		WithArgs(hashes[0], hashes[1], hashes[2]).
		WillReturnRows(sqlmock.NewRows([]string{"identity_hash"}))
	signerInfoCache.storeBeforeQuery(len(ids), func() {
		require.NoError(t, f.store.RegisterIdentityDescriptor(
			t.Context(),
			&idriver.IdentityDescriptor{Identity: identity, SignerInfo: []byte("info")},
			alias,
		))
	})

	existing, err := f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.NoError(t, err)
	require.Empty(t, existing)
	require.Equal(t, map[string]bool{hashes[0]: true, hashes[1]: true, hashes[2]: false}, signerInfoCache.entries,
		"both the registered identity and its alias keep the registration's verdict")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// The guard must not make a negative unwritable in general: an identity the query did not
// find, and that no store touched, has to be cached as absent, or every lookup for it would
// re-query forever.
func TestGetExistingSignerInfo_UncontendedNegativeIsCached(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	ids, hashes := threeSigners()

	f.mock.ExpectQuery(signersSelect).
		WithArgs(hashes[0], hashes[1], hashes[2]).
		WillReturnRows(sqlmock.NewRows([]string{"identity_hash"}))

	existing, err := f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.NoError(t, err)
	require.Empty(t, existing)
	require.Equal(t, map[string]bool{hashes[0]: false, hashes[1]: false, hashes[2]: false}, signerInfoCache.entries)

	// Served from the cache: ExpectationsWereMet fails if a second query is issued.
	existing, err = f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.NoError(t, err)
	require.Empty(t, existing)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestGetExistingSignerInfo_QueryErrorIsReported(t *testing.T) {
	signerInfoCache := newRecordingCache()
	f := newSignerInfoStore(t, signerInfoCache)
	ids, hashes := threeSigners()

	f.mock.ExpectQuery(signersSelect).
		WithArgs(hashes[0], hashes[1], hashes[2]).
		WillReturnError(errors.New("statement timeout"))

	existing, err := f.store.GetExistingSignerInfo(t.Context(), ids...)

	require.ErrorContains(t, err, "statement timeout")
	require.Nil(t, existing)
	require.Empty(t, signerInfoCache.adds)
	require.NoError(t, f.mock.ExpectationsWereMet())
}
