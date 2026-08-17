/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorserdb

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errQueryFailed is a sentinel standing in for a driver-level cause a caller may want to match,
// such as sql.ErrNoRows or a driver sentinel error.
var errQueryFailed = errors.New("driver query failed")

// failingEndorserStore is an EndorserStore whose QueryValidations always fails with
// errQueryFailed.
type failingEndorserStore struct{}

func (failingEndorserStore) Close() error { return nil }

func (failingEndorserStore) NewEndorserStoreTransaction() (dbdriver.EndorserStoreTransaction, error) {
	return nil, errQueryFailed
}

func (failingEndorserStore) QueryValidations(context.Context, dbdriver.QueryValidationRecordsParams) (dbdriver.ValidationRecordsIterator, error) {
	return nil, errQueryFailed
}

func (failingEndorserStore) GetStatus(context.Context, string) (dbdriver.TxStatus, string, error) {
	return dbdriver.Unknown, "", errQueryFailed
}

// TestValidationRecordsPreservesErrorChain checks that a driver failure surfaced by
// ValidationRecords stays matchable with errors.Is. Building the error by interpolating the cause
// into a new message (errors.Errorf with %s) would break every caller that switches on the
// underlying cause.
func TestValidationRecordsPreservesErrorChain(t *testing.T) {
	store := &StoreService{
		StatusSupport: common.NewStatusSupport(),
		db:            failingEndorserStore{},
	}

	it, err := store.ValidationRecords(t.Context(), QueryValidationRecordsParams{})
	require.Error(t, err)
	assert.Nil(t, it)
	require.ErrorIs(t, err, errQueryFailed, "the driver cause must remain unwrappable")
	assert.Contains(t, err.Error(), "failed to query validation records")
}
