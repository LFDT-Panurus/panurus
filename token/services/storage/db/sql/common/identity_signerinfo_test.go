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
	cache2 "github.com/LFDT-Panurus/panurus/token/services/utils/cache"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

type recordingCache struct {
	entries map[string]bool
	adds    []string
}

func newRecordingCache() *recordingCache {
	return &recordingCache{entries: map[string]bool{}}
}

func (c *recordingCache) Get(key string) (bool, bool) {
	v, ok := c.entries[key]

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
