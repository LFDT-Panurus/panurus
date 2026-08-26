/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSigner is a driver.Signer that does nothing; tests only need a non-nil value.
type stubSigner struct{}

func (stubSigner) Sign([]byte) ([]byte, error) { return []byte("sig"), nil }

// stubProbeFreeDeserializer is a ProbeFreeSignerDeserializer that always succeeds, so a test can
// tell a successful route from one that was torn down.
type stubProbeFreeDeserializer struct{}

func (stubProbeFreeDeserializer) DeserializeSigner(_ context.Context, _ []byte) (driver.Signer, error) {
	return stubSigner{}, nil
}

func (stubProbeFreeDeserializer) DeserializeSignerNoProbe(_ context.Context, _ []byte) (driver.Signer, error) {
	return stubSigner{}, nil
}

// TestSignerRouter_Unregister covers the byConfID teardown reported in issue #2073: byConfID had no
// eviction or teardown hook, so every registered conf_id pinned its KeyManager - and everything that
// KeyManager owns - for the lifetime of the router.
func TestSignerRouter_Unregister(t *testing.T) {
	router := identity.NewSignerRouter(nil)
	router.Register("conf-id-1", erroringProbeFreeDeserializer{})
	router.Register("conf-id-2", erroringProbeFreeDeserializer{})
	require.Equal(t, 2, router.Len())

	router.Unregister("conf-id-1")
	assert.Equal(t, 1, router.Len())

	// Unknown and repeated conf_ids are ignored.
	router.Unregister("conf-id-1", "never-registered")
	assert.Equal(t, 1, router.Len())

	// No conf_ids at all is a no-op, not a full wipe.
	router.Unregister()
	assert.Equal(t, 1, router.Len())

	router.Unregister("conf-id-2")
	assert.Equal(t, 0, router.Len())
}

// TestSignerRouter_UnregisteredConfIDStopsRouting proves the behavioural consequence: once a conf_id
// is unregistered, Resolve reports ok=false for its identities so callers fall back to the probing
// deserializer instead of being handed a signer from a released KeyManager.
func TestSignerRouter_UnregisteredConfIDStopsRouting(t *testing.T) {
	router := identity.NewSignerRouter(nil)
	router.Register("conf-id-1", stubProbeFreeDeserializer{})

	wrapped, err := identity.WrapWithType(idemix.IdentityType, []byte("raw"))
	require.NoError(t, err)

	resolver := &idmock.ConfIDResolver{}
	resolver.GetConfIDReturns("conf-id-1", nil)
	router.SetConfIDResolver(resolver)

	signer, ok := router.Resolve(t.Context(), wrapped)
	require.True(t, ok)
	require.NotNil(t, signer)

	router.Unregister("conf-id-1")

	signer, ok = router.Resolve(t.Context(), wrapped)
	assert.False(t, ok)
	assert.Nil(t, signer)
}

// TestSignerRouter_Close drops every registration and is idempotent.
func TestSignerRouter_Close(t *testing.T) {
	router := identity.NewSignerRouter(nil)
	router.Register("conf-id-1", erroringProbeFreeDeserializer{})
	router.Register("conf-id-2", erroringProbeFreeDeserializer{})
	require.Equal(t, 2, router.Len())

	router.Close()
	assert.Equal(t, 0, router.Len())

	router.Close()
	assert.Equal(t, 0, router.Len())

	// The router is reusable after Close: it holds no other state that Close invalidates.
	router.Register("conf-id-3", erroringProbeFreeDeserializer{})
	assert.Equal(t, 1, router.Len())
}
