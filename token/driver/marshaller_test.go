/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"testing"

	driver "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
)

// Test ToTokenID function
func TestToTokenID(t *testing.T) {
	tests := []struct {
		name     string
		input    *driver.TokenID
		expected *token.ID
	}{
		{
			name: "Valid conversion",
			input: &driver.TokenID{
				TxId:  "test-tx-id",
				Index: 123,
			},
			expected: &token.ID{
				TxId:  "test-tx-id",
				Index: 123,
			},
		},
		{
			name:     "Nil input",
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToTokenID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test ToProtoIdentitySlice function
func TestToProtoIdentitySlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []Identity
		expected []*driver.Identity
	}{
		{
			name: "Valid conversion",
			input: []Identity{
				[]byte("identity1"),
				[]byte("identity2"),
			},
			expected: []*driver.Identity{
				{Raw: []byte("identity1")},
				{Raw: []byte("identity2")},
			},
		},
		{
			name:     "Empty input",
			input:    []Identity{},
			expected: []*driver.Identity{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToProtoIdentitySlice(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test FromProtoIdentitySlice function
func TestFromProtoIdentitySlice(t *testing.T) {
	tests := []struct {
		name     string
		input    []*driver.Identity
		expected []Identity
	}{
		{
			name: "Valid conversion",
			input: []*driver.Identity{
				{Raw: []byte("identity1")},
				{Raw: []byte("identity2")},
			},
			expected: []Identity{
				[]byte("identity1"),
				[]byte("identity2"),
			},
		},
		{
			name:     "Empty input",
			input:    []*driver.Identity{},
			expected: []Identity{},
		},
		{
			name: "Nil elements in input",
			input: []*driver.Identity{
				nil,
				{Raw: []byte("identity2")},
			},
			expected: []Identity{
				nil,
				[]byte("identity2"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FromProtoIdentitySlice(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test ToIdentity function
func TestToIdentity(t *testing.T) {
	tests := []struct {
		name     string
		input    *driver.Identity
		expected Identity
	}{
		{
			name: "Valid conversion",
			input: &driver.Identity{
				Raw: []byte("test-identity"),
			},
			expected: []byte("test-identity"),
		},
		{
			name:     "Nil input",
			input:    nil,
			expected: nil,
		},
		{
			name: "Nil Raw field",
			input: &driver.Identity{
				Raw: nil,
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToIdentity(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
