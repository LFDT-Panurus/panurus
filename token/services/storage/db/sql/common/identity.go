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
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
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

// Savepoint names used by registerIdentityDescriptor to isolate each of its individually
// idempotent inserts. They are compile-time constants, never derived from caller input.
const (
	savepointIdentitySignerInfo = "sp_identity_signer_info"
	savepointIdentityAuditInfo  = "sp_identity_audit_info"
	savepointAliasSignerInfo    = "sp_alias_signer_info"
	savepointAliasAuditInfo     = "sp_alias_audit_info"
)

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
		Fields("id", "type", "url", "conf", "raw", "conf_id").
		Row(wp.ID, wp.Type, wp.URL, wp.Config, wp.Raw, wp.UniqueID()).
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

// GetConfigurationID returns the conf_id persisted for the configuration with the given id,
// type, and url. It returns the empty string if that configuration is not stored yet.
//
// The column is read rather than recomputed from driver.IdentityConfiguration.UniqueID on
// purpose: conf_id is referenced by wallets.conf_id through a foreign key, so for a
// configuration stored by an earlier release the value on disk is the only one the constraint
// accepts, even when a newer encoding derives a different UniqueID for the same tuple.
func (db *IdentityStore) GetConfigurationID(ctx context.Context, id, typ, url string) (string, error) {
	query, args := q.Select().
		FieldsByName("conf_id").
		From(q.Table(db.table.IdentityConfigurations)).
		Where(cond.And(cond.Eq("id", id), cond.Eq("type", typ), cond.Eq("url", url))).
		Format(db.ci)
	logging.Debug(logger, query, args)
	var confID string
	if err := db.readDB.QueryRowContext(ctx, query, args...).Scan(&confID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}

		return "", err
	}

	return confID, nil
}

// ConfigurationsByID returns all configurations with the given id and type, regardless of their url.
func (db *IdentityStore) ConfigurationsByID(ctx context.Context, id, configurationType string) ([]driver.IdentityConfiguration, error) {
	query, args := q.Select().
		FieldsByName("id", "type", "url", "conf", "raw").
		From(q.Table(db.table.IdentityConfigurations)).
		Where(cond.And(cond.Eq("id", id), cond.Eq("type", configurationType))).
		Format(db.ci)
	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var res []driver.IdentityConfiguration
	for rows.Next() {
		var c driver.IdentityConfiguration
		if err := rows.Scan(&c.ID, &c.Type, &c.URL, &c.Config, &c.Raw); err != nil {
			return nil, err
		}
		res = append(res, c)
	}

	return res, rows.Err()
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

// StoreIdentityData binds id to its audit info and token metadata.
//
// Verification: an empty id is refused. Rows are keyed by
// tdriver.Identity.UniqueID, which maps the empty identity to the constant
// "<empty>" rather than to a hash, so an empty identity would write to a
// well-known key that any later empty-identity lookup reads back as its own.
func (db *IdentityStore) StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error {
	if err := integrity.CheckIdentity(id); err != nil {
		return errors.WithMessage(err, "refusing to store identity data")
	}

	_, err := db.storeIdentityData(ctx, db.writeDB, tdriver.Identity(id).UniqueID(), id, identityAudit, tokenMetadata, tokenMetadataAudit, true)

	return err
}

// GetAuditInfo returns the audit info stored for id, or nil if none is stored.
//
// Verification: the row is addressed by identity hash, so the identity stored
// alongside the audit info is compared against id before the audit info is
// returned or cached. Audit info is what an auditor uses to attribute a
// transaction to a party, so handing back audit info belonging to a different
// identity than the caller asked for would misattribute it. A row whose
// identity and identity_hash columns disagree is reported rather than returned.
func (db *IdentityStore) GetAuditInfo(ctx context.Context, id []byte) ([]byte, error) {
	if err := integrity.CheckIdentity(id); err != nil {
		return nil, errors.WithMessage(err, "refusing to look up audit info")
	}
	h := token.Identity(id).String()
	logger.DebugfContext(ctx, "get audit info for [%s]", h)

	value, _, err := db.auditInfoCache.GetOrLoad(h, func() ([]byte, error) {
		logger.DebugfContext(ctx, "load from backend identity data for [%s]", view.Identity(id))
		query, args := q.Select().
			FieldsByName("identity", "identity_audit_info").
			From(q.Table(db.table.IdentityInfo)).
			Where(cond.Eq("identity_hash", h)).
			Format(db.ci)
		logging.Debug(logger, query, args)

		row := db.readDB.QueryRowContext(ctx, query, args...)
		var storedIdentity []byte
		var auditInfo []byte
		if err := row.Scan(&storedIdentity, &auditInfo); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}

			return nil, errors.Wrapf(err, "error querying db")
		}
		if err := integrity.CheckIdentityMatch(id, storedIdentity); err != nil {
			logger.ErrorfContext(ctx, "identity data row under [%s] does not belong to the requested identity: %v", h, err)

			return nil, errors.WithMessagef(err, "identity data row under [%s]", h)
		}

		return auditInfo, nil
	})

	return value, err
}

