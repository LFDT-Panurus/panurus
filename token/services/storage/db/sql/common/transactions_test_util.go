/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	driver2 "database/sql/driver"
	"math/big"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/pagination"
	"github.com/LFDT-Panurus/panurus/token/token"
	fscerrors "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/onsi/gomega"
)

type transactionsStoreConstructor func(*sql.DB) *TransactionStore

type QueryConstructorTraits struct {
	SupportsIN          bool
	MultipleParenthesis bool
}

func TestGetTokenRequest(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := string("1234")
	output := []byte("some_result")
	mockDB.
		ExpectQuery("SELECT request FROM REQUESTS WHERE tx_id = \\$1").
		WithArgs(input).
		WillReturnRows(mockDB.NewRows([]string{"request"}).AddRow(output))

	info, err := store(db).GetTokenRequest(t.Context(), input)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(info).To(gomega.Equal(output))
}

func TestQueryMovements(t *testing.T, store transactionsStoreConstructor, traits QueryConstructorTraits) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	record := driver.MovementRecord{
		TxID:         "1234",
		EnrollmentID: "5678",
		TokenType:    token.Type("USD"),
		Amount:       big.NewInt(-100),
		Status:       driver.Deleted,
	}
	output := []driver2.Value{
		record.TxID, record.EnrollmentID, record.TokenType, int(record.Amount.Int64()), record.Status,
	}
	var query string
	if traits.MultipleParenthesis {
		query = "SELECT MOVEMENTS.tx_id, enrollment_id, token_type, amount, REQUESTS.status " +
			"FROM MOVEMENTS LEFT JOIN REQUESTS ON MOVEMENTS.tx_id = REQUESTS.tx_id " +
			"WHERE \\(\\(\\(enrollment_id = \\$1\\)\\)\\) AND \\(\\(\\(token_type = \\$2\\)\\)\\) AND \\(\\(\\(status = \\$3\\)\\)\\) AND \\(amount < \\$4\\) " +
			"ORDER BY MOVEMENTS.stored_at DESC " +
			"LIMIT \\$5"
	} else {
		query = "SELECT MOVEMENTS.tx_id, enrollment_id, token_type, amount, REQUESTS.status " +
			"FROM MOVEMENTS LEFT JOIN REQUESTS ON MOVEMENTS.tx_id = REQUESTS.tx_id " +
			"WHERE \\(enrollment_id = \\$1\\) AND \\(token_type = \\$2\\) AND \\(status = \\$3\\) AND \\(amount < \\$4\\) " +
			"ORDER BY MOVEMENTS.stored_at DESC " +
			"LIMIT \\$5"
	}
	mockDB.
		ExpectQuery(query).
		WithArgs(record.EnrollmentID, record.TokenType, record.Status, 0, 1).
		WillReturnRows(mockDB.NewRows([]string{"tx_id", "enrollment_id", "token_type", "amount", "status"}).AddRow(output...))

	info, err := store(db).QueryMovements(t.Context(),
		driver.QueryMovementsParams{
			EnrollmentIDs:     []string{record.EnrollmentID},
			TokenTypes:        []token.Type{record.TokenType},
			TxStatuses:        []driver.TxStatus{record.Status},
			MovementDirection: driver.Sent,
			NumRecords:        1,
		})

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(info).To(gomega.ConsistOf(&record))
}

func TestQueryTransactions(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	record := driver.TransactionRecord{
		TxID:         "1234",
		ActionType:   driver.Transfer,
		SenderEID:    "alice",
		RecipientEID: "bob",
		TokenType:    token.Type("USD"),
		Amount:       big.NewInt(100),
		Status:       driver.Deleted,
	}
	output := []driver2.Value{
		record.TxID, record.ActionType, record.SenderEID, record.RecipientEID, record.TokenType, int(record.Amount.Int64()), record.Status, nil, nil, time.Time{},
	}
	mockDB.
		ExpectQuery("SELECT TRANSACTIONS.tx_id, action_type, sender_eid, recipient_eid, token_type, amount, " +
			"REQUESTS.status, REQUESTS.application_metadata, REQUESTS.public_metadata, TRANSACTIONS.stored_at " +
			"FROM TRANSACTIONS LEFT JOIN REQUESTS ON TRANSACTIONS.tx_id = REQUESTS.tx_id ORDER BY TRANSACTIONS.stored_at DESC").
		WillReturnRows(mockDB.NewRows([]string{"tx_id", "action_type", "sender_eid", "recipient_eid", "token_type", "amount", "status", "application_metadata", "public_metadata", "stored_at"}).AddRow(output...))

	info, err := store(db).QueryTransactions(t.Context(),
		driver.QueryTransactionsParams{
			IDs: []string{},
		}, pagination.None())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	records, err := iterators.ReadAllValues(info.Items)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(records).To(gomega.ConsistOf(record))
}

