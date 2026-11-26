/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"bytes"
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/cmd/skicleanup/cobra/signers"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fallbackType has no registered provider, so the SHA-256 fallback applies.
const fallbackType = "custom"

func runDB(t *testing.T, db *testDB) ([]string, string, error) {
	t.Helper()
	var errOut bytes.Buffer
	skis, err := Run(context.Background(), db.source(), newSKIExtractor(&fakeSignerStore{}), &errOut)

	return skis, errOut.String(), err
}

// C1
func TestRun_UnspentOwnedTokenListed(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hex([]byte("alice"))}, skis)
}

// C2
func TestRun_SpentOwnedTokenListed(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	db.spend("tx1")

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hex([]byte("alice"))}, skis)
}

// C3
func TestRun_NotOwnedTokenExcluded(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("audited"), false)
	db.storeToken("tx2", fallbackType, []byte("alice"), true)

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hex([]byte("alice"))}, skis)
}

// C4
func TestRun_DeduplicatesAndSorts(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("bob"), true)
	db.storeToken("tx2", fallbackType, []byte("alice"), true)
	db.storeToken("tx3", fallbackType, []byte("bob"), true)
	db.storeTokenWithOwners("tx4", fallbackType, []byte("alice"), true, []string{"a", "b"})

	skis, errOut, err := runDB(t, db)
	require.NoError(t, err)
	want := []string{sha256Hex([]byte("alice")), sha256Hex([]byte("bob"))}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	assert.Equal(t, want, skis)
	// DISTINCT in SQL: two owners, not four tokens.
	assert.Contains(t, errOut, "Owners processed : 2")
}

// Different identities that derive the same SKI produce one entry.
func TestRun_DeduplicatesSKIsAcrossOwners(t *testing.T) {
	src := &fakeSource{owners: []Owner{{Identity: []byte("a"), Type: "t"}, {Identity: []byte("b"), Type: "t"}}}
	ext := &fakeExtractor{skis: map[string][]string{"a": {"k2", "k1"}, "b": {"k1"}}}

	skis, err := Run(context.Background(), src, ext, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, []string{"k1", "k2"}, skis)
}

// C5
func TestRun_FallbackSKIMatchesCleanupExtractor(t *testing.T) {
	db := newTestDB(t)
	id := []byte("some-identity")
	db.storeToken("tx1", fallbackType, id, true)

	want, err := cleanup.NewSKIExtractor().GetSKIsFromIdentity(context.Background(), id, fallbackType)
	require.NoError(t, err)

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, want, skis)
	assert.Equal(t, []string{sha256Hex(id)}, skis)
}

// C6
func TestRun_X509YieldsNoSKIs(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", x509.IdentityTypeString, []byte("cert"), true)

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Empty(t, skis)
}

// C7
func TestRun_ExtractionErrorReported(t *testing.T) {
	src := &fakeSource{owners: []Owner{
		{Identity: []byte("good"), Type: "t"},
		{Identity: []byte("bad1"), Type: "t1"},
		{Identity: []byte("bad2"), Type: "t2"},
	}}
	ext := &fakeExtractor{
		skis: map[string][]string{"good": {"k"}},
		errs: map[string]error{"bad1": errors.New("boom1"), "bad2": errors.New("boom2")},
	}
	var errOut bytes.Buffer

	skis, err := Run(context.Background(), src, ext, &errOut)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 owner(s)")
	assert.Nil(t, skis)
	assert.Contains(t, errOut.String(), "owner type [t1] identity sha256 ["+sha256Hex([]byte("bad1"))+"]: boom1")
	assert.Contains(t, errOut.String(), "owner type [t2] identity sha256 ["+sha256Hex([]byte("bad2"))+"]: boom2")
}

