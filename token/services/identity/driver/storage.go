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
	// GetWalletID fetches a walletID that is bound to the identity passed
	GetWalletID(ctx context.Context, identity token.Identity, roleID int) (WalletID, error)
	// GetWalletIDs fetches all walletID's that have been stored so far without duplicates
	GetWalletIDs(ctx context.Context, roleID int) ([]WalletID, error)
	// StoreIdentity binds an identity to a walletID and its metadata
	StoreIdentity(ctx context.Context, identity token.Identity, eID string, wID WalletID, roleID int, meta []byte) error
	// IdentityExists checks whether an identity-wallet binding has already been stored
	IdentityExists(ctx context.Context, identity token.Identity, wID WalletID, roleID int) bool
	// LoadMeta returns the metadata stored for a specific identity
	LoadMeta(ctx context.Context, identity token.Identity, wID WalletID, roleID int) ([]byte, error)
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
	// ConfigurationExists returns true if a configuration with the given id and type exists.
	ConfigurationExists(ctx context.Context, id, typ, url string) (bool, error)
	// IteratorConfigurations returns an iterator to all configurations stored
	IteratorConfigurations(ctx context.Context, configurationType string) (IdentityConfigurationIterator, error)
	// Notifier returns an IdentityConfigurationNotifier for this store to subscribe to configuration changes.
	Notifier() (IdentityConfigurationNotifier, error)
	// StoreIdentityData stores the passed identity and token information
	StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error
	// GetAuditInfo retrieves the audit info bounded to the given identity
	GetAuditInfo(ctx context.Context, id []byte) ([]byte, error)
	// GetTokenInfo returns the token information related to the passed identity
	GetTokenInfo(ctx context.Context, id []byte) ([]byte, []byte, error)
	// StoreSignerInfo stores the passed signer info and bound it to the given identity
	StoreSignerInfo(ctx context.Context, id driver.Identity, info []byte) error
	// GetExistingSignerInfo returns the hashes of the identities for which StoreSignerInfo was called
	GetExistingSignerInfo(ctx context.Context, ids ...driver.Identity) ([]string, error)
	// SignerInfoExists returns true if StoreSignerInfo was called on input the given identity
	SignerInfoExists(ctx context.Context, id []byte) (bool, error)
	// GetSignerInfo returns the signer info bound to the given identity
	GetSignerInfo(ctx context.Context, id []byte) ([]byte, error)
	// RegisterIdentityDescriptor registers a descriptor for an identity and associates it with an alias
	RegisterIdentityDescriptor(ctx context.Context, descriptor *IdentityDescriptor, alias driver.Identity) error
	// IterateSigners returns a page of SignerEntry values from the Signers table ordered by
	// identity_hash, starting at the given offset and returning at most limit entries.
	// Use offset=0 for the first page and increment by limit on each subsequent call.
	// When the returned slice has fewer entries than limit, iteration is complete.
	IterateSigners(ctx context.Context, offset, limit int) ([]SignerEntry, error)
	// Close closes the store
	Close() error
}
