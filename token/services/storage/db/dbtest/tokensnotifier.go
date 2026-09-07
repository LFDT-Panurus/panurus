/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"context"
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

type TestTokenDB interface {
	driver.TokenStore

	StoreToken(ctx context.Context, tr driver.TokenRecord, owners []string) error
	GetAllTokenInfos(ctx context.Context, ids []*token.ID) ([][]byte, error)
}

const (
	TST = token.Type("TST")
	ABC = token.Type("ABC")
)

var TokenNotifierCases = []struct {
	Name string
	Fn   func(*testing.T, TestTokenDB, driver.TokenNotifier)
}{
	{"TokenNotifier", TTokenNotifier},
	{"SubscribeStore", TSubscribeStore},
	{"SubscribeStoreDelete", TSubscribeStoreDelete},
	{"SubscribeStoreNoCommit", TSubscribeStoreNoCommit},
	{"SubscribeRead", TSubscribeRead},
}

func TTokenNotifier(t *testing.T, db TestTokenDB, notifier driver.TokenNotifier) {
	t.Helper()
	ctx := t.Context()

	result, err := collectDBEvents[driver.TokenRecordReference](&tokenSubscriber{notifier: notifier})
	require.NoError(t, err)

	tr := driver.TokenRecord{
		TxID:           "tx-notify-1",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x02",
		Type:           TST,
		Amount:         big.NewInt(2),
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	}
	require.NoError(t, db.StoreToken(ctx, tr, []string{"alice"}))

	requireSizeOrSkip(t, result, 1)
	values := result.Values()
	require.Equal(t, driver.Insert, values[0].Op)
	require.Equal(t, driver.TokenRecordReference{
		TxID:  tr.TxID,
		Index: tr.Index,
	}, values[0].Val)
}

var tokenRecords = []driver.TokenRecord{
	{
		TxID:           "tx1",
		Index:          0,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         big.NewInt(0),
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	},
	{
		TxID:           "tx1",
		Index:          1,
		IssuerRaw:      []byte{},
		OwnerRaw:       []byte{1, 2, 3},
		OwnerType:      "idemix",
		OwnerIdentity:  []byte{},
		Ledger:         []byte("ledger"),
		LedgerMetadata: []byte{},
		Quantity:       "0x01",
		Type:           ABC,
		Amount:         big.NewInt(0),
		Owner:          true,
		Auditor:        false,
		Issuer:         false,
	},
}

type tokenSubscriber struct {
	notifier driver.TokenNotifier
}

func (t *tokenSubscriber) Subscribe(f func(operation driver.Operation, vals driver.TokenRecordReference)) error {
	return t.notifier.Subscribe(f)
}

// TransportError forwards the underlying notifier's transport-error channel
// when it exposes one (the Postgres notifier does), letting the collector skip
// a subtest whose LISTEN connection dropped mid-run. It returns nil for
// notifiers that do not report transport errors; the collector treats a nil
// channel as "never fails". See issue #2270.
func (t *tokenSubscriber) TransportError() <-chan error {
	if r, ok := t.notifier.(transportErrorReporter); ok {
		return r.TransportError()
	}

	return nil
}

func TSubscribeStore(t *testing.T, db TestTokenDB, notifier driver.TokenNotifier) {
	t.Helper()
	result, err := collectDBEvents[driver.TokenRecordReference](&tokenSubscriber{notifier: notifier})
	require.NoError(t, err)
	require.NoError(t, err)
	tx, err := db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.StoreToken(t.Context(), tokenRecords[0], []string{"alice"}))
	require.NoError(t, tx.StoreToken(t.Context(), tokenRecords[1], []string{"alice"}))
	require.NoError(t, tx.Commit())

	requireSizeOrSkip(t, result, 2)
}

func TSubscribeStoreDelete(t *testing.T, db TestTokenDB, notifier driver.TokenNotifier) {
	t.Helper()
	result, err := collectDBEvents[driver.TokenRecordReference](&tokenSubscriber{notifier: notifier})
	require.NoError(t, err)
	tx, err := db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.StoreToken(t.Context(), tokenRecords[0], []string{"alice"}))
	require.NoError(t, tx.StoreToken(t.Context(), tokenRecords[1], []string{"alice"}))
	require.NoError(t, tx.Delete(t.Context(), token.ID{TxId: "tx1", Index: 1}, "alice"))
	require.NoError(t, tx.Commit())

	requireSizeOrSkip(t, result, 3)
}

func TSubscribeStoreNoCommit(t *testing.T, db TestTokenDB, notifier driver.TokenNotifier) {
	t.Helper()
	result, err := collectDBEvents[driver.TokenRecordReference](&tokenSubscriber{notifier: notifier})
	require.NoError(t, err)
	tx, err := db.NewTokenDBTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.StoreToken(t.Context(), tokenRecords[0], []string{"alice"}))
	require.NoError(t, tx.StoreToken(t.Context(), tokenRecords[1], []string{"alice"}))

	requireSizeOrSkip(t, result, 0)
}

func TSubscribeRead(t *testing.T, db TestTokenDB, notifier driver.TokenNotifier) {
	t.Helper()
	result, err := collectDBEvents[driver.TokenRecordReference](&tokenSubscriber{notifier: notifier})
	require.NoError(t, err)
	tx, err := db.NewTokenDBTransaction()
	require.NoError(t, err)
	// require.NoError(t, tx.StoreToken(t.Context(), driver.TokenRecord{TxID: "tx1", Index: 0}, []string{"alice"}))
	_, _, err = tx.GetToken(t.Context(), token.ID{TxId: "tx1"}, true)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	requireSizeOrSkip(t, result, 0)
}
