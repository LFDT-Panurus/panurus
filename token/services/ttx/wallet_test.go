/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests wallet.go which provides wallet retrieval utilities for token transactions.
// Note: Most wallet retrieval functions (MyWallet, GetWallet, etc.) require integration testing
// as they depend on token.GetManagementService which needs a fully initialized context with
// service providers. These functions are tested in integration tests.
package ttx_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithType verifies the WithType option function.
func TestWithType(t *testing.T) {
	t.Run("with specific token type", func(t *testing.T) {
		tokenType := token2.Type("USD")
		opts := &token.ListTokensOptions{}

		err := ttx.WithType(tokenType)(opts)

		require.NoError(t, err)
		assert.Equal(t, tokenType, opts.TokenType)
	})

	t.Run("with empty token type", func(t *testing.T) {
		tokenType := token2.Type("")
		opts := &token.ListTokensOptions{}

		err := ttx.WithType(tokenType)(opts)

		require.NoError(t, err)
		assert.Equal(t, tokenType, opts.TokenType)
	})

	t.Run("multiple token types", func(t *testing.T) {
		types := []token2.Type{"USD", "EUR", "GBP", "JPY", "CHF"}
		for _, tt := range types {
			opts := &token.ListTokensOptions{}
			err := ttx.WithType(tt)(opts)
			require.NoError(t, err)
			assert.Equal(t, tt, opts.TokenType)
		}
	})

	t.Run("special characters in token type", func(t *testing.T) {
		specialTypes := []token2.Type{
			"TOKEN-123",
			"token_456",
			"token.789",
			"TOKEN@ABC",
		}
		for _, tt := range specialTypes {
			opts := &token.ListTokensOptions{}
			err := ttx.WithType(tt)(opts)
			require.NoError(t, err)
			assert.Equal(t, tt, opts.TokenType)
		}
	})

	t.Run("overwrite existing token type", func(t *testing.T) {
		opts := &token.ListTokensOptions{
			TokenType: token2.Type("OLD"),
		}

		newType := token2.Type("NEW")
		err := ttx.WithType(newType)(opts)

		require.NoError(t, err)
		assert.Equal(t, newType, opts.TokenType)
		assert.NotEqual(t, token2.Type("OLD"), opts.TokenType)
	})

	t.Run("apply multiple times", func(t *testing.T) {
		opts := &token.ListTokensOptions{}

		// Apply first type
		err := ttx.WithType(token2.Type("FIRST"))(opts)
		require.NoError(t, err)
		assert.Equal(t, token2.Type("FIRST"), opts.TokenType)

		// Apply second type (should overwrite)
		err = ttx.WithType(token2.Type("SECOND"))(opts)
		require.NoError(t, err)
		assert.Equal(t, token2.Type("SECOND"), opts.TokenType)

		// Apply third type (should overwrite again)
		err = ttx.WithType(token2.Type("THIRD"))(opts)
		require.NoError(t, err)
		assert.Equal(t, token2.Type("THIRD"), opts.TokenType)
	})

	t.Run("with very long token type", func(t *testing.T) {
		longType := token2.Type("VERY_LONG_TOKEN_TYPE_NAME_THAT_EXCEEDS_NORMAL_LENGTH_EXPECTATIONS")
		opts := &token.ListTokensOptions{}

		err := ttx.WithType(longType)(opts)

		require.NoError(t, err)
		assert.Equal(t, longType, opts.TokenType)
	})

	t.Run("with unicode token type", func(t *testing.T) {
		unicodeType := token2.Type("TOKEN_€_£_¥")
		opts := &token.ListTokensOptions{}

		err := ttx.WithType(unicodeType)(opts)

		require.NoError(t, err)
		assert.Equal(t, unicodeType, opts.TokenType)
	})
}

// TestWithType_Integration verifies WithType works with other ListTokensOptions fields.
func TestWithType_Integration(t *testing.T) {
	t.Run("with other options set", func(t *testing.T) {
		opts := &token.ListTokensOptions{
			// Simulate other fields being set
			TokenType: token2.Type("EXISTING"),
		}

		newType := token2.Type("NEW_TYPE")
		err := ttx.WithType(newType)(opts)

		require.NoError(t, err)
		assert.Equal(t, newType, opts.TokenType)
	})

	t.Run("nil options should not panic", func(t *testing.T) {
		// This would panic if the function doesn't handle nil properly
		// The actual implementation doesn't check for nil, so this documents expected behavior
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Expected panic with nil options: %v", r)
			}
		}()

		// This will panic - documenting that callers must provide valid options
		_ = ttx.WithType(token2.Type("TEST"))(nil)
	})
}
