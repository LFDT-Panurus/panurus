/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cfgProvider func(string) driver2.Driver

func TokensTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range tokensCases {
		t.Run(c.Name, func(xt *testing.T) {
			driver := cfgProvider(c.Name)
			db, err := driver.NewToken("")
			require.NoError(xt, err)
			tokenDB, ok := db.(TestTokenDB)
			assert.True(xt, ok)
			defer utils.IgnoreError(tokenDB.Close)
			c.Fn(t, db.(TestTokenDB))
		})
	}

	for _, c := range TokenNotifierCases {
		t.Run(c.Name, func(xt *testing.T) {
			driver := cfgProvider(c.Name)
			db, err := driver.NewToken("", c.Name)
			require.NoError(xt, err)
			tokenDB, ok := db.(TestTokenDB)
			assert.True(xt, ok)
			defer utils.IgnoreError(tokenDB.Close)
			notifier, err := db.Notifier()
			if err != nil && errors.Is(err, storage.ErrNotSupported) {
				t.Logf("notifier not supported, skip test")

				return
			}
			require.NoError(xt, err)
			c.Fn(t, db.(TestTokenDB), notifier)
		})
	}
}

var tokensCases = []struct {
	Name string
	Fn   func(*testing.T, TestTokenDB)
}{
	{"Transaction", TTokenTransaction},
	{"SaveAndGetToken", TSaveAndGetToken},
	{"DeleteAndMine", TDeleteAndMine},
	{"GetTokenMetadata", TGetTokenInfos},
	{"ListAuditTokens", TListAuditTokens},
	{"ListIssuedTokens", TListIssuedTokens},
	{"IssuerBalance", TIssuerBalance},
	{"DeleteMultiple", TDeleteMultiple},
	{"PublicParams", TPublicParams},
	{"Certification", TCertification},
	{"QueryTokenDetails", TQueryTokenDetails},
	{"TTokenTypes", TTokenTypes},
	{"ListUnspentTokensByWallets", TListUnspentTokensByWallets},
	{"GetDeletedTokensPendingSKICleanup", TGetDeletedTokensPendingSKICleanup},
}

func TTokenTransaction(t *testing.T, db TestTokenDB) {
	t.Helper()
	tx, err := db.NewTokenDBTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = tx.StoreToken(t.Context(), driver2.TokenRecord{
		TxID:           "tx1",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           TST,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}, []string{"alice"})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	tx, err = db.NewTokenDBTransaction()
	if err != nil {
		t.Fatal(err)
	}
	tok, owners, err := tx.GetToken(t.Context(), token.ID{TxId: "tx1"}, false)
	require.NoError(t, err, "get token")
	assert.Equal(t, "0x02", tok.Quantity)
	assert.Equal(t, []string{"alice"}, owners)

	require.NoError(t, tx.Delete(t.Context(), token.ID{TxId: "tx1"}, "me"))
	tok, owners, err = tx.GetToken(t.Context(), token.ID{TxId: "tx1"}, false)
	require.NoError(t, err)
	assert.Nil(t, tok)
	assert.Empty(t, owners)

	tok, _, err = tx.GetToken(t.Context(), token.ID{TxId: "tx1"}, true) // include deleted
	require.NoError(t, err)
	assert.Equal(t, "0x02", tok.Quantity)
	require.NoError(t, tx.Rollback())

	tx, err = db.NewTokenDBTransaction()
	if err != nil {
		t.Fatal(err)
	}
	tok, owners, err = tx.GetToken(t.Context(), token.ID{TxId: "tx1"}, false)
	require.NoError(t, err)
	assert.NotNil(t, tok)
	assert.Equal(t, "0x02", tok.Quantity)
	assert.Equal(t, []string{"alice"}, owners)
	require.NoError(t, tx.Delete(t.Context(), token.ID{TxId: "tx1"}, "me"))
	require.NoError(t, tx.Commit())

	tx, err = db.NewTokenDBTransaction()
	if err != nil {
		t.Fatal(err)
	}
	tok, owners, err = tx.GetToken(t.Context(), token.ID{TxId: "tx1"}, false)
	require.NoError(t, err)
	assert.Nil(t, tok)
	assert.Equal(t, []string(nil), owners)
	require.NoError(t, tx.Commit())
}

