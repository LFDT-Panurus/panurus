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
