/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	cache2 "github.com/LFDT-Panurus/panurus/token/services/utils/cache"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/cache/secondcache"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type cache[T any] interface {
	Get(key string) (T, bool)
	GetOrLoad(key string, loader func() (T, error)) (T, bool, error)
	Add(key string, value T)
	Delete(key string)
}

type dbTransaction interface {
	ExecContext(ctx context.Context, query string, args ...common3.Param) (sql.Result, error)
}

type identityTables struct {
	IdentityConfigurations string
	IdentityInfo           string
	Signers                string
}

// IdentityStore is a SQL-backed implementation of the IdentityStore interface.
type IdentityStore struct {
	readDB   *sql.DB
	writeDB  *sql.DB
	table    identityTables
	ci       common3.CondInterpreter
	notifier idriver.IdentityConfigurationNotifier

	signerInfoCache cache[bool]
	auditInfoCache  cache[[]byte]
	errorWrapper    driver2.SQLErrorWrapper
}

func newIdentityStore(
	readDB, writeDB *sql.DB,
	tables identityTables,
	singerInfoCache cache[bool],
	auditInfoCache cache[[]byte],
	ci common3.CondInterpreter,
	errorWrapper driver2.SQLErrorWrapper,
	notifier idriver.IdentityConfigurationNotifier,
) *IdentityStore {
	return &IdentityStore{
		readDB:          readDB,
		writeDB:         writeDB,
		table:           tables,
		signerInfoCache: singerInfoCache,
		auditInfoCache:  auditInfoCache,
		ci:              ci,
		errorWrapper:    errorWrapper,
		notifier:        notifier,
	}
}

func NewCachedIdentityStore(
	readDB, writeDB *sql.DB,
	tables TableNames,
	ci common3.CondInterpreter,
	errorWrapper driver2.SQLErrorWrapper,
) (*IdentityStore, error) {
	return NewIdentityStore(
		readDB,
		writeDB,
		tables,
		secondcache.NewTyped[bool](5000),
		secondcache.NewTyped[[]byte](5000),
		ci,
		errorWrapper,
	)
}

func NewNoCacheIdentityStore(
	readDB, writeDB *sql.DB,
	tables TableNames,
	ci common3.CondInterpreter,
	errorWrapper driver2.SQLErrorWrapper,
) (*IdentityStore, error) {
	return NewIdentityStore(
		readDB,
		writeDB,
		tables,
		cache2.NewNoCache[bool](),
		cache2.NewNoCache[[]byte](),
		ci,
		errorWrapper,
	)
}

func NewIdentityStore(
	readDB, writeDB *sql.DB,
	tables TableNames,
	signerInfoCache cache[bool],
	auditInfoCache cache[[]byte],
	ci common3.CondInterpreter,
	errorWrapper driver2.SQLErrorWrapper,
) (*IdentityStore, error) {
	return newIdentityStore(
		readDB,
		writeDB,
		identityTables{
			IdentityConfigurations: tables.IdentityConfigurations,
			IdentityInfo:           tables.IdentityInfo,
			Signers:                tables.Signers,
		},
		signerInfoCache,
		auditInfoCache,
		ci,
		errorWrapper,
		nil,
	), nil
}

// NewIdentityStoreWithNotifier creates a new IdentityStore with a notifier.
func NewIdentityStoreWithNotifier(
	readDB, writeDB *sql.DB,
	tables TableNames,
	signerInfoCache cache[bool],
	auditInfoCache cache[[]byte],
	ci common3.CondInterpreter,
	errorWrapper driver2.SQLErrorWrapper,
	notifier idriver.IdentityConfigurationNotifier,
) (*IdentityStore, error) {
	return newIdentityStore(
		readDB,
		writeDB,
		identityTables{
			IdentityConfigurations: tables.IdentityConfigurations,
			IdentityInfo:           tables.IdentityInfo,
			Signers:                tables.Signers,
		},
		signerInfoCache,
		auditInfoCache,
		ci,
		errorWrapper,
		notifier,
	), nil
}

func (db *IdentityStore) CreateSchema() error {
	return common.InitSchema(db.writeDB, []string{db.GetSchema()}...)
}

// AddConfiguration stores an identity configuration in the database.
// It also enqueues an event to the notifier if available.
func (db *IdentityStore) AddConfiguration(ctx context.Context, wp driver.IdentityConfiguration) error {
	query, args := q.InsertInto(db.table.IdentityConfigurations).
		Fields("id", "type", "url", "conf", "raw").
		Row(wp.ID, wp.Type, wp.URL, wp.Config, wp.Raw).
		Format()
	logging.Debug(logger, query, args)

	_, err := db.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}

	return nil
}

