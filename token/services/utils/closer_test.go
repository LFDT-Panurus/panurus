/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests closer.go which provides utility functions for ignoring errors
// from cleanup operations. Tests verify that IgnoreError and IgnoreErrorWithOneArg
// properly execute functions while discarding their error returns.
package utils

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
)

// TestIgnoreError verifies that IgnoreError silently ignores errors
func TestIgnoreError(t *testing.T) {
	t.Parallel()

	called := false
	fn := func() error {
		called = true

		return errors.New("test error")
	}

	// Should not panic even though fn returns an error
	IgnoreError(fn)

	assert.True(t, called, "function should have been called")
}

// TestIgnoreError_NoError verifies IgnoreError works with functions that don't error
func TestIgnoreError_NoError(t *testing.T) {
	t.Parallel()

	called := false
	fn := func() error {
		called = true

		return nil
	}

	IgnoreError(fn)

	assert.True(t, called, "function should have been called")
}

// TestIgnoreErrorWithOneArg verifies that IgnoreErrorWithOneArg silently ignores errors
func TestIgnoreErrorWithOneArg(t *testing.T) {
	t.Parallel()

	called := false
	var receivedArg string

	fn := func(arg string) error {
		called = true
		receivedArg = arg

		return errors.New("test error")
	}

	// Should not panic even though fn returns an error
	IgnoreErrorWithOneArg(fn, "test-arg")

	assert.True(t, called, "function should have been called")
	assert.Equal(t, "test-arg", receivedArg, "function should receive the argument")
}

// TestIgnoreErrorWithOneArg_NoError verifies IgnoreErrorWithOneArg works with functions that don't error
func TestIgnoreErrorWithOneArg_NoError(t *testing.T) {
	t.Parallel()

	called := false
	var receivedArg int

	fn := func(arg int) error {
		called = true
		receivedArg = arg

		return nil
	}

	IgnoreErrorWithOneArg(fn, 42)

	assert.True(t, called, "function should have been called")
	assert.Equal(t, 42, receivedArg, "function should receive the argument")
}
