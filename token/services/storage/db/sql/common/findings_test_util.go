/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/onsi/gomega"
)

// TestCountOpenFindings verifies the aggregate query groups open findings by
// severity and excludes resolved ones, and that the rows scan into the
// returned map keyed by the raw severity value.
func TestCountOpenFindings(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	query := "SELECT severity, COUNT\\(\\*\\) FROM FINDINGS WHERE resolved_at IS NULL GROUP BY severity"
	mockDB.
		ExpectQuery(query).
		WillReturnRows(mockDB.NewRows([]string{"severity", "count"}).
			AddRow(1, 2).
			AddRow(2, 5))

	counts, err := store(db).CountOpenFindings(t.Context())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(counts).To(gomega.Equal(map[int]int64{1: 2, 2: 5}))
}

// TestCountOpenFindings_NoOpenFindings verifies an empty result set (nothing
// open) returns an empty, non-nil map rather than an error.
func TestCountOpenFindings_NoOpenFindings(t *testing.T, store transactionsStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	query := "SELECT severity, COUNT\\(\\*\\) FROM FINDINGS WHERE resolved_at IS NULL GROUP BY severity"
	mockDB.
		ExpectQuery(query).
		WillReturnRows(mockDB.NewRows([]string{"severity", "count"}))

	counts, err := store(db).CountOpenFindings(t.Context())

	gomega.Expect(mockDB.ExpectationsWereMet()).To(gomega.Succeed())
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	gomega.Expect(counts).To(gomega.Equal(map[int]int64{}))
}