func TSaveAndGetToken(t *testing.T, db TestTokenDB) {
	t.Helper()
	for i := range 20 {
		tr := driver2.TokenRecord{
			TxID:           fmt.Sprintf("tx%d", i),
			Index:          0,
			IssuerRaw:      []byte{},
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x02",
			Type:           TST,
			Amount:         2,
			Owner:          true,
			Auditor:        false,
			Issuer:         false,
		}
		require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))
	}
	tr := driver2.TokenRecord{
		TxID:           fmt.Sprintf("tx%d", 100),
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           TST,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"dan"}))

	tr = driver2.TokenRecord{
		TxID:           fmt.Sprintf("tx%d", 100), // only txid + index + ns is unique together
		Index:          1,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           TST,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice", "bob"}))

	tr = driver2.TokenRecord{
		TxID:           fmt.Sprintf("tx%d", 101),
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           ABC,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))

	tok, err := db.ListUnspentTokens(t.Context())
	require.NoError(t, err)
	assert.Len(t, tok.Tokens, 24, "unspentTokensIterator: expected all tokens to be returned (2 for the one owned by alice and bob)")
	assert.Equal(t, "48", tok.Sum(64).Decimal(), "expect sum to be 2*22")
	assert.Len(t, tok.ByType(TST).Tokens, 23, "expect filter on type to work")
	for _, token := range tok.Tokens {
		assert.NotNil(t, token.Owner, "expected owner to not be nil")
		assert.NotEmpty(t, token.Owner, "expected owner raw to not be empty")
	}

	tokens := getTokensBy(t, db, "alice", "")
	require.NoError(t, err)
	assert.Len(t, tokens, 22, "unspentTokensIteratorBy: expected only Alice tokens to be returned")

	tokens = getTokensBy(t, db, "", ABC)
	assert.Len(t, tokens, 1, "unspentTokensIteratorBy: expected only ABC tokens to be returned")

	tokens = getTokensBy(t, db, "alice", ABC)
	assert.Len(t, tokens, 1, "unspentTokensIteratorBy: expected only Alice ABC tokens to be returned")

	unsp, err := db.GetTokens(t.Context(), &token.ID{TxId: "tx101", Index: 0})
	require.NoError(t, err)
	assert.Len(t, unsp, 1)
	assert.Equal(t, "0x02", unsp[0].Quantity)
	assert.Equal(t, ABC, unsp[0].Type)

	tr = driver2.TokenRecord{
		TxID:           fmt.Sprintf("tx%d", 2000),
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "pineapple",
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           ABC,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))
	_, err = db.GetTokens(t.Context(), &token.ID{TxId: fmt.Sprintf("tx%d", 2000)})
	require.NoError(t, err)

	tx, err := db.NewTokenDBTransaction()
	require.NoError(t, err)
	_, owners, err := tx.GetToken(t.Context(), token.ID{TxId: fmt.Sprintf("tx%d", 2000)}, true)
	require.NoError(t, err)
	assert.Len(t, owners, 1)
	require.NoError(t, tx.Rollback())
}

func getTokensBy(t *testing.T, db TestTokenDB, ownerEID string, typ token.Type) []*token.UnspentToken {
	t.Helper()
	it, err := db.UnspentTokensIteratorBy(t.Context(), ownerEID, typ)
	require.NoError(t, err)

	tokens, err := iterators.ReadAllPointers(it)
	require.NoError(t, err, "error iterating over tokens")

	return tokens
}

func TDeleteAndMine(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()
	tr := driver2.TokenRecord{
		TxID:           "tx101",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))
	tr = driver2.TokenRecord{
		TxID:           "tx101",
		Index:          1,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"bob"}))
	tr = driver2.TokenRecord{
		TxID:           "tx102",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))
	require.NoError(t, db.DeleteTokens(ctx, "tx103", &token.ID{TxId: "tx101", Index: 0}))

	tok, err := db.ListUnspentTokens(t.Context())
	require.NoError(t, err)
	assert.Len(t, tok.Tokens, 2, "expected only tx101-0 to be deleted")

	mine, err := db.IsMine(ctx, "tx101", 0)
	require.NoError(t, err)
	assert.False(t, mine, "expected deleted token to not be mine")

	mine, err = db.IsMine(ctx, "tx101", 1)
	require.NoError(t, err)
	assert.True(t, mine, "expected existing token to be mine")

	tid := []*token.ID{
		{TxId: "tx101", Index: 0},
		{TxId: "tx101", Index: 1},
	}
	deletedBy, deleted, err := db.WhoDeletedTokens(ctx, tid...)
	require.NoError(t, err)
	assert.True(t, deleted[0], "expected tx101-0 to be deleted")
	assert.Equal(t, "tx103", deletedBy[0], "expected tx101-0 to be deleted by tx103")
	assert.False(t, deleted[1], "expected tx101-0 to not be deleted")
	assert.Empty(t, deletedBy[1], "expected tx101-0 to not be deleted by tx103")
}

// // ListAuditTokens returns the audited tokens associated to the passed ids
func TListAuditTokens(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()
	tr := driver2.TokenRecord{
		TxID:           "tx101",
		Index:          0,
		OwnerRaw:       []byte{1, 2},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "idemix",
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          false,
		Auditor:        true,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))
	tr = driver2.TokenRecord{
		TxID:           "tx101",
		Index:          1,
		OwnerRaw:       []byte{3, 4},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "idemix",
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           ABC,
		Amount:         0,
		Owner:          false,
		Auditor:        true,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))
	tr = driver2.TokenRecord{
		TxID:           "tx102",
		Index:          0,
		OwnerRaw:       []byte{5, 6},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "idemix",
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x03",
		Type:           ABC,
		Amount:         0,
		Owner:          false,
		Auditor:        true,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))

	tid := []*token.ID{
		{TxId: "tx101", Index: 0},
		{TxId: "tx101", Index: 1},
	}
	tok, err := db.ListAuditTokens(ctx, tid...)
	require.NoError(t, err)
	assert.Len(t, tok, 2)
	assert.Equal(t, "0x01", tok[0].Quantity, "expected tx101-0 to be returned")
	assert.Equal(t, "0x02", tok[1].Quantity, "expected tx101-1 to be returned")
	for _, token := range tok {
		assert.NotNil(t, token.Owner, "expected owner to not be nil")
		assert.NotEmpty(t, token.Owner, "expected owner raw to not be empty")
	}

	tok, err = db.ListAuditTokens(ctx)
	require.NoError(t, err)
	assert.Empty(t, tok)
}