// GetTokenInfo returns the token metadata stored for id, or nil if none is
// stored.
//
// Verification: as for GetAuditInfo — an empty id is refused, and the identity
// stored alongside the metadata is compared against id before the metadata is
// returned. Token metadata is what the owner of a token uses to recognise and
// spend it, so metadata belonging to a different identity is not a usable
// substitute for the caller's own.
func (db *IdentityStore) GetTokenInfo(ctx context.Context, id []byte) ([]byte, []byte, error) {
	if err := integrity.CheckIdentity(id); err != nil {
		return nil, nil, errors.WithMessage(err, "refusing to look up token info")
	}
	h := token.Identity(id).String()
	logger.DebugfContext(ctx, "get identity data for [%s]", h)

	query, args := q.Select().
		FieldsByName("identity", "token_metadata", "token_metadata_audit_info").
		From(q.Table(db.table.IdentityInfo)).
		Where(cond.Eq("identity_hash", h)).
		Format(db.ci)
	logging.Debug(logger, query, args)

	row := db.readDB.QueryRowContext(ctx, query, args...)
	var storedIdentity []byte
	var tokenMetadata []byte
	var tokenMetadataAuditInfo []byte
	err := row.Scan(&storedIdentity, &tokenMetadata, &tokenMetadataAuditInfo)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}

		return nil, nil, errors.Wrapf(err, "error querying db")
	}
	if err := integrity.CheckIdentityMatch(id, storedIdentity); err != nil {
		logger.ErrorfContext(ctx, "identity data row under [%s] does not belong to the requested identity: %v", h, err)

		return nil, nil, errors.WithMessagef(err, "identity data row under [%s]", h)
	}

	return tokenMetadata, tokenMetadataAuditInfo, nil
}

// StoreSignerInfo binds id to the signer info a key manager resolves into a
// signer.
//
// Verification: an empty id is refused, for the reason given on
// StoreIdentityData — the empty identity does not hash, it maps to the shared
// "<empty>" row key.
func (db *IdentityStore) StoreSignerInfo(ctx context.Context, id tdriver.Identity, info []byte) error {
	if err := integrity.CheckIdentity(id); err != nil {
		return errors.WithMessage(err, "refusing to store signer info")
	}
	_, err := db.storeSignerInfo(ctx, db.writeDB, id.UniqueID(), id, info, true)

	return err
}