// C8
func TestRun_QueryError(t *testing.T) {
	db := newTestDB(t)
	src := db.source()
	src.TokensTable = "missing_table"

	skis, err := Run(context.Background(), src, &fakeExtractor{}, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query owners from table [missing_table]")
	assert.Nil(t, skis)
}

// C9
func TestRun_SourceError(t *testing.T) {
	src := &fakeSource{owners: []Owner{{Identity: []byte("a"), Type: "t"}}, err: errors.New("iteration broke")}
	var errOut bytes.Buffer

	skis, err := Run(context.Background(), src, &fakeExtractor{skis: map[string][]string{"a": {"k"}}}, &errOut)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iteration broke")
	assert.Nil(t, skis)
}

// The SQL source stops at the first callback error and returns it.
func TestSQLOwnerSource_StopsOnCallbackError(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("a"), true)
	db.storeToken("tx2", fallbackType, []byte("b"), true)

	calls := 0
	err := db.source().ForEachOwner(context.Background(), func(Owner) error {
		calls++

		return errors.New("stop")
	})
	require.EqualError(t, err, "stop")
	assert.Equal(t, 1, calls)
}

// A failing errOut makes Run fail instead of hiding errors.
func TestRun_ErrOutWriteError(t *testing.T) {
	src := &fakeSource{owners: []Owner{{Identity: []byte("a"), Type: "t"}}}

	_, err := Run(context.Background(), src, &fakeExtractor{errs: map[string]error{"a": errors.New("x")}}, failingWriter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write warning")

	_, err = Run(context.Background(), &fakeSource{}, &fakeExtractor{}, failingWriter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write statistics")
}

// C16
func TestNewSKIExtractor_Providers(t *testing.T) {
	ctx := context.Background()
	store := &mock.IdentityStoreService{}
	store.GetSignerInfoReturns(nil, errors.New("not found"))
	ext := signers.NewSKIExtractor(store)

	skis, err := ext.GetSKIsFromIdentity(ctx, []byte("any"), x509.IdentityTypeString)
	require.NoError(t, err)
	assert.Empty(t, skis, "x509 must use the no-op provider")

	_, err = ext.GetSKIsFromIdentity(ctx, []byte("garbage"), "idemix")
	require.Error(t, err, "idemix must use the idemix provider, not the SHA-256 fallback")

	_, err = ext.GetSKIsFromIdentity(ctx, []byte("unknown"), "idemixnym")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve signer info")
	assert.Equal(t, 1, store.GetSignerInfoCallCount())
}

// C17: the standard wallet path stores no Ownership row; the token must still be listed.
func TestRun_TokenWithoutOwnershipRowListed(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)

	var n int
	require.NoError(t, db.stores.ReadDB.QueryRow("SELECT COUNT(*) FROM "+db.stores.TableNames.Ownership).Scan(&n))
	require.Equal(t, 0, n, "precondition: no Ownership row")

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Equal(t, []string{sha256Hex([]byte("alice"))}, skis)
}

// C19
func TestRun_EmptyIdentity(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte{}, true)

	skis, _, err := runDB(t, db)
	require.NoError(t, err)
	assert.Empty(t, skis)
}

type fakeSource struct {
	owners []Owner
	err    error
}

func (f *fakeSource) ForEachOwner(_ context.Context, fn func(Owner) error) error {
	for _, o := range f.owners {
		if err := fn(o); err != nil {
			return err
		}
	}

	return f.err
}

type fakeExtractor struct {
	skis map[string][]string
	errs map[string]error
}

func (f *fakeExtractor) GetSKIsFromIdentity(_ context.Context, id []byte, _ string) ([]string, error) {
	if err := f.errs[string(id)]; err != nil {
		return nil, err
	}

	return f.skis[string(id)], nil
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// A row that cannot be scanned makes the SQL source fail with "scan owner".
func TestSQLOwnerSource_ScanError(t *testing.T) {
	db := newTestDB(t)
	_, err := db.stores.ReadDB.Exec("CREATE TABLE bad_tokens (owner_identity BLOB, owner_type TEXT, owner BOOL)")
	require.NoError(t, err)
	_, err = db.stores.ReadDB.Exec("INSERT INTO bad_tokens VALUES (x'01', NULL, true)")
	require.NoError(t, err)
	src := db.source()
	src.TokensTable = "bad_tokens"

	err = src.ForEachOwner(context.Background(), func(Owner) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan owner")
}