// GetConfiguration returns the configuration with the given id, type, and url.
func (db *IdentityStore) GetConfiguration(ctx context.Context, id, typ, url string) (*driver.IdentityConfiguration, error) {
	query, args := q.Select().
		FieldsByName("id", "type", "url", "conf", "raw").
		From(q.Table(db.table.IdentityConfigurations)).
		Where(cond.And(cond.Eq("id", id), cond.Eq("type", typ), cond.Eq("url", url))).
		Format(db.ci)
	logging.Debug(logger, query, args)
	row := db.readDB.QueryRowContext(ctx, query, args...)
	c := &driver.IdentityConfiguration{}
	err := row.Scan(&c.ID, &c.Type, &c.URL, &c.Config, &c.Raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return c, nil
}

// IteratorConfigurations returns an iterator to all configurations of the given type.
func (db *IdentityStore) IteratorConfigurations(ctx context.Context, configurationType string) (idriver.IdentityConfigurationIterator, error) {
	query, args := q.Select().
		FieldsByName("id", "url", "conf", "raw").
		From(q.Table(db.table.IdentityConfigurations)).
		Where(cond.Eq("type", configurationType)).
		Format(db.ci)
	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	return common.NewIterator(rows, func(c *driver.IdentityConfiguration) error {
		c.Type = configurationType

		return rows.Scan(&c.ID, &c.URL, &c.Config, &c.Raw)
	}), nil
}

func (db *IdentityStore) ConfigurationExists(ctx context.Context, id, typ, url string) (bool, error) {
	query, args := q.Select().
		FieldsByName("id").
		From(q.Table(db.table.IdentityConfigurations)).
		Where(cond.And(cond.Eq("id", id), cond.Eq("type", typ), cond.Eq("url", url))).
		Format(db.ci)
	result, err := common.QueryUniqueContext[string](ctx, db.readDB, query, args...)
	if err != nil {
		return false, errors.Wrapf(err, "failed getting configuration for [%s:%s:%s]", id, typ, url)
	}
	logger.DebugfContext(ctx, "found configuration for [%s:%s:%s]", id, typ, url)

	return len(result) != 0, nil
}

// Notifier returns the IdentityNotifier associated with this store.
func (db *IdentityStore) Notifier() (idriver.IdentityConfigurationNotifier, error) {
	if db.notifier == nil {
		return nil, storage.ErrNotSupported
	}

	return db.notifier, nil
}

func (db *IdentityStore) StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error {
	return db.storeIdentityData(ctx, db.writeDB, tdriver.Identity(id).UniqueID(), id, identityAudit, tokenMetadata, tokenMetadataAudit, true)
}

func (db *IdentityStore) GetAuditInfo(ctx context.Context, id []byte) ([]byte, error) {
	h := token.Identity(id).String()
	logger.DebugfContext(ctx, "get audit info for [%s]", h)

	value, _, err := db.auditInfoCache.GetOrLoad(h, func() ([]byte, error) {
		logger.DebugfContext(ctx, "load from backend identity data for [%s]", view.Identity(id))
		query, args := q.Select().
			FieldsByName("identity_audit_info").
			From(q.Table(db.table.IdentityInfo)).
			Where(cond.Eq("identity_hash", h)).
			Format(db.ci)

		return common.QueryUniqueContext[[]byte](ctx, db.readDB, query, args...)
	})

	return value, err
}

func (db *IdentityStore) GetTokenInfo(ctx context.Context, id []byte) ([]byte, []byte, error) {
	h := token.Identity(id).String()
	logger.DebugfContext(ctx, "get identity data for [%s]", h)

	query, args := q.Select().
		FieldsByName("token_metadata", "token_metadata_audit_info").
		From(q.Table(db.table.IdentityInfo)).
		Where(cond.Eq("identity_hash", h)).
		Format(db.ci)
	logging.Debug(logger, query, args)

	row := db.readDB.QueryRowContext(ctx, query, args...)
	var tokenMetadata []byte
	var tokenMetadataAuditInfo []byte
	err := row.Scan(&tokenMetadata, &tokenMetadataAuditInfo)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}

		return nil, nil, errors.Wrapf(err, "error querying db")
	}

	return tokenMetadata, tokenMetadataAuditInfo, nil
}

func (db *IdentityStore) StoreSignerInfo(ctx context.Context, id tdriver.Identity, info []byte) error {
	_, err := db.storeSignerInfo(ctx, db.writeDB, id.UniqueID(), id, info, true)

	return err
}