func TListIssuedTokens(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()
	tr := driver2.TokenRecord{
		TxID:           "tx101",
		Index:          0,
		OwnerRaw:       []byte{1, 2},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "idemix",
		IssuerRaw:      []byte{11, 12},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          false,
		Auditor:        false,
		Issuer:         true,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))
	tr = driver2.TokenRecord{
		TxID:           "tx101",
		Index:          1,
		OwnerRaw:       []byte{3, 4},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "idemix",
		IssuerRaw:      []byte{13, 14},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           ABC,
		Amount:         0,
		Owner:          false,
		Auditor:        false,
		Issuer:         true,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))
	tr = driver2.TokenRecord{
		TxID:           "tx102",
		Index:          0,
		OwnerRaw:       []byte{5, 6},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		OwnerWalletID:  "idemix",
		IssuerRaw:      []byte{15, 16},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x03",
		Type:           "DEF",
		Amount:         0,
		Owner:          false,
		Auditor:        false,
		Issuer:         true,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, nil))

	tok, err := db.ListHistoryIssuedTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assert.Len(t, tok.Tokens, 3)
	assert.Equal(t, 3, tok.Count(), "expected 3 issued tokens")
	assert.Equal(t, "0x01", tok.Tokens[0].Quantity, "expected tx101-0 to be returned")
	assert.Equal(t, "0x02", tok.Tokens[1].Quantity, "expected tx101-1 to be returned")
	assert.Equal(t, "0x03", tok.Tokens[2].Quantity, "expected tx102-0 to be returned")
	for _, token := range tok.Tokens {
		assert.NotNil(t, token.Issuer, "expected issuer to not be nil")
		assert.NotEmpty(t, token.Issuer, "expected issuer raw to not be empty")
		assert.NotNil(t, token.Owner, "expected owner to not be nil")
		assert.NotEmpty(t, token.Owner, "expected owner raw to not be empty")
	}

	tok, err = db.ListHistoryIssuedTokens(ctx)
	require.NoError(t, err)
	assert.Len(t, tok.ByType("DEF").Tokens, 1, "expected tx102-0 to be filtered")
	for _, token := range tok.Tokens {
		assert.NotNil(t, token.Issuer, "expected issuer to not be nil")
		assert.NotEmpty(t, token.Issuer, "expected issuer raw to not be empty")
		assert.NotNil(t, token.Owner, "expected owner to not be nil")
		assert.NotEmpty(t, token.Owner, "expected owner raw to not be empty")
	}
}

// TIssuerBalance verifies that IssuedBalance and RedeemedBalance sum the amounts of the
// issued and redeemed tokens respectively, honoring the token type filter, and that a
// redeemed token (empty owner) can be stored and is excluded from the issued balance.
func TIssuerBalance(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()

	// two issued tokens of type ABC (amounts 10 and 20) and one of type DEF (amount 5)
	issued := []struct {
		txID   string
		idx    uint64
		typ    token.Type
		amount uint64
	}{
		{"txi1", 0, ABC, 10},
		{"txi2", 0, ABC, 20},
		{"txi3", 0, "DEF", 5},
	}
	for _, i := range issued {
		require.NoError(t, db.StoreToken(ctx, driver2.TokenRecord{
			TxID:           i.txID,
			Index:          i.idx,
			OwnerRaw:       []byte{1, 2},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			OwnerWalletID:  "idemix",
			IssuerRaw:      []byte{11, 12},
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       fmt.Sprintf("0x%02x", i.amount),
			Type:           i.typ,
			Amount:         i.amount,
			Owner:          false,
			Auditor:        false,
			Issuer:         true,
			Redeemed:       false,
		}, nil))
	}

	// two redeemed tokens of type ABC (amounts 3 and 4): empty owner, issuer=true, redeemed=true
	redeemed := []struct {
		txID   string
		amount uint64
	}{
		{"txr1", 3},
		{"txr2", 4},
	}
	for _, r := range redeemed {
		require.NoError(t, db.StoreToken(ctx, driver2.TokenRecord{
			TxID:           r.txID,
			Index:          0,
			OwnerRaw:       []byte{},
			OwnerType:      "",
			OwnerIdentity:  []byte{},
			OwnerWalletID:  "",
			IssuerRaw:      []byte{11, 12},
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       fmt.Sprintf("0x%02x", r.amount),
			Type:           ABC,
			Amount:         r.amount,
			Owner:          false,
			Auditor:        false,
			Issuer:         true,
			Redeemed:       true,
		}, nil))
	}

	// issued balance across all types = 10 + 20 + 5 = 35
	issuedBalance, err := db.IssuedBalance(ctx, tdriver.IssuerBalanceQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(35), issuedBalance.Int64())

	// issued balance for ABC = 10 + 20 = 30
	issuedABC, err := db.IssuedBalance(ctx, tdriver.IssuerBalanceQuery{TokenType: ABC})
	require.NoError(t, err)
	assert.Equal(t, int64(30), issuedABC.Int64())

	// redeemed balance across all types = 3 + 4 = 7
	redeemedBalance, err := db.RedeemedBalance(ctx, tdriver.IssuerBalanceQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(7), redeemedBalance.Int64())

	// redeemed balance for DEF = 0 (no redeems of that type)
	redeemedDEF, err := db.RedeemedBalance(ctx, tdriver.IssuerBalanceQuery{TokenType: "DEF"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), redeemedDEF.Int64())

	// Simulate a node that is both issuer and auditor: when a token is spent (transferred/redeemed),
	// DeleteTokens marks the issuer's record is_deleted=true. IssuedBalance must still count it —
	// issued balance is a permanent historical sum, not an unspent balance.
	require.NoError(t, db.DeleteTokens(ctx, "spender", &token.ID{TxId: "txi1", Index: 0}))
	issuedAfterDelete, err := db.IssuedBalance(ctx, tdriver.IssuerBalanceQuery{TokenType: ABC})
	require.NoError(t, err)
	assert.Equal(t, int64(30), issuedAfterDelete.Int64(), "issued balance must not drop when an issued token is spent")
}

