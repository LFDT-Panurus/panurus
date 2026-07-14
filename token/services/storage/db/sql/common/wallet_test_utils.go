/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/onsi/gomega"
)

type walletStoreConstructor func(*sql.DB) *WalletStore

func TestGetWalletID(t *testing.T, store walletStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.Identity([]byte("1234"))
	roleID := 5
	output := driver.WalletID("my wallet")
	mockDB.
		ExpectQuery("SELECT wallet_id FROM WALLETS WHERE \\(identity_hash = \\$1\\) AND \\(role_id = \\$2\\)").
		WithArgs(tokenID.UniqueID(), roleID).
		WillReturnRows(mockDB.NewRows([]string{"request"}).AddRow(output))

	actualWalletID, err := store(db).GetWalletID(t.Context(), tokenID, roleID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(actualWalletID).To(gomega.Equal(output))
}

func TestGetWalletIDs(t *testing.T, store walletStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	roleID := 5
	output := driver.WalletID("my wallet")
	mockDB.
		ExpectQuery("SELECT DISTINCT wallet_id FROM WALLETS WHERE role_id = \\$1").
		WithArgs(roleID).
		WillReturnRows(mockDB.NewRows([]string{"wallet_id"}).AddRow(output))

	actualWalletIDs, err := store(db).GetWalletIDs(t.Context(), roleID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(actualWalletIDs).To(gomega.ConsistOf(output))
}

func TestLoadMeta(t *testing.T, store walletStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.Identity([]byte("1234"))
	roleID := 5
	walletID := driver.WalletID("my wallet")
	output := []byte("some meta data")
	mockDB.
		ExpectQuery("SELECT meta FROM WALLETS WHERE \\(identity_hash = \\$1\\) AND \\(wallet_id = \\$2\\) AND \\(role_id = \\$3\\)").
		WithArgs(tokenID.UniqueID(), walletID, roleID).
		WillReturnRows(mockDB.NewRows([]string{"meta"}).AddRow(output))

	actual, err := store(db).LoadMeta(t.Context(), tokenID, walletID, roleID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(actual).To(gomega.Equal(output))
}

func TestIdentityExists(t *testing.T, store walletStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.Identity([]byte("1234"))
	roleID := 5
	walletID := driver.WalletID("my wallet")
	mockDB.
		ExpectQuery("SELECT wallet_id FROM WALLETS WHERE \\(identity_hash = \\$1\\) AND \\(wallet_id = \\$2\\) AND \\(role_id = \\$3\\)").
		WithArgs(tokenID.UniqueID(), walletID, roleID).
		WillReturnRows(mockDB.NewRows([]string{"wallet_id"}).AddRow(walletID))

	exists := store(db).IdentityExists(t.Context(), tokenID, walletID, roleID)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(exists).To(gomega.BeTrue())
}

func TestStoreIdentity(t *testing.T, store walletStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.Identity([]byte("1234"))
	eID := "5678"
	walletID := driver.WalletID("my wallet")
	roleID := 5

	mockDB.ExpectExec("INSERT INTO WALLETS "+
		"\\(identity_hash, meta, wallet_id, role_id, created_at, enrollment_id\\) "+
		"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6\\) ON CONFLICT DO NOTHING").
		WithArgs(tokenID.UniqueID(), []uint8(nil), walletID, roleID, sqlmock.AnyArg(), eID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store(db).StoreIdentity(t.Context(), tokenID, eID, walletID, roleID, nil)

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
}

func TestStoreIdentityIdempotent(t *testing.T, store walletStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	tokenID := token.Identity([]byte("1234"))
	eID := "5678"
	walletID := driver.WalletID("my wallet")
	roleID := 5

	insertQuery := "INSERT INTO WALLETS " +
		"\\(identity_hash, meta, wallet_id, role_id, created_at, enrollment_id\\) " +
		"VALUES \\(\\$1, \\$2, \\$3, \\$4, \\$5, \\$6\\) ON CONFLICT DO NOTHING"

	// First call: row inserted (1 row affected)
	mockDB.ExpectExec(insertQuery).
		WithArgs(tokenID.UniqueID(), []uint8(nil), walletID, roleID, sqlmock.AnyArg(), eID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Second call: conflict, 0 rows affected — must still return nil
	mockDB.ExpectExec(insertQuery).
		WithArgs(tokenID.UniqueID(), []uint8(nil), walletID, roleID, sqlmock.AnyArg(), eID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	s := store(db)
	err = s.StoreIdentity(t.Context(), tokenID, eID, walletID, roleID, nil)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	err = s.StoreIdentity(t.Context(), tokenID, eID, walletID, roleID, nil)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
}
