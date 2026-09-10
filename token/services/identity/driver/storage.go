/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

// Keystore provides a minimal key/value style interface used by the identity
// service to persist arbitrary cryptographic key objects keyed by an identifier.
//
// The `id` parameter is expected to be the hexadecimal representation of the key's
// Subject Key Identifier (SKI). Other packages may depend on this convention.
//
// Implementations should treat the provided `key` as an opaque value.
// For Put the caller supplies the value to store; for Get the caller supplies a
// pointer or value that implementations should populate with the stored
// representation.
// Implementations are responsible for any necessary (de)serialization.
type Keystore interface {
	// Put stores the given key under the provided id. Implementations MUST
	// overwrite any existing value for the id and return a non-nil error on
	// failure.
	Put(id string, key any) error

	// Get retrieves the key stored under the provided id and populates the
	// provided `key` parameter.
	// If no entry exists for id, implementations should return an error describing the missing entry.
	Get(id string, key any) error

	// Delete removes the key with the given identifier.
	// If the key does not exist, implementations should return nil (idempotent).
	Delete(id string) error

	// Close closes the store
	Close() error
}

// StorageProvider returns storage services scoped to a specific token
// management system (TMS) identified by token.TMSID.
// Callers request the concrete store service for the given TMS and use the returned service to
// access persisted wallet, identity, or keystore data.
//
//go:generate counterfeiter -o mock/sp.go -fake-name StorageProvider . StorageProvider
type StorageProvider interface {
	// WalletStore returns a WalletStoreService for the given tmsID.
	WalletStore(tmsID token.TMSID) (WalletStoreService, error)

	// IdentityStore returns an IdentityStoreService for the given tmsID.
	IdentityStore(tmsID token.TMSID) (IdentityStoreService, error)

	// Keystore returns a Keystore service for the given tmsID.
	Keystore(tmsID token.TMSID) (Keystore, error)
}

// IdentityConfigurationIterator is an iterator over stored IdentityConfiguration values.
// It yields pointers to IdentityConfiguration and follows the iterator
// contract defined in the collections/iterators package.
type IdentityConfigurationIterator = iterators.Iterator[*IdentityConfiguration]

// SignerEntry holds a single row from the Signers table.
type SignerEntry struct {
	// IdentityHash is the primary key of the Signers row (hex-encoded hash of the identity).
	IdentityHash string
	// Identity is the raw serialised identity bytes.
	Identity []byte
}

// WalletID models the wallet id type
type WalletID = string

// WalletStoreService provides operations for binding identities to wallets and
// managing associated metadata.
//
//go:generate counterfeiter -o mock/wss.go -fake-name WalletStoreService . WalletStoreService
type WalletStoreService interface {
	// GetWalletID fetches a walletID that is bound to the identity passed.
	// It returns an empty WalletID and no error if the identity has no stored
	// binding; a non-nil error indicates a genuine storage failure and must not
	// be interpreted by callers as "no binding".
	GetWalletID(ctx context.Context, identity token.Identity, roleID int) (WalletID, error)
	// GetWalletIDs fetches all walletID's that have been stored so far without duplicates
	GetWalletIDs(ctx context.Context, roleID int) ([]WalletID, error)
	// StoreIdentity binds an identity to a walletID and its metadata, linking it to the
	// identity configuration (by its unique id, see driver.IdentityConfiguration.UniqueID)
	// that originated it.
	StoreIdentity(ctx context.Context, identity token.Identity, eID string, wID WalletID, roleID int, meta []byte, confID string) error
	// IdentityExists checks whether an identity-wallet binding has already been stored
	IdentityExists(ctx context.Context, identity token.Identity, wID WalletID, roleID int) bool
	// LoadMeta returns the metadata stored for a specific identity
	LoadMeta(ctx context.Context, identity token.Identity, wID WalletID, roleID int) ([]byte, error)
	// GetConfID returns the identity configuration id (see driver.IdentityConfiguration.UniqueID)
	// that this identity was bound with, regardless of role. Returns an empty string and no error
	// if the identity has no stored binding.
	GetConfID(ctx context.Context, identity token.Identity) (string, error)
	// Close closes the store
	Close() error
}

type IdentityDescriptor struct {
	Identity  Identity
	AuditInfo []byte

	Signer     driver.Signer
	SignerInfo []byte
	Verifier   driver.Verifier

	// Ephemeral if true, nothing will be stored in the storage space
	Ephemeral bool
}

// IdentityNotifier is an alias to the driver-level notifier.
// It is used to subscribe to events in the identity storage.
type IdentityNotifier = driver2.Notifier

type (
	// Operation defines the type of database operation (Insert, Update, etc.).
	Operation = driver2.Operation
	// ColumnKey defines the name of a database column.
	ColumnKey = driver2.ColumnKey
)

const (
	// Insert indicates a record was added to the table.
	Insert = driver2.Insert
	// Update indicates an existing record was modified.
	Update = driver2.Update
)

// IdentityConfigurationRecord contains the primary key fields of an identity configuration.
type IdentityConfigurationRecord struct {
	// ID is the unique identifier of the identity.
	ID string
	// Type is the type of the identity (e.g. "fabtoken", "zkatdlog").
	Type string
	// URL is the path to the credentials relevant to this identity.
	URL string
}

