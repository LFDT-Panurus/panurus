/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// testKeyHex is the well-known secp256k1 test key 1, whose address is reproducible with any Ethereum
// tooling.
const (
	testKeyHex     = "0000000000000000000000000000000000000000000000000000000000000001"
	testKeyAddress = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
)

func writeKey(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// TestLoadKeyAcceptsTheCommonFormats covers what the dev tooling actually writes: bare hex, a 0x
// prefix, and a trailing newline.
func TestLoadKeyAcceptsTheCommonFormats(t *testing.T) {
	for name, content := range map[string]string{
		"bare hex":         testKeyHex,
		"0x prefixed":      "0x" + testKeyHex,
		"trailing newline": testKeyHex + "\n",
		"surrounded":       "  0x" + testKeyHex + "  \n",
	} {
		t.Run(name, func(t *testing.T) {
			key, err := LoadKey(writeKey(t, content))
			require.NoError(t, err)
			assert.Equal(t, testKeyAddress, eip712.PubKeyToAddress(key.PubKey()).Hex())
		})
	}
}

func TestLoadKeyRejectsBadMaterial(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := LoadKey(filepath.Join(t.TempDir(), "absent"))
		require.Error(t, err)
	})

	t.Run("empty path", func(t *testing.T) {
		_, err := LoadKey("   ")
		require.Error(t, err)
	})

	t.Run("not hex", func(t *testing.T) {
		_, err := LoadKey(writeKey(t, "not a key"))
		require.Error(t, err)
	})

	t.Run("wrong length", func(t *testing.T) {
		_, err := LoadKey(writeKey(t, "0011223344"))
		require.Error(t, err)
	})

	t.Run("zero scalar", func(t *testing.T) {
		_, err := LoadKey(writeKey(t, "00000000000000000000000000000000000000000000000000000000000000"+"00"))
		require.Error(t, err, "the zero scalar has no valid public key")
	})
}

// TestLoadKeyForAddressCatchesMismatch is the configuration guard: pairing a key with the wrong
// address must fail at startup, not by producing signatures the contract rejects as an unknown signer.
func TestLoadKeyForAddressCatchesMismatch(t *testing.T) {
	path := writeKey(t, testKeyHex)

	t.Run("matching address loads", func(t *testing.T) {
		key, err := LoadKeyForAddress(path, testKeyAddress)
		require.NoError(t, err)
		require.NotNil(t, key)
	})

	t.Run("empty expectation skips the check", func(t *testing.T) {
		_, err := LoadKeyForAddress(path, "")
		require.NoError(t, err)
	})

	t.Run("mismatched address is refused", func(t *testing.T) {
		_, err := LoadKeyForAddress(path, "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "derives address")
	})

	t.Run("malformed expectation is refused", func(t *testing.T) {
		_, err := LoadKeyForAddress(path, "not-an-address")
		require.Error(t, err)
	})
}
