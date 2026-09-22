/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp

import (
	"strings"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testVaultPath = "kv1/data/panurus/"

// TestNormalizeIDIsInjective asserts that composite keys that differ in any way map to
// distinct Vault paths, and that each path maps back to the composite key it came from.
// Without per-component escaping, ("", ["1"]) and ("1", nil) both collapse to <path>/1.
func TestNormalizeIDIsInjective(t *testing.T) {
	store, err := NewWithClient(nil, testVaultPath)
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		objectType string
		attrs      []string
		path       string
	}{
		{"plain", "k", []string{"1"}, "k/1"},
		{"no attributes", "1", nil, "1"},
		{"empty object type", "", []string{"1"}, "%/1"},
		{"empty attribute", "k", []string{"", "1"}, "k/%/1"},
		{"trailing empty attribute", "k", []string{"1", ""}, "k/1/%"},
		{"separator in attribute", "walletDB", []string{"a/b"}, "walletDB/a%2Fb"},
		{"separator in object type", "a/b", []string{"1"}, "a%2Fb/1"},
		{"percent in attribute", "k", []string{"100%"}, "k/100%25"},
		{"already escaped attribute", "k", []string{"a%2Fb"}, "k/a%252Fb"},
		{"dot attribute", "k", []string{"."}, "k/%2E"},
		{"relative parent attribute", "k", []string{".."}, "k/%2E%2E"},
		{"base64 attribute", "idb", []string{"MHg=+/abc"}, "idb/MHg=+%2Fabc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := kvs.CreateCompositeKey(tc.objectType, tc.attrs)
			require.NoError(t, err)

			normalized := store.NormalizeID(key)
			assert.Equal(t, testVaultPath+tc.path, normalized)

			// the path must not leak out of the store's prefix
			assert.NotContains(t, normalized, "//")
			for component := range strings.SplitSeq(strings.TrimPrefix(normalized, testVaultPath), "/") {
				assert.NotContains(t, []string{"", ".", ".."}, component)
			}

			deNormalized, err := store.deNormalizeID(normalized)
			require.NoError(t, err)
			assert.Equal(t, key, deNormalized)
		})
	}
}

// TestDeNormalizeIDTrailingSeparator asserts that the trailing separator Vault reports for a
// path that has children is not mistaken for a trailing empty component.
func TestDeNormalizeIDTrailingSeparator(t *testing.T) {
	store, err := NewWithClient(nil, testVaultPath)
	require.NoError(t, err)

	key, err := kvs.CreateCompositeKey("walletDB", []string{"tms", "0", "hash"})
	require.NoError(t, err)

	deNormalized, err := store.deNormalizeID(store.NormalizeID(key) + "/")
	require.NoError(t, err)
	assert.Equal(t, key, deNormalized)
}

// FuzzNormalizeIDRoundTrip checks that mapping a composite key onto a Vault path and back
// returns the original key. A round trip implies the mapping is injective, so no two distinct
// composite keys can ever address the same Vault secret.
func FuzzNormalizeIDRoundTrip(f *testing.F) {
	f.Add("k", "1", "2")
	f.Add("", "", "")
	f.Add("", "1", "")
	f.Add("1", "", "")
	f.Add("walletDB", "a/b", "")
	f.Add("idb", "MHg=+/abc", "%2F")
	f.Add("k", ".", "..")
	f.Add("k", "/", "//")
	f.Add("k", "%", "%%")
	f.Add("\x00", "\x00", "\x00")
	f.Add("ü", " ", "\t")

	store, err := NewWithClient(nil, testVaultPath)
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, objectType, attr1, attr2 string) {
		key, err := kvs.CreateCompositeKey(objectType, []string{attr1, attr2})
		if err != nil {
			// not a valid composite key, nothing to normalize
			t.Skip()
		}

		normalized := store.NormalizeID(key)
		require.True(t, strings.HasPrefix(normalized, testVaultPath))

		// no component may be empty or resolve away: the Vault client cleans the request path
		for component := range strings.SplitSeq(strings.TrimPrefix(normalized, testVaultPath), "/") {
			require.NotContains(t, []string{"", ".", ".."}, component)
		}

		deNormalized, err := store.deNormalizeID(normalized)
		require.NoError(t, err)
		require.Equal(t, key, deNormalized)
	})
}
