/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewConfiguration verifies Configuration constructor
func TestNewConfiguration(t *testing.T) {
	mockCM := &mock.Configuration{}
	config := NewConfiguration(mockCM)

	assert.NotNil(t, config)
	assert.Equal(t, mockCM, config.cm)
}

// TestConfiguration_IsSet verifies IsSet delegates to underlying configuration
func TestConfiguration_IsSet(t *testing.T) {
	mockCM := &mock.Configuration{}
	config := NewConfiguration(mockCM)

	mockCM.IsSetReturns(true)
	result := config.IsSet("test.key")

	assert.True(t, result)
	assert.Equal(t, 1, mockCM.IsSetCallCount())
	assert.Equal(t, "test.key", mockCM.IsSetArgsForCall(0))
}

// TestConfiguration_IsSet_False verifies IsSet returns false when key not found
func TestConfiguration_IsSet_False(t *testing.T) {
	mockCM := &mock.Configuration{}
	config := NewConfiguration(mockCM)

	mockCM.IsSetReturns(false)
	result := config.IsSet("missing.key")

	assert.False(t, result)
}

// TestConfiguration_UnmarshalKey_Success verifies successful unmarshaling
func TestConfiguration_UnmarshalKey_Success(t *testing.T) {
	mockCM := &mock.Configuration{}
	config := NewConfiguration(mockCM)

	type TestStruct struct {
		Value string
	}

	var target TestStruct
	mockCM.UnmarshalKeyReturns(nil)

	err := config.UnmarshalKey("test.key", &target)

	require.NoError(t, err)
	assert.Equal(t, 1, mockCM.UnmarshalKeyCallCount())
	key, val := mockCM.UnmarshalKeyArgsForCall(0)
	assert.Equal(t, "test.key", key)
	assert.Equal(t, &target, val)
}

// TestConfiguration_UnmarshalKey_Error verifies error handling during unmarshaling
func TestConfiguration_UnmarshalKey_Error(t *testing.T) {
	mockCM := &mock.Configuration{}
	config := NewConfiguration(mockCM)

	var target any
	expectedErr := errors.New("unmarshal error")
	mockCM.UnmarshalKeyReturns(expectedErr)

	err := config.UnmarshalKey("test.key", &target)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}