// GetTokenMetadata retrieves the token information for the passed ids.
// For each id, the callback is invoked to unmarshal the token information
func TGetTokenInfos(t *testing.T, db TestTokenDB) {
	t.Helper()
	tr := driver2.TokenRecord{
		TxID:           "tx101",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("tx101l"),
		LedgerMetadata: []byte("tx101"),
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"bob"}))
	tr = driver2.TokenRecord{
		TxID:           "tx102",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("tx102l"),
		LedgerMetadata: []byte("tx102"),
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))
	tr = driver2.TokenRecord{
		TxID:           "tx102",
		Index:          1,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("tx102l"),
		LedgerMetadata: []byte("tx102"),
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         0,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))

	ids := []*token.ID{
		{TxId: "tx101", Index: 0},
		{TxId: "tx102", Index: 0},
	}

	infos, err := db.GetAllTokenInfos(t.Context(), ids)
	require.NoError(t, err)
	for i, info := range infos {
		assert.Equal(t, ids[i].TxId, string(info))
		assert.Equal(t, uint64(0), ids[i].Index)
	}
	assert.Len(t, infos, 2)
	assert.Equal(t, "tx101", string(infos[0]))
	assert.Equal(t, "tx102", string(infos[1]))

	// Order should match the provided order
	ids = []*token.ID{
		{TxId: "tx102", Index: 1},
		{TxId: "tx102", Index: 0},
		{TxId: "tx101", Index: 0},
		{TxId: "non existent", Index: 0},
	}
	_, err = db.GetTokenMetadata(t.Context(), ids)
	require.Error(t, err)

	ids = []*token.ID{
		{TxId: "tx102", Index: 1},
		{TxId: "tx102", Index: 0},
		{TxId: "tx101", Index: 0},
	}
	infos, err = db.GetTokenMetadata(t.Context(), ids)
	require.NoError(t, err)
	assert.Equal(t, "tx102", string(infos[0]))
	assert.Equal(t, "tx102", string(infos[1]))
	assert.Equal(t, "tx101", string(infos[2]))

	// infos and outputs
	toks, infos, _, err := db.GetTokenOutputsAndMeta(t.Context(), ids)
	require.NoError(t, err)
	assert.Len(t, infos, 3)
	assert.Equal(t, "tx102", string(infos[0]))
	assert.Equal(t, "tx102", string(infos[1]))
	assert.Equal(t, "tx101", string(infos[2]))
	assert.Len(t, toks, 3)
	assert.Equal(t, "tx102l", string(toks[0]))
	assert.Equal(t, "tx102l", string(toks[1]))
	assert.Equal(t, "tx101l", string(toks[2]))
}

func TDeleteMultiple(t *testing.T, db TestTokenDB) {
	t.Helper()
	tr := driver2.TokenRecord{
		TxID:           "tx101",
		Index:          0,
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Owner:          true,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))
	tr = driver2.TokenRecord{
		TxID:           "tx101",
		Index:          1,
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Owner:          true,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"bob"}))
	tr = driver2.TokenRecord{
		TxID:           "tx102",
		Index:          0,
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Owner:          true,
	}
	require.NoError(t, db.StoreToken(t.Context(), tr, []string{"alice"}))
	require.NoError(t, db.DeleteTokens(t.Context(), "", &token.ID{TxId: "tx101", Index: 0}, &token.ID{TxId: "tx102", Index: 0}))

	tok, err := db.ListUnspentTokens(t.Context())
	require.NoError(t, err)
	assert.Len(t, tok.Tokens, 1, "expected only tx101-0 and tx102-0 to be deleted", tok.Tokens)

	mine, err := db.IsMine(t.Context(), "tx101", 0)
	require.NoError(t, err)
	assert.False(t, mine, "expected deleted token to not be mine")

	mine, err = db.IsMine(t.Context(), "tx101", 1)
	require.NoError(t, err)
	assert.True(t, mine, "expected existing token to be mine")
}

func TPublicParams(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()
	b := []byte("test bytes")
	bHash := utils.Hashable(b).Raw()
	b1 := []byte("test bytes1")
	b1Hash := utils.Hashable(b1).Raw()

	res, err := db.PublicParams(ctx)
	require.NoError(t, err) // not found
	assert.Nil(t, res)

	err = db.StorePublicParams(ctx, b)
	require.NoError(t, err)

	res, err = db.PublicParams(ctx)
	require.NoError(t, err)
	assert.Equal(t, res, b)

	// retrieve by hash
	res, err = db.PublicParamsByHash(ctx, bHash)
	require.NoError(t, err)
	assert.Equal(t, res, b)

	err = db.StorePublicParams(ctx, b1)
	require.NoError(t, err)

	res, err = db.PublicParams(ctx)
	require.NoError(t, err)
	assert.Equal(t, res, b1)

	// retrieve by hash
	res, err = db.PublicParamsByHash(ctx, b1Hash)
	require.NoError(t, err)
	assert.Equal(t, res, b1)
}

