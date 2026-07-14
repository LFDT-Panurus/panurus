/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
)

// TestAuthorization_Struct verifies Authorization struct wraps driver.Authorization correctly
func TestAuthorization_Struct(t *testing.T) {
	mockAuth := &mock.Authorization{}
	auth := &Authorization{Authorization: mockAuth}

	assert.NotNil(t, auth)
	assert.Equal(t, mockAuth, auth.Authorization)
}
