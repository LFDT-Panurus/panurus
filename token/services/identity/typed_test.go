/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypedIdentity_Bytes(t *testing.T) {
	ti := identity.TypedIdentity{
		Type:     identity.Type(5),
		Identity: driver.Identity("testIdentity"),
	}

	bytes, err := ti.Bytes()
	require.NoError(t, err)
	assert.NotNil(t, bytes)
}

func TestUnmarshalTypedIdentity(t *testing.T) {
	ti := identity.TypedIdentity{
		Type:     identity.Type(5),
		Identity: driver.Identity("testIdentity"),
	}

	bytes, err := ti.Bytes()
	require.NoError(t, err)

	unmarshaledTI, err := identity.UnmarshalTypedIdentity(bytes)
	require.NoError(t, err)
	assert.Equal(t, ti, *unmarshaledTI)
}

func TestUnmarshalTypedIdentity_Error(t *testing.T) {
	invalidBytes := []byte{0xff, 0xfe, 0xfd}

	_, err := identity.UnmarshalTypedIdentity(invalidBytes)
	require.Error(t, err)
}

func TestWrapWithType(t *testing.T) {
	idType := identity.Type(5)
	id := driver.Identity("testIdentity")

	wrappedID, err := identity.WrapWithType(idType, id)
	require.NoError(t, err)
	assert.NotNil(t, wrappedID)

	unmarshaledTI, err := identity.UnmarshalTypedIdentity(wrappedID)
	require.NoError(t, err)
	assert.Equal(t, idType, unmarshaledTI.Type)
	assert.Equal(t, id, unmarshaledTI.Identity)
}

func TestTypeToString(t *testing.T) {
	// The numeric values are persisted in the owner field of tokens, they must never change.
	for _, tc := range []struct {
		idType   driver.IdentityType
		expected int32
		str      string
	}{
		{driver.IdemixIdentityType, 1, "idemix"},
		{driver.X509IdentityType, 2, "x509"},
		{driver.IdemixNymIdentityType, 3, "idemixnym"},
		{driver.HTLCScriptIdentityType, 4, "htlc"},
		{driver.MultiSigIdentityType, 5, "multisig"},
		{driver.LazerIdentityType, 7, "lazer"},
	} {
		assert.Equal(t, tc.expected, tc.idType, tc.str)
		assert.Equal(t, tc.str, identity.TypeToString(tc.idType))
	}
	assert.Equal(t, "Type (42)", identity.TypeToString(42))
}
