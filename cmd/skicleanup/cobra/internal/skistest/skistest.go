/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package skistest holds test helpers shared by the skicleanup command tests.
// It must not import skis, signers, or ownedskis: their tests import it.
package skistest

import (
	"context"
	"crypto"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	qcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	tokenpkg "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	scommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// LeafType has no registered provider, so a leaf's SKI is hex(sha256(payload)).
const LeafType tdriver.IdentityType = 100

// Leaf wraps payload as a TypedIdentity of LeafType.
func Leaf(tb testing.TB, payload string) token.Identity {
	tb.Helper()
	id, err := identity.WrapWithType(LeafType, token.Identity(payload))
	require.NoError(tb, err)

	return id
}

// Multisig wraps ids as a multisig identity.
func Multisig(tb testing.TB, ids ...token.Identity) token.Identity {
	tb.Helper()
	id, err := multisig.WrapIdentities(ids...)
	require.NoError(tb, err)

	return id
}

// Policy wraps ids as a "$0 OR $1" policy identity.
func Policy(tb testing.TB, ids ...token.Identity) token.Identity {
	tb.Helper()
	id, err := boolpolicy.WrapPolicyIdentity("$0 OR $1", ids...)
	require.NoError(tb, err)

	return id
}

// HTLC builds a script owner the way htlc.Transaction.recipientAsScript does.
func HTLC(tb testing.TB, sender, recipient token.Identity) token.Identity {
	tb.Helper()
	raw, err := json.Marshal(&htlc.Script{
		Sender:    []byte(sender),
		Recipient: []byte(recipient),
		Deadline:  time.Now().Add(time.Hour),
		HashInfo:  htlc.HashInfo{Hash: []byte("image"), HashFunc: crypto.SHA256, HashEncoding: encoding.Base64},
	})
	require.NoError(tb, err)
	id, err := identity.WrapWithType(htlc.ScriptType, raw)
	require.NoError(tb, err)

	return id
}

// Stored returns the (owner_type, owner_identity) pair the token service stores for owner:
// AuthorizationMultiplexer.OwnerType unwraps the TypedIdentity, tokens.parseOutput applies TypeToString.
func Stored(tb testing.TB, owner token.Identity) (string, []byte) {
	tb.Helper()
	ti, err := identity.UnmarshalTypedIdentity(owner)
	require.NoError(tb, err)

	return identity.TypeToString(ti.Type), ti.Identity
}

// SHA256Hex returns hex(sha256(b)).
func SHA256Hex(b []byte) string {
	h := sha256.Sum256(b)

	return hex.EncodeToString(h[:])
}

// LeafSKIs returns the SKIs of leaves with the given payloads, in order.
func LeafSKIs(payloads ...string) []string {
	skis := make([]string, len(payloads))
	for k, p := range payloads {
		skis[k] = SHA256Hex([]byte(p))
	}

	return skis
}

// SortedSKIs returns the sorted, de-duplicated leaf SKIs, as the keep-list lists them.
func SortedSKIs(payloads ...string) []string {
	skis := LeafSKIs(payloads...)
	slices.Sort(skis)

	return slices.Compact(skis)
}

// FailingWriter is an io.Writer whose writes always fail.
type FailingWriter struct{}

// Write returns an error.
func (FailingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// CaptureStdout returns what fn prints to os.Stdout.
func CaptureStdout(tb testing.TB, fn func()) string {
	tb.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(tb, err)
	os.Stdout = w
	tb.Cleanup(func() { os.Stdout = orig })

	type result struct {
		out []byte
		err error
	}
	done := make(chan result)
	go func() {
		b, err := io.ReadAll(r)
		done <- result{b, err}
	}()
	fn()
	require.NoError(tb, w.Close())
	os.Stdout = orig
	res := <-done
	require.NoError(tb, res.err)
	require.NoError(tb, r.Close())

	return string(res.out)
}

// TokenStore is the part of the token store the helpers write through.
type TokenStore interface {
	StoreToken(ctx context.Context, tr driver.TokenRecord, owners []string) error
	DeleteTokens(ctx context.Context, deletedBy string, ids ...*tokenpkg.ID) error
}

// StoreToken stores a token at index 0 of txID; an owned token gets owner wallet "w".
func StoreToken(tb testing.TB, ts TokenStore, txID string, ownerType string, identity []byte, owner bool, owners []string) {
	tb.Helper()
	tr := driver.TokenRecord{
		TxID:           txID,
		Index:          0,
		OwnerRaw:       identity,
		OwnerType:      ownerType,
		OwnerIdentity:  identity,
		Ledger:         []byte("ledger"),
		LedgerFormat:   "fmt",
		LedgerMetadata: []byte("meta"),
		IssuerRaw:      []byte("issuer"),
		Quantity:       "0x01",
		Type:           "USD",
		Amount:         big.NewInt(1),
		Owner:          owner,
	}
	if owner {
		tr.OwnerWalletID = "w"
	}
	require.NoError(tb, ts.StoreToken(context.Background(), tr, owners))
}

// StoreOwner stores an owned token the way the token service does: TypeToString(type) and the inner payload.
// It returns the inner payload.
func StoreOwner(tb testing.TB, ts TokenStore, txID string, owner token.Identity) []byte {
	tb.Helper()
	typ, raw := Stored(tb, owner)
	StoreToken(tb, ts, txID, typ, raw, true, nil)

	return raw
}

// DB is a SQLite database with the Tokens schema, opened the way signers.NewStores opens it.
// ConfigPath is a skicleanup config file that points at the database.
type DB struct {
	tb              testing.TB
	ConfigPath      string
	ReadDB          *sql.DB
	TableNames      sqlcommon.TableNames
	CondInterpreter qcommon.CondInterpreter
	Identity        *sqlcommon.IdentityStore
	Token           *sqlcommon.TokenStore
}

// NewDB creates a DB in a temporary directory. The DB is closed at test cleanup.
func NewDB(tb testing.TB) *DB {
	tb.Helper()
	dir := tb.TempDir()
	path := filepath.Join(dir, "data.db")
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(tb, os.WriteFile(configPath, []byte("driver: sqlite\ndataSource: "+path+"\n"), 0o600))

	db, err := sql.Open("sqlite", path)
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, db.Close()) })
	tables, err := sqlcommon.GetTableNamesWithConfig("", sqlcommon.StorageConfig{})
	require.NoError(tb, err)
	identityStore, err := sqlcommon.NewNoCacheIdentityStore(db, db, tables, sqlite.NewConditionInterpreter(), &fscSqlite.ErrorMapper{})
	require.NoError(tb, err)
	tokenStore, err := sqlite.NewTokenStore(&scommon.RWDB{ReadDB: db, WriteDB: db}, tables)
	require.NoError(tb, err)
	require.NoError(tb, tokenStore.CreateSchema())

	return &DB{
		tb:              tb,
		ConfigPath:      configPath,
		ReadDB:          db,
		TableNames:      tables,
		CondInterpreter: sqlite.NewConditionInterpreter(),
		Identity:        identityStore,
		Token:           tokenStore,
	}
}

// StoreToken stores a token the production way: owner wallet set, no Ownership rows.
func (db *DB) StoreToken(txID string, ownerType string, identity []byte, owner bool) {
	db.tb.Helper()
	StoreToken(db.tb, db.Token, txID, ownerType, identity, owner, nil)
}

// StoreTokenWithOwners stores a token with the given Ownership rows.
func (db *DB) StoreTokenWithOwners(txID string, ownerType string, identity []byte, owner bool, owners []string) {
	db.tb.Helper()
	StoreToken(db.tb, db.Token, txID, ownerType, identity, owner, owners)
}

// StoreOwner stores an owned token for owner; see StoreOwner.
func (db *DB) StoreOwner(txID string, owner token.Identity) {
	db.tb.Helper()
	StoreOwner(db.tb, db.Token, txID, owner)
}

// Spend deletes the token at index 0 of txID.
func (db *DB) Spend(txID string) {
	db.tb.Helper()
	require.NoError(db.tb, db.Token.DeleteTokens(context.Background(), "spender", &tokenpkg.ID{TxId: txID, Index: 0}))
}
