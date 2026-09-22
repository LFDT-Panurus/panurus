/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	mem "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/memory"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/stretchr/testify/require"
)

// closeFailingStore is a key-value store whose Close always fails.
type closeFailingStore struct {
	driver.KeyValueStore
}

func (closeFailingStore) Close() error {
	return errors.New("cannot close")
}

func TestInMemoryClose(t *testing.T) {
	backend, err := NewInMemory()
	require.NoError(t, err)
	require.NoError(t, backend.Close())
}

// TestInMemoryCloseSurfacesStoreError asserts that a failure to close the underlying store is
// returned to the caller rather than only logged: Backend.Close() is the only place a caller
// can observe it.
func TestInMemoryCloseSurfacesStoreError(t *testing.T) {
	store := utils.MustGet(mem.NewDriver().NewKVS(""))
	k, err := kvs.New(store, "", kvs.DefaultCacheSize)
	require.NoError(t, err)

	backend := &fscKVS{KVS: k, store: closeFailingStore{KeyValueStore: store}}
	require.ErrorContains(t, backend.Close(), "cannot close")
}
