/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"context"

	tokendriver "github.com/LFDT-Panurus/panurus/token/driver"
	driver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// WrapEndorser wraps an endorser store with the guard policy.
func WrapEndorser(s driver.EndorserStore, p Policy) driver.EndorserStore {
	if s == nil {
		return s
	}

	return &guardedEndorserStore{EndorserStore: s, policy: p}
}

type guardedEndorserStore struct {
	driver.EndorserStore
	policy Policy
}

// QueryValidations is intentionally not row-capped and delegates to the embedded
// store unchanged: QueryValidationRecordsParams carries no page-size or cursor,
// so a caller that hits a cap has no way to page around it. Bounding this read
// needs a SQL-level LIMIT on the query itself (tracked follow-up), not a wrapper.

func (g *guardedEndorserStore) NewEndorserStoreTransaction() (driver.EndorserStoreTransaction, error) {
	w, err := g.EndorserStore.NewEndorserStoreTransaction()
	if err != nil {
		return nil, err
	}

	return &guardedEndorserStoreTx{EndorserStoreTransaction: w, policy: g.policy}, nil
}

type guardedEndorserStoreTx struct {
	driver.EndorserStoreTransaction
	policy Policy
}

func (w *guardedEndorserStoreTx) AddValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash tokendriver.PPHash) error {
	size := len(txID) + len(tokenRequest) + BlobMapSize(meta)
	if err := CheckPayload("AddValidationRecord", size, w.policy.MaxPayloadSize); err != nil {
		return err
	}

	return w.EndorserStoreTransaction.AddValidationRecord(ctx, txID, tokenRequest, meta, ppHash)
}