// GetExistingSignerInfo returns the unique ids of the passed identities for which
// signer info is stored.
//
// Verification: an empty identity is excluded from the lookup rather than looked
// up, for the reason given on StoreSignerInfo — its unique id is the shared
// "<empty>" row key, so consulting it would report a row written for one caller
// as the signer info of any other empty identity, and identity.Provider.AreMe
// would answer that the empty identity is mine. It is excluded rather than
// rejected because this is an existence query over a batch: an empty entry is
// simply not an identity signer info can exist for, and failing the batch would
// also lose the answer for the caller's other identities.
func (db *IdentityStore) GetExistingSignerInfo(ctx context.Context, ids ...tdriver.Identity) ([]string, error) {
	idHashes := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := integrity.CheckIdentity(id); err != nil {
			logger.DebugfContext(ctx, "excluding an identity from the signer info lookup: %v", err)

			continue
		}
		idHashes = append(idHashes, id.UniqueID())
	}
	if len(idHashes) == 0 {
		return []string{}, nil
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

// SignerInfoExists returns true if signer info is stored for id.
//
// Verification: as for GetExistingSignerInfo, which this delegates to — an empty
// id is never looked up, so it reads as "no signer info" and the shared "<empty>"
// row key stays unconsulted.
func (db *IdentityStore) SignerInfoExists(ctx context.Context, id []byte) (bool, error) {
	existing, err := db.GetExistingSignerInfo(ctx, id)
	if err != nil {
		return false, err
	}

	return len(existing) > 0, nil
}

// GetSignerInfo returns the signer info stored for identity, or nil if none is
// stored.
//
// Verification: the row is addressed by identity hash, so the identity stored
// alongside the signer info is compared against the requested one before the
// info is returned. Signer info is what a key manager resolves into a signer, so
// returning the info of a different identity than the caller named would route
// signing to the wrong key. A row whose identity and identity_hash columns
// disagree is reported rather than returned.
func (db *IdentityStore) GetSignerInfo(ctx context.Context, identity []byte) ([]byte, error) {
	if err := integrity.CheckIdentity(identity); err != nil {
		return nil, errors.WithMessage(err, "refusing to look up signer info")
	}
	h := token.Identity(identity).UniqueID()
	query, args := q.Select().
		FieldsByName("identity", "info").
		From(q.Table(db.table.Signers)).
		Where(cond.Eq("identity_hash", h)).
		Format(db.ci)
	logging.Debug(logger, query, args)

	row := db.readDB.QueryRowContext(ctx, query, args...)
	var storedIdentity []byte
	var info []byte
	if err := row.Scan(&storedIdentity, &info); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}

		return nil, errors.Wrapf(err, "error querying db")
	}
	if err := integrity.CheckIdentityMatch(identity, storedIdentity); err != nil {
		logger.ErrorfContext(ctx, "signer row under [%s] does not belong to the requested identity: %v", h, err)

		return nil, errors.WithMessagef(err, "signer row under [%s]", h)
	}

	return info, nil
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