func (db *IdentityStore) GetExistingSignerInfo(ctx context.Context, ids ...tdriver.Identity) ([]string, error) {
	idHashes := make([]string, len(ids))
	for i, id := range ids {
		idHashes[i] = id.UniqueID()
	}

	result := make([]string, 0)
	notFound := make([]string, 0)

	for _, idHash := range idHashes {
		if v, ok := db.signerInfoCache.Get(idHash); !ok {
			notFound = append(notFound, idHash)
		} else if v {
			result = append(result, idHash)
		}
	}
	if len(notFound) == 0 {
		return result, nil
	}

	idHashes = notFound
	notFound = make([]string, 0)
	for _, idHash := range idHashes {
		if v, ok := db.signerInfoCache.Get(idHash); !ok {
			notFound = append(notFound, idHash)
		} else if v {
			result = append(result, idHash)
		}
	}
	if len(notFound) == 0 {
		return result, nil
	}

	idHashes = notFound

	query, args := q.Select().
		FieldsByName("identity_hash").
		From(q.Table(db.table.Signers)).
		Where(cond.In("identity_hash", idHashes...)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "error querying db")
	}
	it := common.NewIterator(rows, func(idHash *string) error { return rows.Scan(idHash) })

	found, err := iterators.Reduce(it, iterators.ToSet[string]())
	if err != nil {
		return nil, err
	}
	for _, idHash := range idHashes {
		db.signerInfoCache.Add(idHash, found.Contains(idHash))
	}

	return append(result, found.ToSlice()...), nil
}

func (db *IdentityStore) SignerInfoExists(ctx context.Context, id []byte) (bool, error) {
	existing, err := db.GetExistingSignerInfo(ctx, id)
	if err != nil {
		return false, err
	}

	return len(existing) > 0, nil
}

func (db *IdentityStore) GetSignerInfo(ctx context.Context, identity []byte) ([]byte, error) {
	query, args := q.Select().
		FieldsByName("info").
		From(q.Table(db.table.Signers)).
		Where(cond.Eq("identity_hash", token.Identity(identity).UniqueID())).
		Format(db.ci)

	return common.QueryUniqueContext[[]byte](ctx, db.readDB, query, args...)
}

