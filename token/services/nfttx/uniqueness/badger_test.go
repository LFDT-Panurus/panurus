/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package uniqueness

import (
	"fmt"
	"os"
	"sync"
	"testing"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBadgerKVS(t *testing.T) *BadgerKVS {
	t.Helper()
	kvs, err := NewBadgerKVS("")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, kvs.Close())
	})

	return kvs
}

func TestBadgerKVS_PutAndGet(t *testing.T) {
	t.Parallel()
	b := newTestBadgerKVS(t)
	ctx := t.Context()
	key := "test-key"
	val := []byte("test-value")

	require.False(t, b.Exists(ctx, key))
	require.NoError(t, b.Put(ctx, key, val))
	require.True(t, b.Exists(ctx, key))

	var result []byte
	require.NoError(t, b.Get(ctx, key, &result))
	require.Equal(t, val, result)
}

func TestBadgerKVS_GetNotFound(t *testing.T) {
	t.Parallel()
	b := newTestBadgerKVS(t)
	var result []byte
	err := b.Get(t.Context(), "missing", &result)
	require.Error(t, err)
}

func TestBadgerKVS_PutInvalidValue(t *testing.T) {
	t.Parallel()
	b := newTestBadgerKVS(t)
	err := b.Put(t.Context(), "key", "not-bytes")
	require.Error(t, err)
}

func TestBadgerKVS_GetInvalidPointer(t *testing.T) {
	t.Parallel()
	b := newTestBadgerKVS(t)
	require.NoError(t, b.Put(t.Context(), "key", []byte("val")))
	var wrong string
	err := b.Get(t.Context(), "key", &wrong)
	require.Error(t, err)
}

func TestNewBadgerKVS_InMemory(t *testing.T) {
	t.Parallel()
	kvs, err := NewBadgerKVS("")
	require.NoError(t, err)
	require.NotNil(t, kvs)
	defer func() { require.NoError(t, kvs.Close()) }()
}

func TestNewBadgerKVS_OnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kvs, err := NewBadgerKVS(dir)
	require.NoError(t, err)
	require.NotNil(t, kvs)
	defer func() { require.NoError(t, kvs.Close()) }()

	require.NoError(t, kvs.Put(t.Context(), "k", []byte("v")))
	var result []byte
	require.NoError(t, kvs.Get(t.Context(), "k", &result))
	require.Equal(t, []byte("v"), result)
}

func TestNewBadgerService(t *testing.T) {
	t.Parallel()
	svc, err := NewBadgerService("")
	require.NoError(t, err)
	require.NotNil(t, svc)

	id, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	require.NotEmpty(t, id)
}

func TestBadgerService_ComputeID_Idempotent(t *testing.T) {
	t.Parallel()
	svc, err := NewBadgerService("")
	require.NoError(t, err)

	id1, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	id2, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	require.Equal(t, id1, id2)
}

func TestBadgerService_ComputeID_DistinctStates(t *testing.T) {
	t.Parallel()
	svc, err := NewBadgerService("")
	require.NoError(t, err)

	type Asset struct {
		ID    string
		Value int
	}

	id1, err := svc.ComputeID(t.Context(), Asset{ID: "a1", Value: 100})
	require.NoError(t, err)
	require.NotEmpty(t, id1)

	id2, err := svc.ComputeID(t.Context(), Asset{ID: "a2", Value: 200})
	require.NoError(t, err)
	require.NotEmpty(t, id2)

	require.NotEqual(t, id1, id2)
}

func TestBadgerKVS_GetNotFound_PreservesErrKeyNotFound(t *testing.T) {
	t.Parallel()
	b := newTestBadgerKVS(t)

	var result []byte
	err := b.Get(t.Context(), "missing", &result)
	require.Error(t, err)
	require.True(t, errors.Is(err, badger.ErrKeyNotFound),
		"expected error to wrap badger.ErrKeyNotFound, got: %v", err)
}

func TestNewBadgerKVS_NonExistentPath(t *testing.T) {
	t.Parallel()
	kvs, err := NewBadgerKVS("/nonexistent/path/that/should/not/exist")
	require.Error(t, err)
	require.Nil(t, kvs)
}

func TestNewBadgerKVS_PathIsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := dir + "/not-a-dir"
	require.NoError(t, writeEmptyFile(file))

	kvs, err := NewBadgerKVS(file)
	require.Error(t, err)
	require.Nil(t, kvs)
}

func TestBadgerKVS_ConcurrentReadsAndWrites(t *testing.T) {
	t.Parallel()
	b := newTestBadgerKVS(t)
	ctx := t.Context()

	const workers = 32
	const opsPerWorker = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := range workers {
		go func(w int) {
			defer wg.Done()
			// Use assert (not require) inside goroutines: require.* calls
			// t.FailNow, which is only safe on the goroutine running the test.
			// assert.* records the failure and lets the goroutine return cleanly.
			for i := range opsPerWorker {
				key := fmt.Sprintf("worker-%d-key-%d", w, i)
				val := fmt.Appendf(nil, "worker-%d-val-%d", w, i)

				assert.NoError(t, b.Put(ctx, key, val))
				assert.True(t, b.Exists(ctx, key))

				var got []byte
				assert.NoError(t, b.Get(ctx, key, &got))
				assert.Equal(t, val, got)
			}
		}(w)
	}
	wg.Wait()

	for w := range workers {
		for i := range opsPerWorker {
			require.True(t, b.Exists(ctx, fmt.Sprintf("worker-%d-key-%d", w, i)))
		}
	}
}

func writeEmptyFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}

	return f.Close()
}
