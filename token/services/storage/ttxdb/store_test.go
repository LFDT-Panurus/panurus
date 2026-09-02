/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttxdb_test

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	sqlite2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestDB(t *testing.T) {
	// create a new config service by loading the config file
	cp, err := config.NewProvider("./testdata/sqlite")
	require.NoError(t, err)

	manager := ttxdb.NewStoreServiceManager(
		tms.NewConfigServiceWrapper(config2.NewService(cp)),
		multiplexed.NewDriver(cp, sqlite.NewNamedDriver(cp, sqlite2.NewDbProvider())),
	)
	db1, err := manager.StoreServiceByTMSId(token.TMSID{Network: "pineapple", Namespace: "ns"})
	require.NoError(t, err)
	db2, err := manager.StoreServiceByTMSId(token.TMSID{Network: "grapes", Namespace: "ns"})
	require.NoError(t, err)

	TEndorserAcks(t, db1, db2)
}

// TestDBWithDigitsInChannelName is a regression test for
// https://github.com/LFDT-Panurus/panurus/issues/2034: building a store for a
// TMS whose channel name contains a digit used to panic while deriving the SQL
// table names, crashing the node instead of returning an error. It goes through
// the real NewStoreServiceManager path, and nothing along that path recovers.
func TestDBWithDigitsInChannelName(t *testing.T) {
	cp, err := config.NewProvider("./testdata/sqlite")
	require.NoError(t, err)

	manager := ttxdb.NewStoreServiceManager(
		tms.NewConfigServiceWrapper(config2.NewService(cp)),
		multiplexed.NewDriver(cp, sqlite.NewNamedDriver(cp, sqlite2.NewDbProvider())),
	)

	var db *ttxdb.StoreService
	require.NotPanics(t, func() {
		db, err = manager.StoreServiceByTMSId(token.TMSID{Network: "mango1", Channel: "channel1", Namespace: "ns2"})
	})
	require.NoError(t, err)
	require.NotNil(t, db)

	// The tables must really exist and be usable, not just be well-named.
	ctx := t.Context()
	require.NoError(t, db.AddTransactionEndorsementAck(ctx, "1", []byte("alice"), []byte("sigma")))
	acks, err := db.GetTransactionEndorsementAcks(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, []byte("sigma"), acks[token.Identity("alice").String()])
}

func TEndorserAcks(t *testing.T, db1, db2 *ttxdb.StoreService) {
	t.Helper()
	ctx := t.Context()
	wg := sync.WaitGroup{}
	n := 100
	wg.Add(n)
	for i := range n {
		go func(i int) {
			assert.NoError(t, db1.AddTransactionEndorsementAck(ctx, "1", fmt.Appendf(nil, "alice_%d", i), fmt.Appendf(nil, "sigma_%d", i)))
			acks, err := db1.GetTransactionEndorsementAcks(ctx, "1")
			assert.NoError(t, err)
			assert.NotEmpty(t, acks)
			assert.NoError(t, db2.AddTransactionEndorsementAck(ctx, "2", fmt.Appendf(nil, "bob_%d", i), fmt.Appendf(nil, "sigma_%d", i)))
			acks, err = db2.GetTransactionEndorsementAcks(ctx, "2")
			assert.NoError(t, err)
			assert.NotEmpty(t, acks)

			wg.Done()
		}(i)
	}
	wg.Wait()

	acks, err := db1.GetTransactionEndorsementAcks(ctx, "1")
	require.NoError(t, err)
	assert.Len(t, acks, n)
	for i := range n {
		assert.Equal(t, fmt.Appendf(nil, "sigma_%d", i), acks[token.Identity(fmt.Sprintf("alice_%d", i)).String()])
	}

	acks, err = db2.GetTransactionEndorsementAcks(ctx, "2")
	require.NoError(t, err)
	assert.Len(t, acks, n)
	for i := range n {
		assert.Equal(t, fmt.Appendf(nil, "sigma_%d", i), acks[token.Identity(fmt.Sprintf("bob_%d", i)).String()])
	}
}

type qsMock struct{}

func (qs qsMock) IsMine(ctx context.Context, id *token2.ID) (bool, error) {
	return true, nil
}

