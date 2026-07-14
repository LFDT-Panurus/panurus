/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputStream_Validate(t *testing.T) {
	os := &OutputStream{
		OutputStream: token.NewOutputStream(nil, 64),
	}

	err := os.Validate()
	assert.NoError(t, err, "empty outputs should validate")
}

func TestOutputStream_StateAt(t *testing.T) {
	h := &House{
		LinearID: "123",
	}
	_, err := json.Marshal(h)
	require.NoError(t, err)

	os := &OutputStream{
		OutputStream: token.NewOutputStream(nil, 64),
	}
	// Append not directly exported, so we simulate an error when fetching bounds or we skip directly. StateAt uses o.At(index).
	assert.Panics(t, func() {
		_ = os.StateAt(0, &House{})
	})
}

func TestOutputStreamWrappers(t *testing.T) {
	os := &OutputStream{
		OutputStream: token.NewOutputStream(nil, 64),
	}
	assert.NotNil(t, os.Filter(func(t *token.Output) bool { return true }))
	assert.NotNil(t, os.ByRecipient(nil))
	assert.NotNil(t, os.ByType("my-type"))
	assert.NotNil(t, os.ByEnrollmentID("my-id"))
}