// IdentityConfigurationNotifier is used to subscribe to configuration changes in the identity storage.
type IdentityConfigurationNotifier interface {
	// Subscribe registers a callback function to be called when an identity configuration is inserted or updated.
	Subscribe(callback func(Operation, IdentityConfigurationRecord)) error
	// UnsubscribeAll unregisters all callbacks.
	UnsubscribeAll() error
}

// IdentityStoreService provides persistent storage operations for identity
// configurations, audit data, token metadata, and signer-related information.
//
//go:generate counterfeiter -o mock/iss.go -fake-name IdentityStoreService . IdentityStoreService
type IdentityStoreService interface {
	// AddConfiguration stores an identity and the path to the credentials relevant to this identity
	AddConfiguration(ctx context.Context, wp IdentityConfiguration) error
	// GetConfiguration returns the configuration with the given id, type, and url.
	// It returns nil if the configuration does not exist.
	GetConfiguration(ctx context.Context, id, typ, url string) (*IdentityConfiguration, error)
	// GetConfigurationID returns the conf_id persisted for the configuration with the given
	// id, type, and url, or the empty string if that configuration is not stored yet.
	//
	// The stored value is authoritative and must be preferred over recomputing
	// IdentityConfiguration.UniqueID: a release that changes how the composite key is encoded
	// derives a different UniqueID for an unchanged configuration, while the value the wallet
	// rows reference by foreign key is the one already on disk.
	GetConfigurationID(ctx context.Context, id, typ, url string) (string, error)
	// ConfigurationsByID returns all configurations with the given id and type, regardless of their url.
	ConfigurationsByID(ctx context.Context, id, configurationType string) ([]IdentityConfiguration, error)
	// ConfigurationExists returns true if a configuration with the given id and type exists.
	ConfigurationExists(ctx context.Context, id, typ, url string) (bool, error)
	// IteratorConfigurations returns an iterator to all configurations stored
	IteratorConfigurations(ctx context.Context, configurationType string) (IdentityConfigurationIterator, error)
	// Notifier returns an IdentityConfigurationNotifier for this store to subscribe to configuration changes.
	Notifier() (IdentityConfigurationNotifier, error)
	// StoreIdentityData stores the passed identity and token information
	//
	// Verification: an implementation must refuse an empty id — see
	// integrity.CheckIdentity. Rows are addressed by driver.Identity.UniqueID,
	// which maps the empty identity to the constant "<empty>" rather than to a
	// hash, so every empty identity shares one row: one caller's audit info
	// would be readable by any other empty-identity lookup.
	StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error
	// GetAuditInfo retrieves the audit info bounded to the given identity
	//
	// Verification: an empty id is refused, and — because the row is addressed
	// by identity hash rather than by the identity itself — the identity stored
	// alongside the audit info must be compared against id before the audit info
	// is returned or cached (see integrity.CheckIdentityMatch). Audit info is
	// what attributes a transaction to a party, so audit info belonging to a
	// different identity misattributes it.
	GetAuditInfo(ctx context.Context, id []byte) ([]byte, error)
	// GetTokenInfo returns the token information related to the passed identity
	//
	// Verification: as for GetAuditInfo.
	GetTokenInfo(ctx context.Context, id []byte) ([]byte, []byte, error)
	// StoreSignerInfo stores the passed signer info and bound it to the given identity
	//
	// Verification: an empty id is refused, for the reason given on
	// StoreIdentityData.
	StoreSignerInfo(ctx context.Context, id driver.Identity, info []byte) error
	// GetExistingSignerInfo returns the hashes of the identities for which StoreSignerInfo was called
	//
	// Verification: an implementation must not consult the shared "<empty>" key
	// an empty identity hashes to — see integrity.CheckIdentity. Because this is
	// an existence query over a batch, an empty identity is excluded from the
	// lookup and reported as not existing, rather than failing the call and with
	// it the answer for the caller's other identities.
	GetExistingSignerInfo(ctx context.Context, ids ...driver.Identity) ([]string, error)
	// SignerInfoExists returns true if StoreSignerInfo was called on input the given identity
	//
	// Verification: as for GetExistingSignerInfo. An empty id reads as false.
	SignerInfoExists(ctx context.Context, id []byte) (bool, error)
	// GetSignerInfo returns the signer info bound to the given identity
	//
	// Verification: as for GetAuditInfo. Signer info is what a key manager
	// resolves into a signer, so returning another identity's signer info would
	// route signing to the wrong key.
	GetSignerInfo(ctx context.Context, id []byte) ([]byte, error)
	// RegisterIdentityDescriptor registers a descriptor for an identity and associates it with an alias.
	// The operation is idempotent and safe to retry: registering the same descriptor again succeeds
	// without error, and a registration that was only partially persisted is completed by the retry.
	//
	// Verification: a nil descriptor and an empty descriptor.Identity are
	// refused, for the reason given on StoreIdentityData. This holds even for an
	// ephemeral descriptor, which writes nothing to storage but still populates
	// the in-memory caches, which are keyed the same way.
	RegisterIdentityDescriptor(ctx context.Context, descriptor *IdentityDescriptor, alias driver.Identity) error
	// IterateSigners returns a page of SignerEntry values from the Signers table ordered by
	// identity_hash, starting at the given offset and returning at most limit entries.
	// Use offset=0 for the first page and increment by limit on each subsequent call.
	// When the returned slice has fewer entries than limit, iteration is complete.
	IterateSigners(ctx context.Context, offset, limit int) ([]SignerEntry, error)
	// Close closes the store
	Close() error
}
