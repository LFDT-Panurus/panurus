/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	qcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	dcommon "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
)

type keystoreTables struct {
	KeyStore string
}

type KeystoreStore struct {
	readDB       *sql.DB
	writeDB      *sql.DB
	errorWrapper driver.SQLErrorWrapper
	table        keystoreTables
	ci           qcommon.CondInterpreter
}

func newKeystoreStore(readDB, writeDB *sql.DB, tables keystoreTables, ci qcommon.CondInterpreter, errorWrapper driver.SQLErrorWrapper) *KeystoreStore {
	return &KeystoreStore{
		readDB:       readDB,
		writeDB:      writeDB,
		table:        tables,
		ci:           ci,
		errorWrapper: errorWrapper,
	}
}

func NewKeystoreStore(readDB, writeDB *sql.DB, tables TableNames, ci qcommon.CondInterpreter, errorWrapper driver.SQLErrorWrapper) (*KeystoreStore, error) {
	return newKeystoreStore(
		readDB,
		writeDB,
		keystoreTables{
			KeyStore: tables.KeyStore,
		},
		ci,
		errorWrapper,
	), nil
}

func (db *KeystoreStore) CreateSchema() error {
	return common.InitSchema(db.writeDB, db.GetSchema())
}

func (db *KeystoreStore) Close() error {
	return dcommon.Close(db.readDB, db.writeDB)
}

func (db *KeystoreStore) Put(key string, state any) error {
	if state == nil {
		return errors.New("cannot store nil state")
	}
	if len(key) == 0 {
		return errors.New("cannot store empty key")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return errors.Wrapf(err, "cannot marshal state with key [%s]", key)
	}
	query, args := q.InsertInto(db.table.KeyStore).
		Fields("key", "val").
		Row(key, raw).
		Format()
	logging.Debug(logger, query, args)

	_, err = db.writeDB.Exec(query, args...)
	if err != nil && errors.HasCause(db.errorWrapper.WrapError(err), driver.UniqueKeyViolation) {
		// then check that raw is equal to what is stored
		rawFromDB, getErr := db.GetRaw(key)
		if getErr != nil {
			return errors.Wrapf(getErr, "key [%s] exists already but its stored value could not be read back", key)
		}
		if rawFromDB == nil {
			// The insert hit the unique constraint, so the row exists on the
			// write side, yet the read-back found nothing. GetRaw maps a missing
			// row to (nil, nil) - the zero value, never scanned - and it reads
			// from the read DB, which for Postgres may be a replica that has not
			// caught up yet. That is "could not read it back", not "the value
			// differs": reporting a mismatch here would fail an idempotent
			// replay (a restart re-Putting the identical value) with a bogus
			// integrity error.
			//
			// The test is against nil rather than len() == 0 on purpose. A row
			// that is present but holds an empty value scans into a non-nil
			// empty slice, and that is a genuine mismatch against any marshalled
			// state (json.Marshal never yields empty), so it must fall through
			// to the comparison below instead of being reported as unreadable.
			return errors.Errorf("key [%s] exists already but its stored value is not visible on the read connection", key)
		}
		if bytes.Equal(rawFromDB, raw) {
			// It might be that this key was already inserted before. The node is restarting, for example.

			return nil
		}

		// A genuine conflict: the key holds a different value. This must be a
		// fresh error - wrapping the (now nil) read-back error would yield nil
		// and report success.
		return errors.Errorf("key [%s] exists already and the value does not match", key)
	}

	return err
}

func (db *KeystoreStore) Get(key string, state any) error {
	raw, err := db.GetRaw(key)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.Errorf("key [%s] does not exist", key)
	}
	if err := json.Unmarshal(raw, state); err != nil {
		return errors.Wrapf(err, "failed retrieving key [%s], cannot unmarshal state", key)
	}

	logger.Debugf("got key [%s] successfully", key)

	return nil
}

func (db *KeystoreStore) GetRaw(key string) ([]byte, error) {
	query, args := q.Select().
		FieldsByName("val").
		From(q.Table(db.table.KeyStore)).
		Where(cond.Eq("key", key)).
		Format(db.ci)
	raw, err := common.QueryUniqueContext[[]byte](context.Background(), db.readDB, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed retrieving key [%s]", key)
	}

	return raw, nil
}

func (db *KeystoreStore) Delete(key string) error {
	if len(key) == 0 {
		return errors.New("cannot delete empty key")
	}

	query, args := q.DeleteFrom(db.table.KeyStore).
		Where(cond.Eq("key", key)).
		Format(db.ci)
	logging.Debug(logger, query, args)

	_, err := db.writeDB.Exec(query, args...)
	if err != nil {
		return errors.Wrapf(err, "failed deleting key [%s]", key)
	}

	logger.Debugf("deleted key [%s] successfully", key)

	return nil
}

func (db *KeystoreStore) GetSchema() string {
	return fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			key TEXT NOT NULL,
			val BYTEA NOT NULL,
			PRIMARY KEY (key)
		);
		`,
		db.table.KeyStore,
	)
}