func TCertification(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()
	wg := sync.WaitGroup{}
	wg.Add(40)
	for i := range 40 {
		go func(i int) {
			tokenID := &token.ID{
				TxId:  fmt.Sprintf("tx_%d", i),
				Index: 0,
			}
			err := db.StoreToken(t.Context(), driver2.TokenRecord{
				TxID:           tokenID.TxId,
				Index:          tokenID.Index,
				OwnerRaw:       []byte{1, 2, 3},
				OwnerType:      "idemix",
				OwnerIdentity:  []byte{},
				Quantity:       "0x01",
				Ledger:         []byte("ledger"),
				LedgerMetadata: []byte{},
				Type:           ABC,
				Owner:          true,
			}, []string{"alice"})
			if err != nil {
				t.Error(err)
			}

			assert.NoError(t, db.StoreCertifications(ctx, map[*token.ID][]byte{
				tokenID: fmt.Appendf(nil, "certification_%d", i),
			}))
			assert.True(t, db.ExistsCertification(ctx, tokenID))
			certifications, err := db.GetCertifications(ctx, []*token.ID{tokenID})
			assert.NoError(t, err)
			for _, bytes := range certifications {
				assert.Equal(t, fmt.Sprintf("certification_%d", i), string(bytes))
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	for i := range 40 {
		tokenID := &token.ID{
			TxId:  fmt.Sprintf("tx_%d", i),
			Index: 0,
		}
		assert.True(t, db.ExistsCertification(ctx, tokenID))
		certifications, err := db.GetCertifications(ctx, []*token.ID{tokenID})
		require.NoError(t, err)
		for _, bytes := range certifications {
			assert.Equal(t, fmt.Sprintf("certification_%d", i), string(bytes))
		}
	}

	// check the certification of a token that was never stored
	tokenID := &token.ID{
		TxId:  "pineapple",
		Index: 0,
	}
	assert.False(t, db.ExistsCertification(ctx, tokenID))

	certifications, err := db.GetCertifications(ctx, []*token.ID{tokenID})
	require.Error(t, err)
	assert.Empty(t, certifications)

	// store an empty certification and check that an error is returned
	err = db.StoreCertifications(ctx, map[*token.ID][]byte{
		tokenID: {},
	})
	require.Error(t, err)
	certifications, err = db.GetCertifications(ctx, []*token.ID{tokenID})
	require.Error(t, err)
	assert.Empty(t, certifications)
}

func TQueryTokenDetails(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.NewTokenDBTransaction()
	if err != nil {
		t.Fatal(err)
	}
	tx1 := driver2.TokenRecord{
		TxID:           "tx1",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           "TST1",
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	tx2 := driver2.TokenRecord{
		TxID:           "tx2",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "htlc",
		OwnerIdentity:  []byte("{}"),
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           TST,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	tx21 := driver2.TokenRecord{
		TxID:           "tx2",
		Index:          1,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "htlc",
		OwnerIdentity:  []byte("{}"),
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           TST,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}

	err = tx.StoreToken(t.Context(), tx1, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.StoreToken(t.Context(), tx2, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.StoreToken(t.Context(), tx21, []string{"bob"})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Commit()
	if err != nil {
		t.Fatal(err)
	}

	// all
	res, err := db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{})
	require.NoError(t, err)
	assert.Len(t, res, 3)
	assertEqual(t, tx1, res[0])
	assertEqual(t, tx2, res[1])
	assertEqual(t, tx21, res[2])
	assert.False(t, res[0].IsSpent, "tx1 is not spent")
	assert.False(t, res[1].IsSpent, "tx2-0 is not spent")
	assert.False(t, res[2].IsSpent, "tx2-1 is not spent")

	// alice
	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{WalletID: "alice"})
	require.NoError(t, err)
	assert.Len(t, res, 2)
	assertEqual(t, tx1, res[0])
	assertEqual(t, tx2, res[1])

	// alice TST1
	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{WalletID: "alice", TokenType: "TST1"})
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assertEqual(t, tx1, res[0])
	balance, err := db.Balance(ctx, "alice", "TST1")
	require.NoError(t, err)
	assert.Equal(t, balance.Uint64(), res[0].Amount.Uint64())

	// alice TST
	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{WalletID: "alice", TokenType: TST})
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assertEqual(t, tx2, res[0])
	balance, err = db.Balance(ctx, "alice", TST)
	require.NoError(t, err)
	assert.Equal(t, balance.Uint64(), res[0].Amount.Uint64())

	// bob TST
	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{WalletID: "bob", TokenType: TST})
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assertEqual(t, tx21, res[0])
	balance, err = db.Balance(ctx, "bob", TST)
	require.NoError(t, err)
	assert.Equal(t, balance.Uint64(), res[0].Amount.Uint64())

	// spent
	require.NoError(t, db.DeleteTokens(ctx, "delby", &token.ID{TxId: "tx2", Index: 1}))
	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{})
	require.NoError(t, err)
	assert.Len(t, res, 2)
	assert.False(t, res[0].IsSpent, "tx1 is not spent")
	assert.False(t, res[1].IsSpent, "tx2-0 is not spent")

	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{IncludeDeleted: true})
	require.NoError(t, err)
	assert.Len(t, res, 3)
	assert.False(t, res[0].IsSpent, "tx1 is not spent")
	assert.False(t, res[1].IsSpent, "tx2-0 is not spent")
	assert.True(t, res[2].IsSpent, "tx2-1 is spent")
	assert.Equal(t, "delby", res[2].SpentBy)

	// by ids
	res, err = db.QueryTokenDetails(ctx, driver2.QueryTokenDetailsParams{IDs: []*token.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 0}}, IncludeDeleted: true})
	require.NoError(t, err)
	assert.Len(t, res, 2)
	assertEqual(t, tx1, res[0])
	assertEqual(t, tx2, res[1])
}

