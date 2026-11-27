/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEIDRHDeserializer_MultisigResolvesCommonEID pins the driver wiring of
// #2660: a multisig whose members share one enrollment ID resolves to it, and
// one whose members span enrollment IDs resolves to none.
func TestEIDRHDeserializer_MultisigResolvesCommonEID(t *testing.T) {
	member := func(name, eid string) ([]byte, []byte) {
		id, err := identity.WrapWithType(x509.IdentityType, []byte(name))
		require.NoError(t, err)
		ai, err := (&x509.AuditInfo{EID: eid, RH: []byte("rh-" + eid)}).Bytes()
		require.NoError(t, err)

		return id, ai
	}
	m0, ai0 := member("cert-zero", "alice")
	m1, ai1 := member("cert-one", "alice")
	m2, ai2 := member("cert-two", "bob")
	eidrh := driver.NewEIDRHDeserializer()

	shared, err := multisig.WrapIdentities(m0, m1)
	require.NoError(t, err)
	sharedInfo, err := multisig.WrapAuditInfo([][]byte{ai0, ai1})
	require.NoError(t, err)
	eid, err := eidrh.GetEnrollmentID(t.Context(), shared, sharedInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", eid)

	spanning, err := multisig.WrapIdentities(m0, m2)
	require.NoError(t, err)
	spanningInfo, err := multisig.WrapAuditInfo([][]byte{ai0, ai2})
	require.NoError(t, err)
	eid, err = eidrh.GetEnrollmentID(t.Context(), spanning, spanningInfo)
	require.NoError(t, err)
	assert.Empty(t, eid)
}
