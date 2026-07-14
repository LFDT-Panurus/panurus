/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithUniqueID(t *testing.T) {
	opt := WithUniqueID("my-id")

	opts := &token.IssueOptions{
		Attributes: make(map[string]any),
	}
	err := opt(opts)
	require.NoError(t, err)
	assert.Equal(t, "my-id", opts.Attributes["github.com/LFDT-Panurus/panurus/token/services/nfttx/UniqueID"])
}