func TestGetStatus(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := "1234"
	output := []driver2.Value{3, "some_message"}

	mockDB.
		ExpectQuery("SELECT status, status_message FROM REQUESTS WHERE tx_id = \\$1").
		WithArgs(input).
		WillReturnRows(mockDB.NewRows([]string{"status", "status_message"}).AddRow(output...))

	status, statusMessage, err := store(db).GetStatus(t.Context(), input)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(status).To(gomega.Equal(output[0]))
	gomega.Expect(statusMessage).To(gomega.Equal(output[1]))
}

func TestQueryTokenRequests(t *testing.T, store transactionsStoreConstructor, traits QueryConstructorTraits) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	record := driver.TokenRequestRecord{
		TxID:         "1234",
		TokenRequest: []byte("some request"),
		Status:       driver.Deleted,
	}
	output := []driver2.Value{
		record.TxID, record.TokenRequest, record.Status,
	}
	var statusClause string
	if traits.SupportsIN {
		statusClause = "\\(status\\) IN \\(\\(\\$1\\), \\(\\$2\\)\\)"
	} else {
		statusClause = "\\(\\(status = \\$1\\)\\) OR \\(\\(status = \\$2\\)\\)"
	}
	mockDB.
		ExpectQuery("SELECT tx_id, request, status FROM REQUESTS WHERE "+statusClause).
		WithArgs(driver.Deleted, driver.Unknown).
		WillReturnRows(mockDB.NewRows([]string{"tx_id", "request", "status"}).AddRow(output...))

	it, err := store(db).QueryTokenRequests(t.Context(),
		driver.QueryTokenRequestsParams{
			Statuses: []driver.TxStatus{driver.Deleted, driver.Unknown},
		},
	)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	records, err := iterators.ReadAllValues[driver.TokenRequestRecord](it)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(records).To(gomega.ConsistOf(record))
}

