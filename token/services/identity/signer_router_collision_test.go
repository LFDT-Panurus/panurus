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
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// labelledSigner identifies which KeyManager produced it.
type labelledSigner struct {
	label string
}

func (s *labelledSigner) Sign([]byte) ([]byte, error) { return []byte(s.label), nil }

// labelledDeserializer stands in for a KeyManager. It hands back a signer tagged with its own
// label, so a test can tell which of several registered KeyManagers a resolution was routed to.
// It implements the probe-free path, which is the one SignerRouter prefers and the one whose
// contract a conf_id collision violates.
type labelledDeserializer struct {
	label string
	calls int
}

func (d *labelledDeserializer) DeserializeSigner(context.Context, []byte) (driver.Signer, error) {
	d.calls++

	return &labelledSigner{label: d.label}, nil
}

func (d *labelledDeserializer) DeserializeSignerNoProbe(context.Context, []byte) (driver.Signer, error) {
	d.calls++

	return &labelledSigner{label: d.label}, nil
}

// TestSignerRouter_CollidingConfigurationsRouteIndependently is the impact described in #2070.
//
// Two distinct identity configurations concatenate to the same string under the pre-fix
// unescaped conf_id encoding. LocalMembership registers each with its own KeyManager under
// config.UniqueID(); with a collision the second Register overwrites the first (they share a
// key), so an identity belonging to the first configuration resolves to the second
// configuration's KeyManager. Because SignerRouter uses DeserializeSignerNoProbe, whose contract
// is that the caller already knows the signer belongs to that KeyManager, nothing detects the
// mismatch here — it surfaces later as invalid signatures.
func TestSignerRouter_CollidingConfigurationsRouteIndependently(t *testing.T) {
	confA := driver.IdentityConfiguration{ID: "a@b", Type: "c", URL: "d"}
	confB := driver.IdentityConfiguration{ID: "a", Type: "b@c", URL: "d"}

	// precondition: these are the tuples that collided before the fix
	require.Equal(t, confA.ID+"@"+confA.Type+"@"+confA.URL, confB.ID+"@"+confB.Type+"@"+confB.URL,
		"precondition: the two configurations collide under the legacy encoding")
	require.NotEqual(t, confA.UniqueID(), confB.UniqueID(), "conf_ids must differ for routing to be possible")

	kmA := &labelledDeserializer{label: "A"}
	kmB := &labelledDeserializer{label: "B"}

	router := identity.NewSignerRouter(nil)
	router.Register(confA.UniqueID(), kmA)
	router.Register(confB.UniqueID(), kmB)

	idA, err := identity.WrapWithType(identity.Type(99), []byte("identity-of-A"))
	require.NoError(t, err)
	idB, err := identity.WrapWithType(identity.Type(99), []byte("identity-of-B"))
	require.NoError(t, err)

	// resolve an identity bound under configuration A: it must reach A's KeyManager, not B's
	resolver := &idmock.ConfIDResolver{}
	resolver.GetConfIDReturns(confA.UniqueID(), nil)
	router.SetConfIDResolver(resolver)

	signer, ok := router.Resolve(context.Background(), idA)
	require.True(t, ok, "routing must succeed for a registered conf_id")
	sig, err := signer.Sign(nil)
	require.NoError(t, err)
	assert.Equal(t, "A", string(sig), "identity of configuration A was routed to the wrong KeyManager")
	assert.Equal(t, 0, kmB.calls, "configuration B's KeyManager must not be touched")

	// and symmetrically for configuration B
	resolver.GetConfIDReturns(confB.UniqueID(), nil)
	signer, ok = router.Resolve(context.Background(), idB)
	require.True(t, ok)
	sig, err = signer.Sign(nil)
	require.NoError(t, err)
	assert.Equal(t, "B", string(sig), "identity of configuration B was routed to the wrong KeyManager")
	assert.Equal(t, 1, kmA.calls, "configuration A's KeyManager must not be touched again")
}
