/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package uniqueness

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryKVS_PutAndGet(t *testing.T) {
	t.Parallel()
	m := NewMemoryKVS()
	ctx := t.Context()
	key := "test-key"
	val := []byte("test-value")

	require.False(t, m.Exists(ctx, key))
	require.NoError(t, m.Put(ctx, key, val))
	require.True(t, m.Exists(ctx, key))

	var result []byte
	require.NoError(t, m.Get(ctx, key, &result))
	require.Equal(t, val, result)
}

func TestMemoryKVS_GetNotFound(t *testing.T) {
	t.Parallel()
	m := NewMemoryKVS()
	var result []byte
	err := m.Get(t.Context(), "missing", &result)
	require.Error(t, err)
}

func TestMemoryService_ComputeID(t *testing.T) {
	t.Parallel()
	svc := NewMemoryService()
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

func TestMemoryService_ComputeID_NilState(t *testing.T) {
	t.Parallel()
	svc := NewMemoryService()
	_, err := svc.ComputeID(t.Context(), nil)
	require.Error(t, err)
}

func TestNewService_WithMemoryBackend(t *testing.T) {
	t.Parallel()
	svc := NewService(NewMemoryKVS())
	require.NotNil(t, svc)
	id, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	require.NotEmpty(t, id)
}

func TestMemoryService_ComputeID_Idempotent(t *testing.T) {
	t.Parallel()
	svc := NewMemoryService()
	id1, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	id2, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	require.Equal(t, id1, id2)
}

// mockServiceProvider implements token.ServiceProvider for testing
type mockServiceProvider struct{}

func (m *mockServiceProvider) GetService(v any) (any, error) {
	svc := NewMemoryService()

	return svc, nil
}

func TestGetService(t *testing.T) {
	t.Parallel()
	sp := &mockServiceProvider{}
	svc := GetService(sp)
	require.NotNil(t, svc)
	id, err := svc.ComputeID(t.Context(), "test-state")
	require.NoError(t, err)
	require.NotEmpty(t, id)
}
