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

const maxFuzzTypedBytes = 256 << 10

func TestSerialization(t *testing.T) {
	raw := []byte("pineapple")
	wrappedToken, err := tokens.WrapWithType(0, raw)
	require.NoError(t, err)
	tok, err := tokens.UnmarshalTypedToken(wrappedToken)
	require.NoError(t, err)
	assert.Equal(t, driver.Type(0), tok.Type)
	assert.Equal(t, driver.Token(raw), tok.Token)
}

// TestUnmarshalTypedTokenRejectsTrailingBytes verifies that a valid encoding
// with arbitrary junk appended is rejected rather than silently accepted. See
// issue #2189: ignoring the ASN.1 `rest` slice makes the decoder malleable —
// two distinct byte strings would otherwise decode to the same object.
func TestUnmarshalTypedTokenRejectsTrailingBytes(t *testing.T) {
	valid, err := tokens.WrapWithType(7, driver.Token("payload"))
	require.NoError(t, err)

	// Sanity: the clean encoding still decodes fine.
	_, err = tokens.UnmarshalTypedToken(valid)
	require.NoError(t, err)

	withTrailer := append(append([]byte{}, valid...), 0x00, 0x01, 0x02)
	got, err := tokens.UnmarshalTypedToken(withTrailer)
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "trailing bytes")
}

// TestUnmarshalTypedMetadataRejectsTrailingBytes is the metadata counterpart of
// TestUnmarshalTypedTokenRejectsTrailingBytes.
func TestUnmarshalTypedMetadataRejectsTrailingBytes(t *testing.T) {
	valid, err := tokens.WrapMetadataWithType(7, driver.Metadata("payload"))
	require.NoError(t, err)

	_, err = tokens.UnmarshalTypedMetadata(valid)
	require.NoError(t, err)

	withTrailer := append(append([]byte{}, valid...), 0x00, 0x01, 0x02)
	got, err := tokens.UnmarshalTypedMetadata(withTrailer)
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "trailing bytes")
}

// FuzzUnmarshalTypedTokenNoPanic fuzzes UnmarshalTypedToken with arbitrary
// bytes. This decoder sits on the receive path for untrusted, ledger-stored
// and peer-supplied token bytes (see issue #2189), so any panic here is an
// unauthenticated DoS against every caller that reads typed tokens.
func FuzzUnmarshalTypedTokenNoPanic(f *testing.F) {
	valid, err := tokens.WrapWithType(7, driver.Token("payload"))
	require.NoError(f, err)

	f.Add([]byte(valid))
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add([]byte(valid)[:len(valid)/2])
	f.Add(append(append([]byte{}, valid...), 0x00, 0x01, 0x02))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTypedBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = tokens.UnmarshalTypedToken(raw)
		})
	})
}

// FuzzUnmarshalTypedMetadataNoPanic fuzzes UnmarshalTypedMetadata with
// arbitrary bytes, for the same reasons as FuzzUnmarshalTypedTokenNoPanic.
func FuzzUnmarshalTypedMetadataNoPanic(f *testing.F) {
	valid, err := tokens.WrapMetadataWithType(7, driver.Metadata("payload"))
	require.NoError(f, err)

	f.Add([]byte(valid))
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add([]byte(valid)[:len(valid)/2])
	f.Add(append(append([]byte{}, valid...), 0x00, 0x01, 0x02))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTypedBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = tokens.UnmarshalTypedMetadata(raw)
		})
	})
}
