/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/storage/kvs/hashicorp"
	vault "github.com/hashicorp/vault/api"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stuff struct {
	S string `json:"s"`
	I int    `json:"i"`
}

func TestVaultKVS(t *testing.T) {
	terminate, vaultURL, token := hashicorp.StartHashicorpVaultContainer(t, 10200)
	defer terminate()
	client, err := hashicorp.NewVaultClient(vaultURL, token)
	require.NoError(t, err)

	testRound(t, client)
	testCompositeKeyComponents(t, client)
	testParallelWrites(t, client)
	testParallelWritesReadDelete(t, client)
	testParallelConnections(t, client)

	terminate()

	testWithVaultDown(t, client)
}

func testRound(t *testing.T, client *vault.Client) {
	t.Helper()
	// Test with slash at the end of the vault path
	ctx := context.Background()
	kvstore, err := hashicorp.NewWithClient(client, "kv1/data/panurus/")
	require.NoError(t, err)

	k1, err := kvs.CreateCompositeKey("k", []string{"1"})
	require.NoError(t, err)
	k2, err := kvs.CreateCompositeKey("k", []string{"2"})
	require.NoError(t, err)

	err = kvstore.Put(ctx, k1, &stuff{"santa", 1})
	require.NoError(t, err)

	val := &stuff{}
	err = kvstore.Get(ctx, k1, val)
	require.NoError(t, err)
	assert.Equal(t, &stuff{"santa", 1}, val)

	err = kvstore.Put(ctx, k2, &stuff{"claws", 2})
	require.NoError(t, err)

	val = &stuff{}
	err = kvstore.Get(ctx, k2, val)
	require.NoError(t, err)
	assert.Equal(t, &stuff{"claws", 2}, val)

	results := kvstore.GetExisting(ctx, k1, k2)
	assert.Len(t, results, 2)

	it, err := kvstore.GetByPartialCompositeID(ctx, "k", []string{})
	require.NoError(t, err)
	defer hashicorp.SilentClose(it)

	for ctr := 0; it.HasNext(); ctr++ {
		val = &stuff{}
		key, err := it.Next(val)
		require.NoError(t, err)
		switch ctr {
		case 0:
			assert.Equal(t, k1, key)
			assert.Equal(t, &stuff{"santa", 1}, val)
		case 1:
			assert.Equal(t, k2, key)
			assert.Equal(t, &stuff{"claws", 2}, val)
		default:
			assert.Fail(t, "expected 2 entries in the range, found more")
		}
	}

	require.NoError(t, kvstore.Delete(t.Context(), k2))
	assert.False(t, kvstore.Exists(ctx, k2))

	results = kvstore.GetExisting(ctx, k1, k2)
	assert.Len(t, results, 1)
	assert.Equal(t, results[0], k1)

	val = &stuff{}
	err = kvstore.Get(ctx, k2, val)
	require.NoError(t, err)

	for ctr := 0; it.HasNext(); ctr++ {
		val = &stuff{}
		key, err := it.Next(val)
		require.NoError(t, err)
		if ctr == 0 {
			assert.Equal(t, k1, key)
			assert.Equal(t, &stuff{"santa", 1}, val)
		} else {
			assert.Fail(t, "expected 2 entries in the range, found more")
		}
	}

	// Test the iterator calling Next without hasNext first in case the
	// iterator has been exhausted
	_, err = it.Next(val)
	require.Error(t, err)

	it, err = kvstore.GetByPartialCompositeID(ctx, "k", []string{})
	require.NoError(t, err)
	defer hashicorp.SilentClose(it)
	for ctr := 0; it.HasNext(); ctr++ {
		val = &stuff{}
		key, err := it.Next(val)
		require.NoError(t, err)
		if ctr == 0 {
			assert.Equal(t, k1, key)
			assert.Equal(t, &stuff{"santa", 1}, val)
		} else {
			assert.Fail(t, "expected 1 entries in the range, found more")
		}
	}

	require.NoError(t, kvstore.Delete(t.Context(), k1))

	val = &stuff{
		S: "hello",
		I: 100,
	}
	data := "Hello World"
	hash := sha256.Sum256([]byte(data)) // Replace with hash.Hashable if applicable
	k := hex.EncodeToString(hash[:])    // Convert to clean hex string

	require.NoError(t, kvstore.Put(ctx, k, val))
	assert.True(t, kvstore.Exists(ctx, k))
	val2 := &stuff{}
	require.NoError(t, kvstore.Get(ctx, k, val2))
	assert.Equal(t, val, val2)

	results = kvstore.GetExisting(ctx, k)
	assert.Len(t, results, 1)

	it, err = kvstore.GetByPartialCompositeID(ctx, k, []string{})
	require.NoError(t, err)
	// nothing stored under the prefix: an empty iterator, not a nil one
	require.NotNil(t, it)
	assert.False(t, it.HasNext())
	require.NoError(t, kvstore.Delete(t.Context(), k))
	assert.False(t, kvstore.Exists(ctx, k))

	k1, err = kvs.CreateCompositeKey(k, []string{"1"})
	require.NoError(t, err)
	require.NoError(t, kvstore.Put(ctx, k1, val))
	it, err = kvstore.GetByPartialCompositeID(ctx, k, []string{})
	require.NoError(t, err)
	defer hashicorp.SilentClose(it)
	for ctr := 0; it.HasNext(); ctr++ {
		val = &stuff{}
		key, err := it.Next(val)
		require.NoError(t, err)
		if ctr == 0 {
			assert.Equal(t, k1, key)
			assert.Equal(t, &stuff{"hello", 100}, val)
		} else {
			assert.Fail(t, "expected 1 entries in the range, found more")
		}
	}
	require.NoError(t, kvstore.Delete(t.Context(), k1))
	assert.False(t, kvstore.Exists(ctx, k1))
	require.NoError(t, kvstore.Delete(t.Context(), k1))

	it, err = kvstore.GetByPartialCompositeID(ctx, k, []string{})
	require.NoError(t, err)
	require.NotNil(t, it)
	assert.False(t, it.HasNext())

	_, err = kvstore.GetByPartialCompositeID(ctx, "k", []string{})
	require.NoError(t, err)

	k3, err := kvs.CreateCompositeKey("k", []string{"3"})
	require.NoError(t, err)

	err = kvstore.Put(ctx, k3, nil)
	require.NoError(t, err)

	err = kvstore.Get(ctx, k3, nil)
	require.Error(t, err)

	require.NoError(t, kvstore.Delete(t.Context(), k3))
	require.NoError(t, kvstore.Delete(t.Context(), k3))

	err = kvstore.Get(ctx, k3, nil)
	require.NoError(t, err)
	assert.False(t, it.HasNext())

	k4, _ := kvs.CreateCompositeKey("k", []string{"4"})
	require.NoError(t, kvstore.Delete(t.Context(), k4))

	results = kvstore.GetExisting(ctx)
	assert.Empty(t, results)
}

