/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// WalkTransactionsForTest runs walkTransactions over db with the passed page size.
func WalkTransactionsForTest(ctx context.Context, db TokenTransactionDB, pageSize int, params driver.QueryTransactionsParams, fn func(*driver.TransactionRecord) error) error {
	checkers := newDefaultCheckers(nil, nil, db, nil, token.TMSID{}, WithMaxPageSize(pageSize))

	return checkers.walkTransactions(ctx, token.TMSID{}, params, fn)
}