// RegisterIdentityDescriptor stores the signer info and audit info of the descriptor's identity
// and of the given alias, and refreshes the corresponding caches. All writes happen in a single
// transaction. The operation is idempotent and safe to retry: rows that are already present are
// left untouched and the missing ones are written, so both a full re-registration and the retry
// of a registration that was only partially persisted succeed.
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
	if err := integrity.CheckIdentity(descriptor.Identity); err != nil {
		return errors.WithMessage(err, "refusing to register identity descriptor")
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

	// Each insert below is individually idempotent, and they all run on the same
	// transaction, so each one is isolated by its own savepoint. Without it, the first
	// duplicate key would abort the whole transaction on PostgreSQL and every following
	// statement would fail with a generic "current transaction is aborted" error that no
	// longer matches UniqueKeyViolation - turning an idempotent re-registration into a hard
	// failure. Rolling back to the savepoint clears that state, so a registration that was
	// only partially written (e.g. interrupted by a crash) is completed by the retry
	// instead of being rejected.
	if err := db.insertIdempotently(ctx, tx, savepointIdentitySignerInfo, func() (bool, error) {
		return db.storeSignerInfo(ctx, tx, h, descriptor.Identity, descriptor.SignerInfo, false)
	}); err != nil {
		return errors.Wrapf(err, "failed to store signer info for descriptor's identity")
	}

	if len(descriptor.AuditInfo) != 0 {
		if err := db.insertIdempotently(ctx, tx, savepointIdentityAuditInfo, func() (bool, error) {
			return db.storeIdentityData(ctx, tx, h, descriptor.Identity, descriptor.AuditInfo, nil, nil, false)
		}); err != nil {
			return errors.Wrapf(err, "failed to store audit info for descriptor's identity")
		}
	}

	if !alias.IsNone() && !descriptor.Identity.Equal(alias) {
		aliasHash := alias.UniqueID()
		if err := db.insertIdempotently(ctx, tx, savepointAliasSignerInfo, func() (bool, error) {
			return db.storeSignerInfo(ctx, tx, aliasHash, alias, descriptor.SignerInfo, false)
		}); err != nil {
			return errors.Wrapf(err, "failed to store signer info for alias")
		}
		if len(descriptor.AuditInfo) != 0 {
			if err := db.insertIdempotently(ctx, tx, savepointAliasAuditInfo, func() (bool, error) {
				return db.storeIdentityData(ctx, tx, aliasHash, alias, descriptor.AuditInfo, nil, nil, false)
			}); err != nil {
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

// insertIdempotently runs insert inside a nested savepoint on tx. insert must report whether
// the row it writes was already present, that is, whether it swallowed a duplicate-key error.
// When it was - or when insert failed outright - the savepoint is rolled back. On PostgreSQL a
// duplicate key aborts the enclosing transaction, and rolling back to the savepoint is what
// clears that state so the remaining statements of the transaction can still be executed. On
// success the savepoint is released.
//
// savepoint must be a fixed identifier, never a value derived from caller input, as it is
// interpolated into the statement.
func (db *IdentityStore) insertIdempotently(ctx context.Context, tx dbTransaction, savepoint string, insert func() (bool, error)) error {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
		return errors.Wrapf(err, "failed to set savepoint [%s]", savepoint)
	}

	exists, insertErr := insert()
	if insertErr != nil || exists {
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); err != nil {
			return errors.Join(insertErr, errors.Wrapf(err, "failed to roll back to savepoint [%s]", savepoint))
		}

		return insertErr
	}

	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
		return errors.Wrapf(err, "failed to release savepoint [%s]", savepoint)
	}

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
			conf_id TEXT NOT NULL,
			PRIMARY KEY(id, type, url),
			UNIQUE(conf_id)
		);
		CREATE INDEX IF NOT EXISTS idx_ic_type_%s ON %s ( type );

		-- IdentityInfo
		CREATE TABLE IF NOT EXISTS %s (
            identity_hash TEXT NOT NULL PRIMARY KEY,
			identity BYTEA NOT NULL,
			identity_audit_info BYTEA NOT NULL,
			token_metadata BYTEA,
			token_metadata_audit_info BYTEA
		);

		-- Signers
		CREATE TABLE IF NOT EXISTS %s (
            identity_hash TEXT NOT NULL PRIMARY KEY,
			identity BYTEA NOT NULL,
			info BYTEA
		);
		`,
		db.table.IdentityConfigurations,
		db.table.IdentityConfigurations, db.table.IdentityConfigurations,
		db.table.IdentityInfo,
		db.table.Signers,
	)
}

// storeSignerInfo inserts the signer info for the identity hashed to h. A duplicate key is
// not an error: the insert is idempotent and the returned boolean reports whether the row was
// already there. Callers running on a shared transaction must react to that boolean, see
// insertIdempotently.
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

// storeIdentityData inserts the identity data for the identity hashed to h. A duplicate key
// is not an error: the insert is idempotent and the returned boolean reports whether the row
// was already there. Callers running on a shared transaction must react to that boolean, see
// insertIdempotently.
func (db *IdentityStore) storeIdentityData(ctx context.Context, tx dbTransaction, h string, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte, updateCache bool) (bool, error) {
	logger.DebugfContext(ctx, "store identity data for [%s]", h)
	query, args := q.InsertInto(db.table.IdentityInfo).
		Fields("identity_hash", "identity", "identity_audit_info", "token_metadata", "token_metadata_audit_info").
		Row(h, id, identityAudit, tokenMetadata, tokenMetadataAudit).
		Format()
	logging.Debug(logger, query, args)

	exists := false
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		if !errors.Is(db.errorWrapper.WrapError(err), driver2.UniqueKeyViolation) {
			return exists, err
		}
		logger.DebugfContext(ctx, "identity data [%s] exists, no error to return", h)
		exists = true
	}

	if updateCache {
		logger.DebugfContext(ctx, "audit info cache update")
		db.auditInfoCache.Add(h, identityAudit)
		logger.DebugfContext(ctx, "audit info cache update done")
	}
	logger.DebugfContext(ctx, "store identity data for [%s] done", h)

	return exists, nil
}