// testCompositeKeyComponents asserts that composite keys whose components are empty, or carry
// the Vault path separator, a percent sign or a relative path element, address distinct
// secrets and come back out of the iterator unchanged. Before the components were escaped,
// CreateCompositeKey("", []string{"1"}) and CreateCompositeKey("1", nil) resolved to the same
// Vault path. It also covers a key that disappears between the list and its read: the
// iterator must skip it instead of yielding a zero-valued state.
func testCompositeKeyComponents(t *testing.T, client *vault.Client) {
	t.Helper()
	ctx := t.Context()
	kvstore, err := hashicorp.NewWithClient(client, "kv1/data/panurus/components/")
	require.NoError(t, err)

	emptyObjectType, err := kvs.CreateCompositeKey("", []string{"1"})
	require.NoError(t, err)
	noAttributes, err := kvs.CreateCompositeKey("1", nil)
	require.NoError(t, err)
	require.NoError(t, kvstore.Put(ctx, emptyObjectType, &stuff{"empty-object-type", 1}))
	require.NoError(t, kvstore.Put(ctx, noAttributes, &stuff{"no-attributes", 2}))

	val := &stuff{}
	require.NoError(t, kvstore.Get(ctx, emptyObjectType, val))
	assert.Equal(t, &stuff{"empty-object-type", 1}, val)
	val = &stuff{}
	require.NoError(t, kvstore.Get(ctx, noAttributes, val))
	assert.Equal(t, &stuff{"no-attributes", 2}, val)

	const prefix = "ck"
	keys := make([]string, 0, 4)
	expected := make(map[string]*stuff, 4)
	for i, attrs := range [][]string{
		{"tms", "MHg=+/abc"}, // base64, as Identity.UniqueID() produces
		{"tms", ""},
		{"tms", ".."},
		{"tms", "100%"},
	} {
		k, err := kvs.CreateCompositeKey(prefix, attrs)
		require.NoError(t, err)
		value := &stuff{strings.Join(attrs, "|"), i}
		require.NoError(t, kvstore.Put(ctx, k, value))
		keys = append(keys, k)
		expected[k] = value
	}

	it, err := kvstore.GetByPartialCompositeID(ctx, prefix, []string{"tms"})
	require.NoError(t, err)
	defer hashicorp.SilentClose(it)
	found := make(map[string]*stuff, len(expected))
	for it.HasNext() {
		value := &stuff{}
		key, err := it.Next(value)
		require.NoError(t, err)
		found[key] = value
	}
	assert.Equal(t, expected, found)

	// the list is taken when the iterator is created, the values are read as it advances
	itDeleted, err := kvstore.GetByPartialCompositeID(ctx, prefix, []string{"tms"})
	require.NoError(t, err)
	defer hashicorp.SilentClose(itDeleted)
	require.NoError(t, kvstore.Delete(ctx, keys[0]))
	remaining := make(map[string]*stuff, len(expected)-1)
	for itDeleted.HasNext() {
		value := &stuff{}
		key, err := itDeleted.Next(value)
		require.NoError(t, err)
		remaining[key] = value
	}
	assert.NotContains(t, remaining, keys[0])
	assert.Len(t, remaining, len(expected)-1)

	for _, k := range keys[1:] {
		require.NoError(t, kvstore.Delete(ctx, k))
	}
	require.NoError(t, kvstore.Delete(ctx, emptyObjectType))
	require.NoError(t, kvstore.Delete(ctx, noAttributes))
}

