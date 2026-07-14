/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core

import (
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
)

// TestDriverIdentifier tests the creation of a TokenDriverIdentifier from a name and version.
func TestDriverIdentifier(t *testing.T) {
	tests := []struct {
		name     driver.TokenDriverName
		version  driver.TokenDriverVersion
		expected string
	}{
		{"test", 0, "test.v0"},
		{"test", 1, "test.v1"},
		{"abc", 10, "abc.v10"},
		{"", 5, ".v5"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s.v%d", tt.name, tt.version), func(t *testing.T) {
			id := DriverIdentifier(tt.name, tt.version)
			assert.Equal(t, TokenDriverIdentifier(tt.expected), id)
		})
	}
}

// TestDriverIdentifierFromPP tests the extraction of a TokenDriverIdentifier from a PublicParameters instance.
func TestDriverIdentifierFromPP(t *testing.T) {
	tests := []struct {
		name     driver.TokenDriverName
		version  driver.TokenDriverVersion
		expected string
	}{
		{"test", 1, "test.v1"},
		{"zkatdlog", 2, "zkatdlog.v2"},
	}

	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			pp := &mock.PublicParameters{}
			pp.TokenDriverNameReturns(tt.name)
			pp.TokenDriverVersionReturns(tt.version)
			id := DriverIdentifierFromPP(pp)
			assert.Equal(t, TokenDriverIdentifier(tt.expected), id)
		})
	}
}
