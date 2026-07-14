/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorserdb

import (
	"context"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o mock/endorser_store_service_manager.go --fake-name EndorserStoreServiceManager . StoreServiceManager

type StoreServiceManager db.StoreServiceManager[*StoreService]

var (
	managerType = reflect.TypeFor[*StoreServiceManager]()
	logger      = logging.MustGetLogger()
)

func NewStoreServiceManager(cp db.ConfigService, drivers multiplexed.Driver) StoreServiceManager {
	return db.NewStoreServiceManager(cp, "endorserdb.persistence", drivers.NewEndorser, newStoreService)
}

func GetByTMSId(sp token.ServiceProvider, tmsID token.TMSID) (*StoreService, error) {
	s, err := sp.GetService(managerType)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get manager service")
	}
	c, err := s.(StoreServiceManager).StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get db for tms [%s]", tmsID)
	}

	return c, nil
}

// TxStatus is the status of a transaction
type TxStatus = dbdriver.TxStatus

const (
	// Unknown is the status of a transaction that is unknown
	Unknown = dbdriver.Unknown
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending = dbdriver.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed = dbdriver.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted = dbdriver.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan = dbdriver.Orphan
)

// TxStatusMessage maps TxStatus to string
var TxStatusMessage = dbdriver.TxStatusMessage

// ValidationRecord is a record that contains information about the validation of a given token request
type ValidationRecord = dbdriver.ValidationRecord

// ValidationRecordsIterator is an iterator over validation records
type ValidationRecordsIterator struct {
	it dbdriver.ValidationRecordsIterator
}

// Close closes the iterator. It must be called when done with the iterator.
func (t *ValidationRecordsIterator) Close() {
	t.it.Close()
}

// Next returns the next validation record, if any.
// It returns nil, nil if there are no more records.
func (t *ValidationRecordsIterator) Next() (*ValidationRecord, error) {
	next, err := t.it.Next()
	if err != nil {
		return nil, err
	}
	if next == nil {
		return nil, nil
	}

	return next, nil
}

// QueryValidationRecordsParams defines the parameters for querying validation records
type QueryValidationRecordsParams = dbdriver.QueryValidationRecordsParams

// StoreService is a database that stores token transaction endorsement validation records
type StoreService struct {
	*common.StatusSupport
	db dbdriver.EndorserStore
}

func newStoreService(p dbdriver.EndorserStore) (*StoreService, error) {
	return &StoreService{
		StatusSupport: common.NewStatusSupport(),
		db:            p,
	}, nil
}

// ValidationRecords returns an iterator of validation records filtered by the given params.
func (d *StoreService) ValidationRecords(ctx context.Context, params QueryValidationRecordsParams) (*ValidationRecordsIterator, error) {
	it, err := d.db.QueryValidations(ctx, params)
	if err != nil {
		return nil, errors.Errorf("failed to query validation records: %s", err)
	}

	return &ValidationRecordsIterator{it: it}, nil
}

// AppendValidationRecord appends the given validation metadata related to the given transaction id
func (d *StoreService) AppendValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash driver2.PPHash) error {
	logger.DebugfContext(ctx, "appending new validation record... [%s]", txID)

	w, err := d.db.NewEndorserStoreTransaction()
	if err != nil {
		return errors.WithMessagef(err, "begin update for txid [%s] failed", txID)
	}
	// Store the token request directly in the validation record
	if err := w.AddValidationRecord(ctx, txID, tokenRequest, meta, ppHash); err != nil {
		w.Rollback()

		return errors.WithMessagef(err, "append validation record for txid [%s] failed", txID)
	}
	if err := w.Commit(); err != nil {
		return errors.WithMessagef(err, "append validation record commit for txid [%s] failed", txID)
	}
	logger.DebugfContext(ctx, "appending validation record completed without errors")

	return nil
}

// SetStatus sets the status of the validation record with the passed transaction id to the passed status
func (d *StoreService) SetStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) error {
	logger.DebugfContext(ctx, "set status [%s][%s]...", txID, status)
	w, err := d.db.NewEndorserStoreTransaction()
	if err != nil {
		return errors.WithMessagef(err, "begin update for txid [%s] failed", txID)
	}
	if err := w.SetStatus(ctx, txID, status, message); err != nil {
		w.Rollback()

		return errors.Wrapf(err, "failed setting status [%s][%s]", txID, dbdriver.TxStatusMessage[status])
	}
	if err := w.Commit(); err != nil {
		w.Rollback()

		return errors.Wrapf(err, "failed committing status [%s][%s]", txID, dbdriver.TxStatusMessage[status])
	}

	// notify the listeners
	d.Notify(common.StatusEvent{
		Ctx:            ctx,
		TxID:           txID,
		ValidationCode: status,
	})
	logger.DebugfContext(ctx, "set status [%s][%s] done", txID, dbdriver.TxStatusMessage[status])

	return nil
}

// GetStatus return the status of the given transaction id.
// It returns an error if no transaction with that id is found
func (d *StoreService) GetStatus(ctx context.Context, txID string) (TxStatus, string, error) {
	logger.DebugfContext(ctx, "get status [%s]...", txID)
	status, message, err := d.db.GetStatus(ctx, txID)
	if err != nil {
		return Unknown, "", errors.Wrapf(err, "failed getting status [%s]", txID)
	}
	logger.DebugfContext(ctx, "got status [%s][%s]", txID, status)

	return status, message, nil
}
