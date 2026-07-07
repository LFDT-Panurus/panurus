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
}
