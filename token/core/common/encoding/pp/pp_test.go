/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerialization(t *testing.T) {
	// Marshal

	// check failures
	_, err := Marshal(nil)
	require.Error(t, err)
	require.EqualError(t, err, "nil public parameters")

	// success
	pp := &pp.PublicParameters{
		Identifier: "pineapple",
		Raw:        []byte{1, 2, 3},
	}
	res, err := Marshal(pp)
	require.NoError(t, err)

	// Unmarshall

	// success
	pp2, err := Unmarshal(res)
	require.NoError(t, err)
	assert.Equal(t, pp, pp2)

	// failure
	_, err = Unmarshal([]byte{})
	require.Error(t, err)

	_, err = Unmarshal(nil)
	require.Error(t, err)

	_, err = Unmarshal([]byte{1, 2, 3})
	require.Error(t, err)
}