func TestGetTransactionEndorsementAcks(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	record := struct {
		endorser string
		sigma    []byte
	}{
		endorser: "auditor",
		sigma:    []byte("5678"),
	}
	inputID := "1234"
	output := []driver2.Value{record.endorser, record.sigma}

	mockDB.
		ExpectQuery("SELECT endorser, sigma FROM TRANSACTION_ENDORSE_ACK WHERE tx_id = \\$1").
		WithArgs(inputID).
		WillReturnRows(mockDB.NewRows([]string{"endorser", "sigma"}).AddRow(output...))

	acks, err := store(db).GetTransactionEndorsementAcks(t.Context(), inputID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(acks).To(gomega.HaveLen(1))
	gomega.Expect(acks).To(gomega.HaveKeyWithValue(token2.Identity(record.endorser).UniqueID(), record.sigma))
}

func TestAddTransactionEndorsementAck(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	uuid := sqlmock.AnyArg()
	txID := "1234"
	eID := token2.Identity([]byte("5678"))
	sigma := []byte("signature")
	now := sqlmock.AnyArg()

	mockDB.ExpectExec("INSERT INTO TRANSACTION_ENDORSE_ACK \\(id, tx_id, endorser, sigma, stored_at\\) VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5\\)").
		WithArgs(uuid, txID, eID, sigma, now).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store(db).AddTransactionEndorsementAck(t.Context(), txID, eID, sigma)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestSetStatus(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "1234"
	status := driver.Confirmed
	message := "message"

	mockDB.ExpectExec("UPDATE REQUESTS SET status = \\$1, status_message = \\$2 WHERE tx_id = \\$3").
		WithArgs(status, message, txID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store(db).SetStatus(t.Context(), txID, status, message)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

type AnyUUID struct{}

func (a AnyUUID) Match(v driver2.Value) bool {
	_, ok := v.(string)

	return ok
}

func TestAWAddTransaction(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := driver.TransactionRecord{
		TxID:         "txid",
		ActionType:   driver.Transfer,
		SenderEID:    "sender",
		RecipientEID: "recipient",
		TokenType:    "USD",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now(),
	}

	mockDB.ExpectBegin()
	mockDB.
		ExpectExec("INSERT INTO TRANSACTIONS \\(id, tx_id, action_type, sender_eid, recipient_eid, token_type, amount, stored_at\\) VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7, \\$8\\)").
		WithArgs(AnyUUID{}, input.TxID, 1, input.SenderEID, input.RecipientEID, input.TokenType, input.Amount.String(), input.Timestamp.UTC()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewTransactionStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddTransaction(t.Context(), input)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestAWAddTokenRequest(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	txID := "txid"
	tr := []byte("1234")
	ppHash := []byte("5678")
	status := driver.Pending
	status_message := ""

	mockDB.ExpectBegin()
	mockDB.
		ExpectExec("INSERT INTO REQUESTS \\(tx_id, request, status, status_message, application_metadata, public_metadata, pp_hash, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6, \\$7, \\$8\\)").
		WithArgs(txID, tr, status, status_message, "{}", "{}", ppHash, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewTransactionStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddTokenRequest(t.Context(), txID, tr, nil, nil, ppHash)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

func TestAWAddMovement(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := driver.MovementRecord{
		TxID:         "txid",
		EnrollmentID: "EID",
		TokenType:    "USD",
		Amount:       big.NewInt(10),
		Status:       driver.Pending,
	}
	now := sqlmock.AnyArg()

	mockDB.ExpectBegin()
	mockDB.
		ExpectExec("INSERT INTO MOVEMENTS \\(id, tx_id, enrollment_id, token_type, amount, stored_at\\) "+
			"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6\\)").
		WithArgs(AnyUUID{}, input.TxID, input.EnrollmentID, input.TokenType, input.Amount.String(), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockDB.ExpectCommit()

	aw, err := store(db).NewTransactionStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(aw.AddMovement(t.Context(), input)).To(gomega.Succeed())
	gomega.Expect(aw.Commit()).To(gomega.Succeed())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}

// TestGetStatusContextCancelled verifies that a cancelled context is propagated from
// QueryContext back to the caller of GetStatus as a context error.
func TestGetStatusContextCancelled(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	// The mock delays the query by 1 s; the context expires after 10 ms.
	mockDB.
		ExpectQuery("SELECT status, status_message FROM REQUESTS WHERE tx_id = \\$1").
		WithArgs("1234").
		WillDelayFor(time.Second).
		WillReturnRows(mockDB.NewRows([]string{"status", "status_message"}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err = store(db).GetStatus(ctx, "1234")

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}

// TestAddTransactionEndorsementAckContextCancelled verifies that a cancelled context
// is propagated from ExecContext back to the caller of AddTransactionEndorsementAck.
func TestAddTransactionEndorsementAckContextCancelled(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	mockDB.
		ExpectExec("INSERT INTO TRANSACTION_ENDORSE_ACK").
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(1, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = store(db).AddTransactionEndorsementAck(ctx, "txid", token2.Identity([]byte("endorser")), []byte("sigma"))

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}

// TestSetStatusContextCancelled verifies that a cancelled context is propagated from
// ExecContext back to the caller of SetStatus as a context error.
func TestSetStatusContextCancelled(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	mockDB.
		ExpectExec("UPDATE REQUESTS SET status = \\$1, status_message = \\$2 WHERE tx_id = \\$3").
		WithArgs(driver.Confirmed, "message", "txid").
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(1, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = store(db).SetStatus(ctx, "txid", driver.Confirmed, "message")

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}

// TestAWAddTransactionContextCancelled verifies that a cancelled context is propagated
// from ExecContext through AddTransaction inside a TransactionStoreTransaction.
func TestAWAddTransactionContextCancelled(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := driver.TransactionRecord{
		TxID:         "txid",
		ActionType:   driver.Transfer,
		SenderEID:    "sender",
		RecipientEID: "recipient",
		TokenType:    "USD",
		Amount:       big.NewInt(10),
		Timestamp:    time.Now(),
	}

	mockDB.ExpectBegin()
	mockDB.
		ExpectExec("INSERT INTO TRANSACTIONS").
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	aw, err := store(db).NewTransactionStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	err = aw.AddTransaction(ctx, input)

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}

// TestAWAddTokenRequestContextCancelled verifies that a cancelled context is propagated
// from ExecContext through AddTokenRequest inside a TransactionStoreTransaction.
func TestAWAddTokenRequestContextCancelled(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	mockDB.ExpectBegin()
	mockDB.
		ExpectExec("INSERT INTO REQUESTS").
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	aw, err := store(db).NewTransactionStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	err = aw.AddTokenRequest(ctx, "txid", []byte("tr"), nil, nil, []byte("pphash"))

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}

// TestAWAddMovementContextCancelled verifies that a cancelled context is propagated
// from ExecContext through AddMovement inside a TransactionStoreTransaction.
func TestAWAddMovementContextCancelled(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	input := driver.MovementRecord{
		TxID:         "txid",
		EnrollmentID: "EID",
		TokenType:    "USD",
		Amount:       big.NewInt(10),
		Status:       driver.Pending,
	}

	mockDB.ExpectBegin()
	mockDB.
		ExpectExec("INSERT INTO MOVEMENTS").
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	aw, err := store(db).NewTransactionStoreTransaction()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	err = aw.AddMovement(ctx, input)

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}
