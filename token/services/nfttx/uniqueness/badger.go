/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package uniqueness

import (
	"context"
	"os"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	badger "github.com/dgraph-io/badger/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// BadgerKVS is a badger-backed implementation of the KVS interface.
type BadgerKVS struct {
	db *badger.DB
}

// NewBadgerKVS creates a new badger-backed KVS at the given path.
// If path is empty, an in-memory badger instance is used. If path is non-empty,
// it must point to an existing directory; otherwise the call fails fast with a
// clear error rather than surfacing badger's lower-level open failure.
func NewBadgerKVS(path string) (*BadgerKVS, error) {
	opts := badger.DefaultOptions(path)
	if path == "" {
		opts = opts.WithInMemory(true)
	} else {
		info, err := os.Stat(path)
		if err != nil {
			return nil, errors.Wrapf(err, "badger path %q is not accessible", path)
		}
		if !info.IsDir() {
			return nil, errors.Errorf("badger path %q exists but is not a directory", path)
		}
	}
	opts = opts.WithLogger(nil)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open badger db")
	}

	return &BadgerKVS{db: db}, nil
}

// Close closes the underlying badger db.
func (b *BadgerKVS) Close() error {
	return b.db.Close()
}

// Exists returns true if the key exists in the store.
//
// BadgerDB transactions are goroutine-safe via MVCC, so no external locking is
// required around Exists/Get/Put.
//
// The KVS interface forces Exists to return bool with no error, so any error
// other than ErrKeyNotFound (e.g. txn conflict, corruption) is logged here
// rather than silently swallowed - callers will see Exists return false, but
// the underlying failure will be visible in the logs for diagnosis.
func (b *BadgerKVS) Exists(_ context.Context, k string) bool {
	err := b.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(k))

		return err
	})
	if err == nil {
		return true
	}
	if !errors.Is(err, badger.ErrKeyNotFound) {
		logger.Warnf("badger Exists(%q) returned unexpected error: %v", k, err)
	}

	return false
}

// Get retrieves the value for the given key.
func (b *BadgerKVS) Get(_ context.Context, k string, v any) error {
	ptr, ok := v.(*[]byte)
	if !ok {
		return errors.Errorf("value must be *[]byte")
	}

	return b.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(k))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				// Wrap (not Errorf) so callers can still errors.Is(err, badger.ErrKeyNotFound).
				return errors.Wrapf(err, "key %s not found", k)
			}

			return err
		}

		return item.Value(func(val []byte) error {
			*ptr = append([]byte{}, val...)

			return nil
		})
	})
}

// Put stores the value for the given key.
func (b *BadgerKVS) Put(_ context.Context, k string, v any) error {
	val, ok := v.([]byte)
	if !ok {
		return errors.Errorf("value must be []byte")
	}

	return b.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(k), val)
	})
}

// NewBadgerService returns a uniqueness service backed by a badger KVS at the given path.
// If path is empty, an in-memory badger instance is used.
func NewBadgerService(path string) (*Service, error) {
	kvs, err := NewBadgerKVS(path)
	if err != nil {
		return nil, err
	}

	return &Service{kvs: kvs}, nil
}
