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

type tokenStoreConstructor func(*sql.DB) *TokenStore

// TestDeleteTokensContextCancelled verifies that a cancelled context is propagated
// from ExecContext back to the caller of DeleteTokens as a context error.
func TestDeleteTokensContextCancelled(t *testing.T, store tokenStoreConstructor) {
	gomega.RegisterTestingT(t)
	db, mockDB, err := sqlmock.New()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	ids := []*token.ID{{TxId: "tx1", Index: 0}}

	// The mock delays the UPDATE by 1 s; the context expires after 10 ms.
	mockDB.
		ExpectExec("UPDATE").
		WillDelayFor(time.Second).
		WillReturnResult(sqlmock.NewResult(0, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = store(db).DeleteTokens(ctx, "spender", ids...)

	gomega.Expect(fscerrors.Is(err, sqlmock.ErrCancelled)).To(gomega.BeTrue(),
		"expected cancellation error, got: %v", err)
}
