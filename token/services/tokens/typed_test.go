/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerialization(t *testing.T) {
	raw := []byte("pineapple")
	wrappedToken, err := tokens.WrapWithType(0, raw)
	require.NoError(t, err)
	tok, err := tokens.UnmarshalTypedToken(wrappedToken)
	require.NoError(t, err)
	assert.Equal(t, driver.Type(0), tok.Type)
	assert.Equal(t, driver.Token(raw), tok.Token)
}