func testParallelWrites(t *testing.T, client *vault.Client) {
	t.Helper()
	kvstore, err := hashicorp.NewWithClient(client, "kv1/data/panurus")
	require.NoError(t, err)
	ctx := context.Background()

	// different composite key keys
	wg := sync.WaitGroup{}
	n := 100
	wg.Add(n)
	for i := range n {
		go func(i int) {
			k1, err := kvs.CreateCompositeKey("parallel_key_1_", []string{strconv.Itoa(i)})
			assert.NoError(t, err)
			err = kvstore.Put(ctx, k1, &stuff{"santa", i})
			assert.NoError(t, err)
			defer wg.Done()
		}(i)
	}
	wg.Wait()

	// same key
	wg = sync.WaitGroup{}
	wg.Add(n)
	k1, err := kvs.CreateCompositeKey("parallel_key_2_", []string{"1"})
	require.NoError(t, err)
	for i := range n {
		go func(i int) {
			err := kvstore.Put(ctx, k1, &stuff{"santa", 1})
			assert.NoError(t, err)
			defer wg.Done()
		}(i)
	}
	wg.Wait()

	// different none composite key keys
	wg = sync.WaitGroup{}
	wg.Add(n)

	for i := range n {
		go func(i int) {
			data := "Hello World " + strconv.Itoa(i)
			hash := sha256.Sum256([]byte(data)) // Replace with hash.Hashable if applicable
			k2 := hex.EncodeToString(hash[:])   // Convert to clean hex string
			err := kvstore.Put(ctx, k2, &stuff{"hello", 1})
			assert.NoError(t, err)
			defer wg.Done()
		}(i)
	}
	wg.Wait()
}

