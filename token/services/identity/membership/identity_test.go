/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalIdentity_String(t *testing.T) {
	t.Run("Anonymous", func(t *testing.T) {
		id := &LocalIdentity{
			Name:         "alice",
			EnrollmentID: "alice_enroll",
			Default:      true,
			Anonymous:    true,
			Remote:       false,
		}
		assert.Equal(t, "{alice@alice_enroll-true-true-false}", id.String())
	})

	t.Run("NotAnonymous_Error", func(t *testing.T) {
		id := &LocalIdentity{
			Name:         "bob",
			EnrollmentID: "bob_enroll",
			Default:      false,
			Anonymous:    false,
			Remote:       true,
			GetIdentity: func(ctx context.Context, auditInfo []byte) (driver.Identity, []byte, error) {
				return nil, nil, errors.New("failed to get identity")
			},
		}
		assert.Equal(t, "failed to get identity", id.String())
	})

	t.Run("NotAnonymous_Success", func(t *testing.T) {
		id := &LocalIdentity{
			Name:         "charlie",
			EnrollmentID: "charlie_enroll",
			Default:      false,
			Anonymous:    false,
			Remote:       false,
			GetIdentity: func(ctx context.Context, auditInfo []byte) (driver.Identity, []byte, error) {
				return driver.Identity("charlie_identity"), nil, nil
			},
		}
		assert.Contains(t, id.String(), "{charlie@charlie_enroll-false-false-false}[")
	})
}

func TestIdentityInfo(t *testing.T) {
	localID := &LocalIdentity{
		Name:         "dave",
		EnrollmentID: "dave_enroll",
		Default:      true,
		Anonymous:    true,
		Remote:       true,
	}

	expectedIdentity := driver.Identity("dave_identity")
	expectedAudit := []byte("audit")

	getIdentity := func(ctx context.Context) (driver.Identity, []byte, error) {
		return expectedIdentity, expectedAudit, nil
	}

	info := NewIdentityInfo(localID, getIdentity)

	assert.Equal(t, "dave", info.ID())
	assert.Equal(t, "dave_enroll", info.EnrollmentID())
	assert.True(t, info.Remote())
	assert.True(t, info.Anonymous())

	id, audit, err := info.Get(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expectedIdentity, id)
	assert.Equal(t, expectedAudit, audit)
}
