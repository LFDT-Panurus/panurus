/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package encoding_test

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/stretchr/testify/assert"
)

func TestEncodingNone(t *testing.T) {
	msg := []byte("hello world")
	e := encoding.None.New()
	o1 := e.EncodeToString(msg)
	assert.Equal(t, o1, string(msg))
}

func TestEncodingBase64(t *testing.T) {
	msg := []byte("hello world")
	e := encoding.Base64.New()
	o1 := e.EncodeToString(msg)
	o2 := base64.StdEncoding.EncodeToString(msg)
	assert.Equal(t, o1, o2)
}

func TestEncodingHex(t *testing.T) {
	msg := []byte("hello world")
	e := encoding.Hex.New()
	o1 := e.EncodeToString(msg)
	o2 := hex.EncodeToString(msg)
	assert.Equal(t, o1, o2)
}

func TestEncodingString(t *testing.T) {
	assert.Equal(t, "None", encoding.None.String())
	assert.Equal(t, "Base64", encoding.Base64.String())
	assert.Equal(t, "Hex", encoding.Hex.String())
	assert.Contains(t, encoding.Encoding(99).String(), "unknown")
}

func TestEncodingAvailable(t *testing.T) {
	assert.True(t, encoding.None.Available())
	assert.True(t, encoding.Base64.Available())
	assert.True(t, encoding.Hex.Available())
	assert.False(t, encoding.Encoding(99).Available())
}

func TestRegisterEncoding_UnknownEncoding(t *testing.T) {
	err := encoding.RegisterEncoding(encoding.Encoding(99), func() encoding.EncodingFunc {
		return nil
	})
	assert.Error(t, err)
}

func TestEncodingFunc(t *testing.T) {
	assert.Equal(t, encoding.None, encoding.None.EncodingFunc())
	assert.Equal(t, encoding.Base64, encoding.Base64.EncodingFunc())
	assert.Equal(t, encoding.Hex, encoding.Hex.EncodingFunc())
}

func TestEncodingNew_Unavailable(t *testing.T) {
	e := encoding.Encoding(99)
	result := e.New()
	assert.Nil(t, result)
}