func testParallelWritesReadDelete(t *testing.T, client *vault.Client) {
	t.Helper()
	kvstore, err := hashicorp.NewWithClient(client, "kv1/data/panurus")
	require.NoError(t, err)
	ctx := context.Background()

	// different composite key keys
	wg := sync.WaitGroup{}
	n := 100
	wg.Add(n)
	for i := range n {
		go func(i int) {
			k, err := kvs.CreateCompositeKey("parallel_key_2_", []string{strconv.Itoa(i)})
			assert.NoError(t, err)

			err = kvstore.Put(ctx, k, &stuff{"santa", i})
			assert.NoError(t, err)

			val := &stuff{}
			err = kvstore.Get(ctx, k, val)
			assert.NoError(t, err)
			assert.Equal(t, &stuff{"santa", i}, val)

			assert.NoError(t, kvstore.Delete(t.Context(), k))
			defer wg.Done()
		}(i)
	}
	wg.Wait()
}

//nolint:testifylint
func testClient(t *testing.T, wg *sync.WaitGroup, prefix string, num int, client *vault.Client) {
	t.Helper()
	defer wg.Done()
	ctx := t.Context()

	// Test without slah at the end of the vault path
	kvstore, err := hashicorp.NewWithClient(client, "kv1/data/panurus")
	assert.NoError(t, err)

	for i := 1; i <= num; i++ {
		k, err := kvs.CreateCompositeKey(prefix, []string{strconv.Itoa(i)})
		assert.NoError(t, err)

		err = kvstore.Put(ctx, k, &stuff{"santa", i})
		assert.NoError(t, err)

		val := &stuff{}
		err = kvstore.Get(ctx, k, val)
		assert.NoError(t, err)
		assert.Equal(t, &stuff{"santa", i}, val)

		assert.NoError(t, kvstore.Delete(t.Context(), k))
	}
}

func testParallelConnections(t *testing.T, client *vault.Client) {
	t.Helper()
	var wg sync.WaitGroup
	// test 20 clients that issues 50 put, get and delete to vault
	n := 20
	wg.Add(n)
	for i := 1; i <= n; i++ {
		go testClient(t, &wg, "parallel_client_"+strconv.Itoa(i), 50, client)
	}
	wg.Wait()
}

//nolint:testifylint
func testWithVaultDown(t *testing.T, client *vault.Client) {
	t.Helper()
	// Test with slash at the end of the vault path
	ctx := context.Background()

	kvstore, err := hashicorp.NewWithClient(client, "kv1/data/panurus/")
	assert.NoError(t, err)

	k1, err := kvs.CreateCompositeKey("k", []string{"1"})
	assert.NoError(t, err)
	k2, err := kvs.CreateCompositeKey("k", []string{"2"})
	assert.NoError(t, err)

	err = kvstore.Put(ctx, k1, &stuff{"santa", 1})
	assert.Error(t, err)

	val := &stuff{}
	err = kvstore.Get(ctx, k1, val)
	assert.Error(t, err)

	assert.False(t, kvstore.Exists(ctx, k2))

	results := kvstore.GetExisting(ctx, k1, k2)
	assert.Empty(t, results)

	assert.Error(t, kvstore.Delete(t.Context(), k1))

	it, err := kvstore.GetByPartialCompositeID(ctx, "k", []string{})
	assert.Error(t, err)
	assert.Nil(t, it)
}
