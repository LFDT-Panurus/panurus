/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Finding: the legacy string fold in DecodeIdentity has no "policy" case, so a
// string-typed "policy" identity decodes without error but with IsInt=false,
// and UnmarshalTypedIdentity then rejects it.
func TestDecodeIdentity_LegacyPolicyStringNotFolded(t *testing.T) {
	raw := marshal.Encode(marshal.Result{
		Str:  driver.PolicyIdentityTypeString,
		Data: []byte("an_identity"),
	})

	res, err := marshal.DecodeIdentity(raw)
	require.NoError(t, err)
	assert.False(t, res.IsInt)
	assert.Equal(t, int32(0), res.Int32)
	assert.Equal(t, driver.PolicyIdentityTypeString, res.Str)
	assert.Equal(t, []byte("an_identity"), res.Data)

	_, err = identity.UnmarshalTypedIdentity(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type not recognized")

	// The integer encoding of the policy type decodes normally.
	ti, err := identity.UnmarshalTypedIdentity(marshal.EncodeIdentity(driver.PolicyIdentityType, []byte("an_identity")))
	require.NoError(t, err)
	assert.Equal(t, driver.PolicyIdentityType, ti.Type)
}
