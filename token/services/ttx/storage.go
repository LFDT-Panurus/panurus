/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

var storageProviderType = reflect.TypeFor[*StorageProvider]()

//go:generate counterfeiter -o dep/mock/storage.go -fake-name Storage . Storage

// Storage defines the interface for storing token transaction records
type Storage interface {
	Append(ctx context.Context, tx *Transaction) error
}

//go:generate counterfeiter -o dep/mock/storage_provider.go -fake-name StorageProvider . StorageProvider

// StorageProvider defines the interface for obtaining token transaction storage instances
type StorageProvider interface {
	// GetStorage returns the Storage instance for the given TMS ID
	GetStorage(id token.TMSID) (Storage, error)
	// CacheRequest caches the given token request for the given TMS ID
	CacheRequest(ctx context.Context, tmsID token.TMSID, request *token.Request) error
}

// GetStorageProvider retrieves the StorageProvider instance from the given service provider
func GetStorageProvider(sp token.ServiceProvider) (StorageProvider, error) {
	s, err := sp.GetService(storageProviderType)
	if err != nil {
		return nil, err
	}

	return s.(StorageProvider), nil
}

// StoreTransactionRecords stores the transaction records extracted from the passed transaction to the
// Storage bound to the transaction's TMS ID
func StoreTransactionRecords(ctx view.Context, tx *Transaction) error {
	sp, err := GetStorageProvider(ctx)
	if err != nil {
		return errors.Join(ErrStorage, err)
	}
	s, err := sp.GetStorage(tx.TMS.ID())
	if err != nil {
		return errors.Join(ErrStorage, err)
	}
	if err := s.Append(ctx.Context(), tx); err != nil {
		return errors.Join(ErrStorage, err)
	}

	return nil
}
