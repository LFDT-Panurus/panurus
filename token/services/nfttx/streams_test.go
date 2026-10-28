/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
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

func newOutputStream(typ token2.Type) *OutputStream {
	return &OutputStream{
		OutputStream: token.NewOutputStream([]*token.Output{
			{Type: typ, Quantity: token2.NewOneQuantity(64)},
		}, 64),
	}
}

func encodeState(t *testing.T, v any) token2.Type {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return token2.Type(base64.StdEncoding.EncodeToString(raw))
}

func TestOutputStream_StateAt(t *testing.T) {
	h := &House{
		LinearID:  "123",
		Address:   "5th Avenue",
		Valuation: 100,
	}

	var house House
	require.NoError(t, newOutputStream(encodeState(t, h)).StateAt(0, &house))
	assert.Equal(t, *h, house)
}

func TestOutputStream_StateAt_UnmarshalError(t *testing.T) {
	malformed := token2.Type(base64.StdEncoding.EncodeToString([]byte("{not json")))

	var house House
	err := newOutputStream(malformed).StateAt(0, &house)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal state")
	assert.Equal(t, House{}, house)
}

func TestOutputStream_StateAt_TypeMismatch(t *testing.T) {
	err := newOutputStream(encodeState(t, map[string]any{"Valuation": "not-a-number"})).StateAt(0, &House{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal state")
}

func TestOutputStream_StateAt_DecodeError(t *testing.T) {
	err := newOutputStream("not-base64").StateAt(0, &House{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode type")
}

func TestOutputStream_StateAt_NoState(t *testing.T) {
	var house House
	err := newOutputStream(token2.Type(base64.StdEncoding.EncodeToString([]byte("null")))).StateAt(0, &house)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type carries no state")
	assert.Equal(t, House{}, house)
}

func TestOutputStream_StateAt_OutOfRange(t *testing.T) {
	empty := &OutputStream{
		OutputStream: token.NewOutputStream(nil, 64),
	}
	require.ErrorContains(t, empty.StateAt(0, &House{}), "out of range")
	require.ErrorContains(t, empty.StateAt(-1, &House{}), "out of range")

	one := newOutputStream(encodeState(t, &House{LinearID: "123"}))
	require.ErrorContains(t, one.StateAt(1, &House{}), "out of range")
	require.ErrorContains(t, one.StateAt(-1, &House{}), "out of range")
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
