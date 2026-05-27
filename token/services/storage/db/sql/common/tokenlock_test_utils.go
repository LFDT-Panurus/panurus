/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/token"
	fscerrors "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/onsi/gomega"
)

type tokenLockStoreConstructor func(*sql.DB) *TokenLockStore

func TestLock(t *testing.T, store tokenLockStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.ID{TxId: "1234", Index: 5}
	trID := "5555"
	now := sqlmock.AnyArg()

	mockDB.
		ExpectExec("INSERT INTO TOKEN_LOCKS \\(consumer_tx_id, tx_id, idx, created_at\\) VALUES \\(\\$1, \\$2, \\$3, \\$4\\)").
		WithArgs(trID, tokenID.TxId, tokenID.Index, now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store(db).Lock(t.Context(), &tokenID, trID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestUnlockByTxID(t *testing.T, store tokenLockStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	consumerTxID := "1234"

	mockDB.
		ExpectExec("DELETE FROM TOKEN_LOCKS WHERE consumer_tx_id = \\$1").
		WithArgs(consumerTxID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store(db).UnlockByTxID(t.Context(), consumerTxID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

// TestLockContextCancelled verifies that Lock propagates context cancellation
// from ExecContext back to the caller as a context error.
func TestLockContextCancelled(t *testing.T, store tokenLockStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.ID{TxId: "1234", Index: 5}
	trID := "5555"
	now := sqlmock.AnyArg()

	// The mock will block for 1 s; the context expires after 10 ms.
	mockDB.
		ExpectExec("INSERT INTO TOKEN_LOCKS \\(consumer_tx_id, tx_id, idx, created_at\\) VALUES \\(\\$1, \\$2, \\$3, \\$4\\)").
		WithArgs(trID, tokenID.TxId, tokenID.Index, now).
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = store(db).Lock(ctx, &tokenID, trID)

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}

// TestUnlockByTxIDContextCancelled verifies that UnlockByTxID propagates context
// cancellation from ExecContext back to the caller as a context error.
func TestUnlockByTxIDContextCancelled(t *testing.T, store tokenLockStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	consumerTxID := "1234"

	mockDB.
		ExpectExec("DELETE FROM TOKEN_LOCKS WHERE consumer_tx_id = \\$1").
		WithArgs(consumerTxID).
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = store(db).UnlockByTxID(ctx, consumerTxID)

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}
