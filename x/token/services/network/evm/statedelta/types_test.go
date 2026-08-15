/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statedelta

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateDeltaValidate(t *testing.T) {
	t.Run("valid transfer-like delta", func(t *testing.T) {
		d := &StateDelta{
			SpentRefs:    [][32]byte{{0x01}},
			Outputs:      []OutputToken{{TokenID: [32]byte{0x02}, TokenData: []byte("tok")}},
			MetadataKeys: [][32]byte{{0x03}},
			MetadataVals: [][]byte{[]byte("v")},
		}
		require.NoError(t, d.Validate())
	})

	t.Run("metadata length mismatch", func(t *testing.T) {
		d := &StateDelta{MetadataKeys: [][32]byte{{0x01}}, MetadataVals: nil}
		assert.Error(t, d.Validate())
	})

	t.Run("valid setup delta", func(t *testing.T) {
		d := &StateDelta{IsSetup: true, SetupParameters: []byte("pp")}
		require.NoError(t, d.Validate())
	})

	t.Run("setup delta without parameters", func(t *testing.T) {
		d := &StateDelta{IsSetup: true}
		assert.Error(t, d.Validate())
	})

	t.Run("setup delta with outputs", func(t *testing.T) {
		d := &StateDelta{IsSetup: true, SetupParameters: []byte("pp"), Outputs: []OutputToken{{}}}
		assert.Error(t, d.Validate())
	})

	t.Run("setup delta with metadata", func(t *testing.T) {
		// TokenState.sol's _applySetup reverts MalformedSetupDelta on non-empty metadataKeys too;
		// Validate must refuse this before an endorser ever signs it, not leave it to the contract.
		d := &StateDelta{
			IsSetup:         true,
			SetupParameters: []byte("pp"),
			MetadataKeys:    [][32]byte{{0x01}},
			MetadataVals:    [][]byte{[]byte("v")},
		}
		assert.Error(t, d.Validate())
	})

	t.Run("non-setup delta smuggling setup parameters", func(t *testing.T) {
		// SetupParameters is digest-covered: endorsers would sign bytes the contract ignores.
		d := &StateDelta{SpentRefs: [][32]byte{{0x01}}, SetupParameters: []byte("pp")}
		assert.Error(t, d.Validate())
	})

	t.Run("metadata keys sorted ascending is valid", func(t *testing.T) {
		d := &StateDelta{
			MetadataKeys: [][32]byte{{0x01}, {0x02}, {0x03}},
			MetadataVals: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
		}
		require.NoError(t, d.Validate())
	})

	t.Run("metadata keys unsorted", func(t *testing.T) {
		d := &StateDelta{
			MetadataKeys: [][32]byte{{0x02}, {0x01}},
			MetadataVals: [][]byte{[]byte("a"), []byte("b")},
		}
		assert.Error(t, d.Validate())
	})

	t.Run("metadata keys duplicated", func(t *testing.T) {
		d := &StateDelta{
			MetadataKeys: [][32]byte{{0x01}, {0x01}},
			MetadataVals: [][]byte{[]byte("a"), []byte("b")},
		}
		assert.Error(t, d.Validate())
	})

	// These are the regression tests for the DoS finding: a delta from an untrusted source (an
	// endorser's reply, before its signature is checked) must be rejected before Validate or the
	// EIP-712 digest does unbounded per-element work over it.
	t.Run("too many outputs", func(t *testing.T) {
		d := &StateDelta{Outputs: make([]OutputToken, maxDeltaEntries+1)}
		assert.ErrorContains(t, d.Validate(), "too many outputs")
	})

	t.Run("too many spent refs", func(t *testing.T) {
		d := &StateDelta{SpentRefs: make([][32]byte, maxDeltaEntries+1)}
		assert.ErrorContains(t, d.Validate(), "too many spent refs")
	})

	t.Run("too many metadata entries", func(t *testing.T) {
		d := &StateDelta{
			MetadataKeys: make([][32]byte, maxDeltaEntries+1),
			MetadataVals: make([][]byte, maxDeltaEntries+1),
		}
		assert.ErrorContains(t, d.Validate(), "too many metadata entries")
	})

	t.Run("output token data too large", func(t *testing.T) {
		d := &StateDelta{Outputs: []OutputToken{{TokenData: make([]byte, maxFieldBytes+1)}}}
		assert.ErrorContains(t, d.Validate(), "token data too large")
	})

	t.Run("metadata value too large", func(t *testing.T) {
		d := &StateDelta{
			MetadataKeys: [][32]byte{{0x01}},
			MetadataVals: [][]byte{make([]byte, maxFieldBytes+1)},
		}
		assert.ErrorContains(t, d.Validate(), "metadata value 0 too large")
	})

	t.Run("setup parameters too large", func(t *testing.T) {
		d := &StateDelta{IsSetup: true, SetupParameters: make([]byte, maxFieldBytes+1)}
		assert.ErrorContains(t, d.Validate(), "setup parameters too large")
	})

	t.Run("at the bound is still valid", func(t *testing.T) {
		d := &StateDelta{
			SpentRefs: make([][32]byte, maxDeltaEntries),
			Outputs:   []OutputToken{{TokenData: make([]byte, maxFieldBytes)}},
		}
		require.NoError(t, d.Validate())
	})
}