func TTokenTypes(t *testing.T, db TestTokenDB) {
	t.Helper()
	tx, err := db.NewTokenDBTransaction()
	require.NoError(t, err)
	tx1 := driver2.TokenRecord{
		TxID:           "tx1",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerFormat:   "CLEAR",
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           TST,
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	tx2 := driver2.TokenRecord{
		TxID:           "tx2",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "htlc",
		OwnerIdentity:  []byte("{}"),
		Ledger:         []byte("ledger"),
		LedgerFormat:   "CLEAR1",
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           "TST1",
		Amount:         2,
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, tx.StoreToken(t.Context(), tx1, []string{"alice"}))
	require.NoError(t, tx.StoreToken(t.Context(), tx2, []string{"alice"}))
	require.NoError(t, tx.Commit())

	it, err := db.SpendableTokensIteratorBy(t.Context(), "", TST)
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, TST, 1)
	it, err = db.SpendableTokensIteratorBy(t.Context(), "", "TST1")
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, "TST1", 1)

	// make all non-spendable
	tx, err = db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.SetSpendableBySupportedTokenFormats(t.Context(), []token.Format{"htlc"}))
	require.NoError(t, tx.Commit())

	it, err = db.SpendableTokensIteratorBy(t.Context(), "", TST)
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, TST, 0)
	it, err = db.SpendableTokensIteratorBy(t.Context(), "", "TST1")
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, "TST1", 0)

	// make TST spendable
	tx, err = db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.SetSpendableBySupportedTokenFormats(t.Context(), []token.Format{"CLEAR"}))
	require.NoError(t, tx.Commit())

	it, err = db.SpendableTokensIteratorBy(t.Context(), "", TST)
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, TST, 1)
	it, err = db.SpendableTokensIteratorBy(t.Context(), "", "TST1")
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, "TST1", 0)

	// make TST1 spendable
	tx, err = db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.SetSpendableBySupportedTokenFormats(t.Context(), []token.Format{"CLEAR1"}))
	require.NoError(t, tx.Commit())

	it, err = db.SpendableTokensIteratorBy(t.Context(), "", TST)
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, TST, 0)
	it, err = db.SpendableTokensIteratorBy(t.Context(), "", "TST1")
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, "TST1", 1)

	// make both spendable
	tx, err = db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.SetSpendableBySupportedTokenFormats(t.Context(), []token.Format{"CLEAR", "CLEAR1"}))
	require.NoError(t, tx.Commit())

	it, err = db.SpendableTokensIteratorBy(t.Context(), "", TST)
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, TST, 1)
	it, err = db.SpendableTokensIteratorBy(t.Context(), "", "TST1")
	require.NoError(t, err)
	consumeSpendableTokensIterator(t, it, "TST1", 1)
}

// TListUnspentTokensByWallets exercises the batch variant of
// ListUnspentTokensBy. Seeds tokens across multiple wallets — two via
// the ownership wallet_id column (StoreToken's owner list) and one via
// the owner_wallet_id column on TokenRecord — then verifies the batch
// method partitions results correctly and honors the empty-input /
// missing-wallet / type-filter contracts.
func TListUnspentTokensByWallets(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()

	storeForOwners := func(txID string, typ token.Type, owners []string) {
		require.NoError(t, db.StoreToken(ctx, driver2.TokenRecord{
			TxID:           txID,
			Index:          0,
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x02",
			Type:           typ,
			Amount:         2,
			Owner:          true,
		}, owners))
	}
	storeForOwnerWalletID := func(txID string, typ token.Type, walletID string) {
		require.NoError(t, db.StoreToken(ctx, driver2.TokenRecord{
			TxID:           txID,
			Index:          0,
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			OwnerWalletID:  walletID,
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x02",
			Type:           typ,
			Amount:         2,
			Owner:          true,
		}, nil))
	}
	storeDisagree := func(txID string, typ token.Type, ownerWalletID string, owners []string) {
		require.NoError(t, db.StoreToken(ctx, driver2.TokenRecord{
			TxID:           txID,
			Index:          0,
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			OwnerWalletID:  ownerWalletID,
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x02",
			Type:           typ,
			Amount:         2,
			Owner:          true,
		}, owners))
	}

	storeForOwners("bw-a-1", TST, []string{"alice"})
	storeForOwners("bw-a-2", TST, []string{"alice"})
	storeForOwners("bw-a-3", ABC, []string{"alice"}) // second type for alice; exercises typ filter
	storeForOwners("bw-b-1", TST, []string{"bob"})
	storeForOwnerWalletID("bw-p-1", TST, "pineapple") // exercises the owner_wallet_id column path

	// Batch across the two storage columns: alice (wallet_id) +
	// pineapple (owner_wallet_id). bob is in the DB but not requested.
	// Empty typ → all types, so alice's ABC token is included.
	res, err := db.ListUnspentTokensByWallets(ctx, []string{"alice", "pineapple"}, "")
	require.NoError(t, err)
	assert.Len(t, res, 2)
	require.NotNil(t, res["alice"])
	require.NotNil(t, res["pineapple"])
	assert.Len(t, res["alice"].Tokens, 3)
	assert.Len(t, res["pineapple"].Tokens, 1)
	_, hasBob := res["bob"]
	assert.False(t, hasBob, "unrequested wallet must not appear in result")

	// Narrow to TST — alice's ABC token drops out.
	res, err = db.ListUnspentTokensByWallets(ctx, []string{"alice", "pineapple"}, TST)
	require.NoError(t, err)
	assert.Len(t, res["alice"].Tokens, 2)
	assert.Len(t, res["pineapple"].Tokens, 1)

	// Narrow to ABC — only alice has one.
	res, err = db.ListUnspentTokensByWallets(ctx, []string{"alice", "pineapple"}, ABC)
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Len(t, res["alice"].Tokens, 1)

	// Unknown wallets produce no map entry (not an error, not a zero-token entry).
	res, err = db.ListUnspentTokensByWallets(ctx, []string{"ghost"}, "")
	require.NoError(t, err)
	assert.Empty(t, res)

	// Mixed existent + missing: only the existent ones come back.
	res, err = db.ListUnspentTokensByWallets(ctx, []string{"alice", "ghost", "bob"}, TST)
	require.NoError(t, err)
	assert.Len(t, res, 2)
	assert.Len(t, res["alice"].Tokens, 2)
	assert.Len(t, res["bob"].Tokens, 1)

	// Empty / nil input must short-circuit.
	res, err = db.ListUnspentTokensByWallets(ctx, nil, "")
	require.NoError(t, err)
	assert.Empty(t, res)
	res, err = db.ListUnspentTokensByWallets(ctx, []string{}, "")
	require.NoError(t, err)
	assert.Empty(t, res)

	// Disagreement between tokens.owner_wallet_id and ownership.wallet_id:
	// StoreToken writes them independently, so a row can match via the
	// Ownership join while carrying a different owner_wallet_id. The caller
	// asked for "carol", so the token must be bucketed under "carol" — not
	// under the unrequested "stranger".
	storeDisagree("bw-dis-1", TST, "stranger", []string{"carol"})
	res, err = db.ListUnspentTokensByWallets(ctx, []string{"carol"}, "")
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.NotNil(t, res["carol"])
	assert.Len(t, res["carol"].Tokens, 1)
	_, hasStranger := res["stranger"]
	assert.False(t, hasStranger, "unrequested wallet must not appear as a bucket key")
}

