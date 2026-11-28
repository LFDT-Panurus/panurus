/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Finding: EscrowAuth.OwnerType returns the outer policy type and the inner
// payload of the TypedIdentity, not a component identity.
func TestEscrowAuth_OwnerType_ReturnsOuterPolicyType(t *testing.T) {
	tok := makePolicyToken(t, "$0 OR $1", []byte("alice"), []byte("bob"))
	ti, err := identity.UnmarshalTypedIdentity(tok.Owner)
	require.NoError(t, err)

	typ, inner, err := boolpolicy.NewEscrowAuth(nil).OwnerType(tok.Owner)
	require.NoError(t, err)
	assert.Equal(t, driver.PolicyIdentityType, typ)
	assert.Equal(t, []byte(ti.Identity), inner)
}

// Finding: EscrowAuth.OwnerType fails on bytes that are not a TypedIdentity.
func TestEscrowAuth_OwnerType_InvalidBytes(t *testing.T) {
	typ, inner, err := boolpolicy.NewEscrowAuth(nil).OwnerType([]byte("garbage"))
	require.Error(t, err)
	assert.Equal(t, driver.ZeroIdentityType, typ)
	assert.Nil(t, inner)
}
