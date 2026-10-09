/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutAndGet(t *testing.T) {
	store := NewTrackedMemory()

	data := "Alice"
	require.NoError(t, store.Put("user1", data))

	var retrievedData string
	require.NoError(t, store.Get("user1", &retrievedData))
	assert.Equal(t, data, retrievedData)

	assert.Equal(t, 1, store.PutCounter)
	assert.Equal(t, 1, store.GetCounter)
	assert.Len(t, store.PutHistory, 1)
	assert.Len(t, store.GetHistory, 1)
	assert.Equal(t, "user1", store.GetHistory[0].Key)
	assert.Equal(t, data, *(store.GetHistory[0].Value.(*string)))
}

func TestGetNonExistentKey(t *testing.T) {
	store := NewTrackedMemory()

	var retrievedData string
	require.Error(t, store.Get("nonexistent", &retrievedData))

	assert.Equal(t, 1, store.GetCounter)
	assert.Len(t, store.GetHistory, 1)
	assert.Equal(t, "nonexistent", store.GetHistory[0].Key)
	assert.Nil(t, store.GetHistory[0].Value)
	assert.Equal(t, "state [,nonexistent] does not exist", store.GetHistory[0].Error)
}

func TestTypeMismatch(t *testing.T) {
	store := NewTrackedMemory()

	require.NoError(t, store.Put("number", 42))

	var wrongType string
	err := store.Get("number", &wrongType)
	require.Error(t, err)
	assert.Equal(t, "failed retrieving state [,number], cannot unmarshal state: json: cannot unmarshal number into Go value of type string", err.Error())

	assert.Equal(t, 1, store.GetCounter)
	assert.Len(t, store.GetHistory, 1)
	assert.Equal(t, "number", store.GetHistory[0].Key)
	assert.Nil(t, store.GetHistory[0].Value)
	assert.Equal(t, "failed retrieving state [,number], cannot unmarshal state: json: cannot unmarshal number into Go value of type string", store.GetHistory[0].Error)
}

// TestHistoryEntriesAreSnapshots asserts that the Put/Get history records the value each call
// actually carried, even when the caller reuses one destination across calls - the common
// `var v T; Get(k1, &v); Get(k2, &v)` idiom. Recording the pointer itself would make the first
// entry retroactively show the second call's data.
func TestHistoryEntriesAreSnapshots(t *testing.T) {
	store := NewTrackedMemory()

	require.NoError(t, store.Put("user1", "Alice"))
	require.NoError(t, store.Put("user2", "Bob"))

	// one destination, reused across both reads
	var retrieved string
	require.NoError(t, store.Get("user1", &retrieved))
	require.NoError(t, store.Get("user2", &retrieved))

	require.Len(t, store.GetHistory, 2)
	assert.Equal(t, "Alice", *(store.GetHistory[0].Value.(*string)))
	assert.Equal(t, "Bob", *(store.GetHistory[1].Value.(*string)))

	// mutating the destination afterwards leaves the history alone
	retrieved = "Carol"
	assert.Equal(t, "Alice", *(store.GetHistory[0].Value.(*string)))
	assert.Equal(t, "Bob", *(store.GetHistory[1].Value.(*string)))

	// the same holds for the written values
	entry := &stuff{S: "first"}
	require.NoError(t, store.Put("entry", entry))
	entry.S = "second"
	require.NoError(t, store.Put("entry", entry))

	require.Len(t, store.PutHistory, 4)
	assert.Equal(t, "first", store.PutHistory[2].Value.(*stuff).S)
	assert.Equal(t, "second", store.PutHistory[3].Value.(*stuff).S)
}

type stuff struct {
	S string `json:"s"`
}