// IterateSigners returns a page of SignerEntry values from the Signers table, ordered by
// identity_hash, starting at the given offset and returning at most limit entries.
func (db *IdentityStore) IterateSigners(ctx context.Context, offset, limit int) ([]idriver.SignerEntry, error) {
	query, args := q.Select().
		FieldsByName("identity_hash", "identity").
		From(q.Table(db.table.Signers)).
		OrderBy(q.Asc(common3.FieldName("identity_hash"))).
		Limit(limit).
		Offset(offset).
		Format(db.ci)
	logging.Debug(logger, query, args)

	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "error querying signers")
	}
	defer Close(rows)

	var entries []idriver.SignerEntry
	for rows.Next() {
		var e idriver.SignerEntry
		if err := rows.Scan(&e.IdentityHash, &e.Identity); err != nil {
			return nil, errors.Wrapf(err, "error scanning signer entry")
		}
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

func (db *IdentityStore) RegisterIdentityDescriptor(ctx context.Context, descriptor *idriver.IdentityDescriptor, alias tdriver.Identity) error {
	// store
	logger.DebugfContext(ctx, "register identity descriptor...")
	if err := db.registerIdentityDescriptor(ctx, descriptor, alias); err != nil {
		logger.ErrorfContext(ctx, "register identity descriptor...failed: %v", err)

		return err
	}
	logger.DebugfContext(ctx, "register identity descriptor...done")

	// update all caches
	logger.DebugfContext(ctx, "register identity descriptor...update caches...")
	h := descriptor.Identity.UniqueID()
	db.signerInfoCache.Add(h, true)
	if len(descriptor.AuditInfo) != 0 {
		db.auditInfoCache.Add(h, descriptor.AuditInfo)
	}
	if !alias.IsNone() && !descriptor.Identity.Equal(alias) {
		h = alias.UniqueID()
		db.signerInfoCache.Add(h, true)
		if len(descriptor.AuditInfo) != 0 {
			db.auditInfoCache.Add(h, descriptor.AuditInfo)
		}
	}
	logger.DebugfContext(ctx, "register identity descriptor...update caches...done")

	return nil
}

func (db *IdentityStore) registerIdentityDescriptor(
	ctx context.Context,
	descriptor *idriver.IdentityDescriptor,
	alias tdriver.Identity,
) error {
	if descriptor == nil {
		return errors.New("identity descriptor is nil")
	}
	tx, err := db.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			if err := tx.Rollback(); err != nil {
				logger.ErrorfContext(ctx, "failed closing connection: %s", err)
			}
		}
	}()

	h := descriptor.Identity.UniqueID()

	exists, err := db.storeSignerInfo(ctx, tx, h, descriptor.Identity, descriptor.SignerInfo, false)
	if err != nil {
		return errors.Wrapf(err, "failed to store signer info for descriptor's identity")
	}
	if exists {
		// no need to continue

		return nil
	}

	if len(descriptor.AuditInfo) != 0 {
		err = db.storeIdentityData(ctx, tx, h, descriptor.Identity, descriptor.AuditInfo, nil, nil, false)
		if err != nil {
			return errors.Wrapf(err, "failed to store audit info for descriptor's identity")
		}
	}

	if !alias.IsNone() && !descriptor.Identity.Equal(alias) {
		h = alias.UniqueID()
		_, err = db.storeSignerInfo(ctx, tx, h, alias, descriptor.SignerInfo, false)
		if err != nil {
			return errors.Wrapf(err, "failed to store signer info for alias")
		}
		if len(descriptor.AuditInfo) != 0 {
			err = db.storeIdentityData(ctx, tx, h, alias, descriptor.AuditInfo, nil, nil, false)
			if err != nil {
				return errors.Wrapf(err, "failed to store audit info for alias")
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// no rollback to be performed
	tx = nil

	return nil
}

func (db *IdentityStore) Close() error {
	return common2.Close(db.readDB, db.writeDB)
}

func (db *IdentityStore) GetSchema() string {
	return fmt.Sprintf(`
		-- IdentityConfigurations
		CREATE TABLE IF NOT EXISTS %s (
			id TEXT NOT NULL,
            type TEXT NOT NULL,  
			url TEXT NOT NULL,
			conf BYTEA,
			raw BYTEA,
			PRIMARY KEY(id, type, url)
		);
		CREATE INDEX IF NOT EXISTS idx_ic_type_%s ON %s ( type );
		CREATE INDEX IF NOT EXISTS idx_ic_id_type_%s ON %s ( id, type, url );

		-- IdentityInfo
		CREATE TABLE IF NOT EXISTS %s (
            identity_hash TEXT NOT NULL PRIMARY KEY,
			identity BYTEA NOT NULL,
			identity_audit_info BYTEA NOT NULL,
			token_metadata BYTEA,
			token_metadata_audit_info BYTEA
		);
		CREATE INDEX IF NOT EXISTS idx_audits_%s ON %s ( identity_hash );

		-- Signers
		CREATE TABLE IF NOT EXISTS %s (
            identity_hash TEXT NOT NULL PRIMARY KEY,
			identity BYTEA NOT NULL,
			info BYTEA
		);
		CREATE INDEX IF NOT EXISTS idx_signers_%s ON %s ( identity_hash );
		`,
		db.table.IdentityConfigurations,
		db.table.IdentityConfigurations, db.table.IdentityConfigurations,
		db.table.IdentityConfigurations, db.table.IdentityConfigurations,
		db.table.IdentityInfo,
		db.table.IdentityInfo, db.table.IdentityInfo,
		db.table.Signers,
		db.table.Signers, db.table.Signers,
	)
}

func (db *IdentityStore) storeSignerInfo(ctx context.Context, tx dbTransaction, h string, id tdriver.Identity, info []byte, updateCache bool) (bool, error) {
	logger.DebugfContext(ctx, "store signer info for [%s]", h)
	query, args := q.InsertInto(db.table.Signers).
		Fields("identity_hash", "identity", "info").
		Row(h, id, info).
		Format()

	logging.Debug(logger, query, h, utils.Hashable(info))
	exists := false
	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		if errors.Is(db.errorWrapper.WrapError(err), driver2.UniqueKeyViolation) {
			logger.DebugfContext(ctx, "signer info [%s] exists, no error to return", h)
			exists = true
		} else {
			return exists, err
		}
	}
	if updateCache {
		db.signerInfoCache.Add(h, true)
	}
	logger.DebugfContext(ctx, "store signer info done")

	return exists, nil
}

func (db *IdentityStore) storeIdentityData(ctx context.Context, tx dbTransaction, h string, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte, updateCache bool) error {
	logger.DebugfContext(ctx, "store identity data for [%s]", h)
	query, args := q.InsertInto(db.table.IdentityInfo).
		Fields("identity_hash", "identity", "identity_audit_info", "token_metadata", "token_metadata_audit_info").
		Row(h, id, identityAudit, tokenMetadata, tokenMetadataAudit).
		Format()
	logging.Debug(logger, query, args)

	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		if !errors.Is(db.errorWrapper.WrapError(err), driver2.UniqueKeyViolation) {
			return err
		}
		logger.DebugfContext(ctx, "identity data [%s] exists, no error to return", h)
	}

	if updateCache {
		logger.DebugfContext(ctx, "audit info cache update")
		db.auditInfoCache.Add(h, identityAudit)
		logger.DebugfContext(ctx, "audit info cache update done")
	}
	logger.DebugfContext(ctx, "store identity data for [%s] done", h)

	return nil
}