func consumeSpendableTokensIterator(t *testing.T, it tdriver.SpendableTokensIterator, tokenType token.Type, count int) {
	t.Helper()
	defer it.Close()
	for range count {
		tok, err := it.Next()
		require.NoError(t, err)
		assert.Equal(t, tokenType, tok.Type)
	}
	tok, err := it.Next()
	require.NoError(t, err)
	assert.Nil(t, tok)
}

func assertEqual(t *testing.T, r driver2.TokenRecord, d driver2.TokenDetails) {
	t.Helper()
	assert.Equal(t, r.TxID, d.TxID)
	assert.Equal(t, r.Index, d.Index)
	assert.Equal(t, r.Amount, d.Amount.Uint64())
	assert.Equal(t, r.OwnerType, d.OwnerType)
}

func TGetDeletedTokensPendingSKICleanup(t *testing.T, db TestTokenDB) {
	t.Helper()
	ctx := t.Context()

	// Helper function to create and delete a token
	createAndDeleteToken := func(txID string, index uint64, ownerType string) {
		tr := driver2.TokenRecord{
			TxID:           txID,
			Index:          index,
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      ownerType,
			OwnerIdentity:  fmt.Appendf(nil, "owner_%s_%d", txID, index),
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x01",
			Type:           ABC,
			Amount:         1,
			Owner:          true,
		}
		require.NoError(t, db.StoreToken(ctx, tr, []string{"alice"}))
		require.NoError(t, db.DeleteTokens(ctx, "deleter_tx", &token.ID{TxId: txID, Index: index}))
	}

	// Test 1: Empty database - should return empty slice
	t.Run("EmptyDatabase", func(t *testing.T) {
		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 10)
		require.NoError(t, err, "query on empty database should not error")
		assert.Empty(t, tokens, "empty database should return empty slice")
	})

	// Test 2: No deleted tokens - only active tokens
	t.Run("NoDeletedTokens", func(t *testing.T) {
		tr := driver2.TokenRecord{
			TxID:           "active1",
			Index:          0,
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte("active_owner"),
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x01",
			Type:           ABC,
			Amount:         1,
			Owner:          true,
		}
		require.NoError(t, db.StoreToken(ctx, tr, []string{"alice"}))

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 10)
		require.NoError(t, err, "query with only active tokens should not error")
		assert.Empty(t, tokens, "active tokens should not be returned")
	})

	// Test 3: Basic functionality - deleted tokens without cleanup records
	t.Run("BasicFunctionality", func(t *testing.T) {
		// Create deleted tokens
		createAndDeleteToken("basic1", 0, "idemix")
		createAndDeleteToken("basic2", 0, "x509")

		// Query with very short duration to get recently deleted tokens
		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 10)
		require.NoError(t, err, "query should not error")

		// Find our test tokens
		found := make(map[string]bool)
		for _, tok := range tokens {
			if tok.TxID == "basic1" || tok.TxID == "basic2" {
				found[tok.TxID] = true
				// Verify token fields are populated correctly
				assert.NotEmpty(t, tok.TxID, "TxID should be populated")
				assert.NotEmpty(t, tok.OwnerIdentity, "OwnerIdentity should be populated")
				assert.NotEmpty(t, tok.OwnerType, "OwnerType should be populated")
				assert.False(t, tok.DeletedAt.IsZero(), "DeletedAt should be populated")
			}
		}

		assert.True(t, found["basic1"], "basic1 token should be found")
		assert.True(t, found["basic2"], "basic2 token should be found")
	})

	// Test 4: Exclusion logic - tokens with cleanup records should be excluded
	t.Run("ExcludeCleanedTokens", func(t *testing.T) {
		// Create two deleted tokens
		createAndDeleteToken("cleaned1", 0, "idemix")
		createAndDeleteToken("uncleaned1", 0, "idemix")

		// Mark one as cleaned
		require.NoError(t, db.MarkTokenCleaned(ctx, "cleaned1", 0, "test_cleaner"))

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 100)
		require.NoError(t, err, "query should not error")

		// Verify cleaned token is not in results
		for _, tok := range tokens {
			assert.NotEqual(t, "cleaned1", tok.TxID, "cleaned token should not be returned")
		}

		// Verify uncleaned token is in results
		found := false
		for _, tok := range tokens {
			if tok.TxID == "uncleaned1" && tok.Index == 0 {
				found = true

				break
			}
		}
		assert.True(t, found, "uncleaned token should be in results")
	})

	// Test 5: Time filtering - only tokens older than duration
	t.Run("TimeFiltering", func(t *testing.T) {
		// Create an old token
		createAndDeleteToken("old_token", 0, "idemix")

		// Wait a bit to ensure time difference
		time.Sleep(100 * time.Millisecond)

		// Create a recent token
		createAndDeleteToken("recent_token", 0, "idemix")

		// Query with duration that should exclude the recent token
		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 50*time.Millisecond, 100)
		require.NoError(t, err, "query should not error")

		// Verify old token is in results
		foundOld := false
		foundRecent := false
		for _, tok := range tokens {
			if tok.TxID == "old_token" {
				foundOld = true
			}
			if tok.TxID == "recent_token" {
				foundRecent = true
			}
		}

		assert.True(t, foundOld, "old token should be in results")
		assert.False(t, foundRecent, "recent token should not be in results")
	})

	// Test 6: Limit parameter
	t.Run("LimitParameter", func(t *testing.T) {
		// Create more tokens than limit
		for i := range 10 {
			createAndDeleteToken(fmt.Sprintf("limit_test_%d", i), 0, "idemix")
		}

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 5)
		require.NoError(t, err, "query should not error")

		// Count our test tokens in results
		count := 0
		for _, tok := range tokens {
			if strings.HasPrefix(tok.TxID, "limit_test_") {
				count++
			}
		}
		assert.LessOrEqual(t, count, 5, "should respect limit parameter")
	})

	// Test 7: Ordering - results should be ordered by spent_at ascending
	t.Run("OrderingBySpentAt", func(t *testing.T) {
		// Create tokens with time delays to ensure different spent_at times
		createAndDeleteToken("order1", 0, "idemix")
		time.Sleep(10 * time.Millisecond)
		createAndDeleteToken("order2", 0, "idemix")
		time.Sleep(10 * time.Millisecond)
		createAndDeleteToken("order3", 0, "idemix")

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 100)
		require.NoError(t, err, "query should not error")

		// Find our test tokens in results
		var testTokens []driver2.DeletedToken
		for _, tok := range tokens {
			if tok.TxID == "order1" || tok.TxID == "order2" || tok.TxID == "order3" {
				testTokens = append(testTokens, tok)
			}
		}

		require.GreaterOrEqual(t, len(testTokens), 3, "should find all test tokens")

		// Verify ordering (oldest first)
		for i := 1; i < len(testTokens); i++ {
			assert.True(t, testTokens[i-1].DeletedAt.Before(testTokens[i].DeletedAt) ||
				testTokens[i-1].DeletedAt.Equal(testTokens[i].DeletedAt),
				"tokens should be ordered by DeletedAt ascending")
		}
	})

	// Test 8: Multiple owner types
	t.Run("MultipleOwnerTypes", func(t *testing.T) {
		createAndDeleteToken("idemix_token", 0, "idemix")
		createAndDeleteToken("x509_token", 0, "x509")
		createAndDeleteToken("htlc_token", 0, "htlc")

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 100)
		require.NoError(t, err, "query should not error")

		// Verify all owner types are present
		ownerTypes := make(map[string]bool)
		for _, tok := range tokens {
			if tok.TxID == "idemix_token" || tok.TxID == "x509_token" || tok.TxID == "htlc_token" {
				ownerTypes[tok.OwnerType] = true
			}
		}

		assert.True(t, ownerTypes["idemix"], "idemix owner type should be present")
		assert.True(t, ownerTypes["x509"], "x509 owner type should be present")
		assert.True(t, ownerTypes["htlc"], "htlc owner type should be present")
	})

	// Test 9: Zero limit
	t.Run("ZeroLimit", func(t *testing.T) {
		createAndDeleteToken("zero_limit", 0, "idemix")

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 0)
		require.NoError(t, err, "query with zero limit should not error")
		assert.Empty(t, tokens, "zero limit should return empty results")
	})

	// Test 10: All tokens already cleaned
	t.Run("AllTokensCleaned", func(t *testing.T) {
		// Create and immediately mark as cleaned
		createAndDeleteToken("all_cleaned1", 0, "idemix")
		createAndDeleteToken("all_cleaned2", 0, "idemix")

		require.NoError(t, db.MarkTokenCleaned(ctx, "all_cleaned1", 0, "cleaner"))
		require.NoError(t, db.MarkTokenCleaned(ctx, "all_cleaned2", 0, "cleaner"))

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 100)
		require.NoError(t, err, "query should not error")

		// Verify our cleaned tokens are not in results
		for _, tok := range tokens {
			assert.NotEqual(t, "all_cleaned1", tok.TxID, "cleaned token should not be returned")
			assert.NotEqual(t, "all_cleaned2", tok.TxID, "cleaned token should not be returned")
		}
	})

	// Test 11: Multiple indices for same transaction
	t.Run("MultipleIndices", func(t *testing.T) {
		createAndDeleteToken("multi_idx", 0, "idemix")
		createAndDeleteToken("multi_idx", 1, "idemix")
		createAndDeleteToken("multi_idx", 2, "idemix")

		tokens, err := db.GetDeletedTokensPendingSKICleanup(ctx, 0, 100)
		require.NoError(t, err, "query should not error")

		// Count how many indices we found
		count := 0
		for _, tok := range tokens {
			if tok.TxID == "multi_idx" {
				count++
			}
		}

		assert.Equal(t, 3, count, "should return all indices for the same transaction")
	})
}
