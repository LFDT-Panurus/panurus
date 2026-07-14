/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
)

// Storage defines the interface for storing token transaction records
//
//go:generate counterfeiter -o mock/storage.go -fake-name Storage . Storage
type Storage interface {
	AppendValidationRecord(ctx context.Context, txID string, tokenRequest []byte, meta map[string][]byte, ppHash tdriver.PPHash) error
}

// StorageProvider defines the interface for obtaining token transaction storage instances
//
//go:generate counterfeiter -o mock/storage_provider.go -fake-name StorageProvider . StorageProvider
type StorageProvider interface {
	// GetStorage returns the Storage instance for the given TMS ID
	GetStorage(id token.TMSID) (Storage, error)
}