func TestTransactionRecords(t *testing.T) {
	now := time.Now()
	ctx := t.Context()

	// Transfer
	input := simpleTransfer()
	recs, err := ttxdb.TransactionRecords(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.TransactionRecord{
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)

	// Transfer with change
	input = transferWithChange()
	recs, err = ttxdb.TransactionRecords(ctx, &input, now)
	if err != nil {
		t.Fatal(err)
	}
	require.NoError(t, err)
	assert.Equal(t, []driver.TransactionRecord{
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Transfer,
			SenderEID:    "alice",
			RecipientEID: "bob",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Transfer,
			SenderEID:    "alice",
			RecipientEID: "alice",
			TokenType:    "TOK",
			Amount:       big.NewInt(90),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)

	// Issue
	input = simpleTransfer()
	input.Inputs = token.NewInputStream(qsMock{}, []*token.Input{}, 64)
	recs, err = ttxdb.TransactionRecords(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.TransactionRecord{
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Issue,
			SenderEID:    "",
			RecipientEID: "bob",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)

	// Redeem
	input = redeem()
	recs, err = ttxdb.TransactionRecords(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.TransactionRecord{
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Redeem,
			SenderEID:    "alice",
			RecipientEID: "",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)
}

func TestMovementRecords(t *testing.T) {
	now := time.Now()
	ctx := t.Context()

	// Transfer
	input := simpleTransfer()
	recs, err := ttxdb.Movements(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.MovementRecord{
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "alice",
			TokenType:    "TOK",
			Amount:       big.NewInt(-10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "bob",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)

	input = transferWithChange()
	recs, err = ttxdb.Movements(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.MovementRecord{
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "alice",
			TokenType:    "TOK",
			Amount:       big.NewInt(-10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "bob",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)

	// Issue
	input = simpleTransfer()
	input.Inputs = token.NewInputStream(qsMock{}, []*token.Input{}, 64)
	recs, err = ttxdb.Movements(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.MovementRecord{
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "bob",
			TokenType:    "TOK",
			Amount:       big.NewInt(10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)

	// Redeem
	input = redeem()
	recs, err = ttxdb.Movements(ctx, &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.MovementRecord{
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "alice",
			TokenType:    "TOK",
			Amount:       big.NewInt(-10),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)
}

func simpleTransfer() token.AuditRecord {
	input1 := &token.Input{
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	output1 := &token.Output{
		ActionIndex:  0,
		EnrollmentID: "bob",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(10),
	}

	return token.AuditRecord{
		Anchor:  "test",
		Inputs:  token.NewInputStream(qsMock{}, []*token.Input{input1}, 64),
		Outputs: token.NewOutputStream([]*token.Output{output1}, 64),
	}
}

func transferWithChange() token.AuditRecord {
	input1 := &token.Input{
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(100),
	}
	output1 := &token.Output{
		ActionIndex:  0,
		EnrollmentID: "bob",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	output2 := &token.Output{
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(90),
	}

	return token.AuditRecord{
		Anchor:  "test",
		Inputs:  token.NewInputStream(qsMock{}, []*token.Input{input1}, 64),
		Outputs: token.NewOutputStream([]*token.Output{output1, output2}, 64),
	}
}

func redeem() token.AuditRecord {
	input1 := &token.Input{
		ActionIndex:  0,
		EnrollmentID: "alice",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(10),
	}
	output1 := &token.Output{
		ActionIndex:  0,
		EnrollmentID: "",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(10),
	}

	return token.AuditRecord{
		Anchor:  "test",
		Inputs:  token.NewInputStream(qsMock{}, []*token.Input{input1}, 64),
		Outputs: token.NewOutputStream([]*token.Output{output1}, 64),
	}
}

// compositePolicySpend models a policy wallet paying 6 to a bank with 34
// change, where both the spent input and the change output are expanded into
// one row per member of the composite owner: same enrollment ID, same
// physical token.
func compositePolicySpend() token.AuditRecord {
	// distinct pointers to the same token ID value, as extraction produces
	inputMember0 := &token.Input{
		ActionIndex:  0,
		Id:           &token2.ID{TxId: "spent-tx", Index: 0},
		EnrollmentID: "policy",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(40),
	}
	inputMember1 := &token.Input{
		ActionIndex:  0,
		Id:           &token2.ID{TxId: "spent-tx", Index: 0},
		EnrollmentID: "policy",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(40),
	}
	bankOutput := &token.Output{
		ActionIndex:  0,
		Index:        1,
		EnrollmentID: "bank",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(6),
	}
	changeMember0 := &token.Output{
		ActionIndex:  0,
		Index:        2,
		EnrollmentID: "policy",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(34),
	}
	changeMember1 := &token.Output{
		ActionIndex:  0,
		Index:        2,
		EnrollmentID: "policy",
		Type:         "TOK",
		Quantity:     token2.NewQuantityFromUInt64(34),
	}

	return token.AuditRecord{
		Anchor:  "test-composite",
		Inputs:  token.NewInputStream(qsMock{}, []*token.Input{inputMember0, inputMember1}, 64),
		Outputs: token.NewOutputStream([]*token.Output{bankOutput, changeMember0, changeMember1}, 64),
	}
}

// TestMovementRecords_CompositePolicySpend checks the member-expanded change
// rows count once: the payer moves -6 and the bank +6.
func TestMovementRecords_CompositePolicySpend(t *testing.T) {
	now := time.Now()
	input := compositePolicySpend()
	recs, err := ttxdb.Movements(t.Context(), &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.MovementRecord{
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "policy",
			TokenType:    "TOK",
			Amount:       big.NewInt(-6),
			Timestamp:    now,
			Status:       driver.Pending,
		},
		{
			TxID:         string(input.Anchor),
			EnrollmentID: "bank",
			TokenType:    "TOK",
			Amount:       big.NewInt(6),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)
}

// TestTransactionRecords_CompositePolicySpend checks the change amount is
// recorded once despite the per-member rows.
func TestTransactionRecords_CompositePolicySpend(t *testing.T) {
	now := time.Now()
	input := compositePolicySpend()
	recs, err := ttxdb.TransactionRecords(t.Context(), &input, now)
	require.NoError(t, err)
	assert.Equal(t, []driver.TransactionRecord{
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Transfer,
			SenderEID:    "policy",
			RecipientEID: "bank",
			TokenType:    "TOK",
			Amount:       big.NewInt(6),
			Timestamp:    now,
			Status:       driver.Pending,
		},
		{
			TxID:         string(input.Anchor),
			ActionType:   driver.Transfer,
			SenderEID:    "policy",
			RecipientEID: "policy",
			TokenType:    "TOK",
			Amount:       big.NewInt(34),
			Timestamp:    now,
			Status:       driver.Pending,
		},
	}, recs)
}

// TestNotifyStatus_NotDroppedByCallerCanceledContext is a regression test for
// #2316: a status notification must not be lost just because the caller's own
// context (for example a recovery attempt's per-transaction timeout) has
// already expired by the time the underlying write succeeded and NotifyStatus
// runs. The context passed in only bounded the write; it says nothing about
// whether the listener is still worth waiting for.
func TestNotifyStatus_NotDroppedByCallerCanceledContext(t *testing.T) {
	store := ttxdb.StoreService{StatusSupport: dbcommon.NewStatusSupport()}
	txID := "tx-2316"

	ch := make(chan dbcommon.StatusEvent, 1)
	store.AddStatusListener(txID, ch)
	defer store.DeleteStatusListener(txID, ch)

	// Fill the buffer so a send cannot complete immediately: this is what makes
	// safeSend's ctx branch the only other option, and is the condition under
	// which the issue says the drop happens deterministically.
	ch <- dbcommon.StatusEvent{TxID: "filler"}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // simulate the deadline having already fired by the time we notify

	done := make(chan struct{})
	go func() {
		store.NotifyStatus(ctx, txID, driver.Confirmed, "")
		close(done)
	}()

	// Give safeSend's select a chance to run against the still-full buffer,
	// which is deliberately left untouched here. If the notification is still
	// tied to the canceled ctx, that select has exactly one ready case
	// (ctx.Done()) and returns almost immediately without sending; the fixed
	// version has no ready case yet and stays blocked, waiting for room.
	select {
	case <-done:
		t.Fatal("NotifyStatus returned without waiting for room in the listener buffer: " +
			"the notification was dropped because the caller's context was canceled")
	case <-time.After(200 * time.Millisecond):
		// still blocked, as expected once the notification isn't tied to ctx
	}

	// Free the buffer slot so the still-pending send can complete.
	<-ch

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("NotifyStatus did not return after the buffer freed up")
	}

	select {
	case event := <-ch:
		assert.Equal(t, txID, event.TxID)
		assert.Equal(t, driver.Confirmed, event.ValidationCode)
	default:
		t.Fatal("notification was dropped despite the caller's context being canceled")
	}
}
