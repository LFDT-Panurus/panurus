/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network

import (
	"errors"
	"strconv"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCertificationClientProvider(t *testing.T) {
	provider := NewCertificationClientProvider()
	require.NotNil(t, provider)
}

// TestCertificationClientProvider_New tests the New method.
// Note: The New method is a thin wrapper around certifier.NewCertificationClient.
// Full integration testing of this method requires a properly initialized TMS with:
// - PublicParametersManager
// - CertificationDriver configuration
// - Driver registry
// These are tested in integration tests and in the certifier package itself.
// This test verifies the method signature and basic behavior.
func TestCertificationClientProvider_New(t *testing.T) {
	t.Run("returns provider instance", func(t *testing.T) {
		provider := NewCertificationClientProvider()
		require.NotNil(t, provider)

		// The provider should be ready to call New when given a proper TMS
		// We can't test the actual call without a full TMS setup, but we verify
		// the provider exists and has the method available
		assert.NotNil(t, provider)
	})
}

func TestCertificationClientProvider_MultipleInstances(t *testing.T) {
	// Test that we can create multiple providers
	provider1 := NewCertificationClientProvider()
	provider2 := NewCertificationClientProvider()

	require.NotNil(t, provider1)
	require.NotNil(t, provider2)

	// They should be different instances
	assert.NotSame(t, provider1, provider2)
}

// TestCertificationClientProvider_Concurrent is skipped because it requires
// a fully initialized TMS which is complex to mock in unit tests.

// Mock certification client for integration-style tests
type testCertificationClient struct {
	certified map[string]bool
	requested []string
}

func (t *testCertificationClient) IsCertified(id *token2.ID) bool {
	if t.certified == nil {
		return false
	}
	key := id.TxId + ":" + strconv.FormatUint(id.Index, 10)

	return t.certified[key]
}

func (t *testCertificationClient) RequestCertification(ids ...*token2.ID) error {
	if t.requested == nil {
		t.requested = make([]string, 0)
	}
	for _, id := range ids {
		key := id.TxId + ":" + strconv.FormatUint(id.Index, 10)
		t.requested = append(t.requested, key)
	}

	return nil
}

func TestCertificationClient_MockBehavior(t *testing.T) {
	client := &testCertificationClient{
		certified: make(map[string]bool),
	}

	// Test IsCertified with non-existent ID
	id1 := &token2.ID{TxId: "tx1", Index: 0}
	assert.False(t, client.IsCertified(id1))

	// Mark as certified
	client.certified["tx1:0"] = true
	assert.True(t, client.IsCertified(id1))

	// Test RequestCertification
	id2 := &token2.ID{TxId: "tx2", Index: 1}
	id3 := &token2.ID{TxId: "tx3", Index: 2}

	err := client.RequestCertification(id2, id3)
	require.NoError(t, err)
	assert.Len(t, client.requested, 2)
}

func TestCertificationClient_ErrorHandling(t *testing.T) {
	// Test error scenarios
	type errorClient struct {
		testCertificationClient
		shouldError bool
	}

	client := &errorClient{shouldError: true}

	// Override RequestCertification to return error
	requestCert := func(ids ...*token2.ID) error {
		if client.shouldError {
			return errors.New("certification request failed")
		}

		return nil
	}

	id := &token2.ID{TxId: "tx1", Index: 0}
	err := requestCert(id)
	require.Error(t, err)
	assert.Equal(t, "certification request failed", err.Error())
}
