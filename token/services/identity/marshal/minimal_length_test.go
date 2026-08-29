/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal_test

import (
	"encoding/asn1"
	"math"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests below cover the second non-canonical vector in identity decoding:
// non-minimal DER length encodings.
//
// readLen used to accept any long form of 1–4 length bytes, so the same length
// could be written several ways — 0x06, 0x81 0x06, 0x82 0x00 0x06 — all decoding
// to an identical identity. As with trailing bytes, that matters because
// Identity.UniqueID() hashes the raw bytes rather than a canonicalised form of
// the decoded value, so each spelling is a separate cache key across the
// identity and wallet layers for one logical identity.
//
// This is safe to tighten because both producers of these bytes emit minimal
// lengths: appendTLV in this package, and encoding/asn1.Marshal (legacy
// TypedIdentity encodings). TestDecodeIdentity_AcceptsEveryLengthFormOurEncoderEmits
// and TestDecodeIdentity_StdlibEncodingsRemainDecodable are the guards against
// over-tightening.

// TestDecodeIdentity_RejectsNonMinimalLengthEncoding walks the three places a
// length appears in an identity encoding — the outer SEQUENCE, the type field,
// and the OCTET STRING — and confirms each rejects a non-minimal spelling.
func TestDecodeIdentity_RejectsNonMinimalLengthEncoding(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			// Outer SEQUENCE length 6 written as 0x81 0x06 instead of 0x06.
			"outer sequence length in long form",
			[]byte{
				seqByte, 0x81, 0x06,
				byte(asn1.TagInteger), 0x01, 0x01,
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
		{
			// INTEGER length 1 written as 0x82 0x00 0x01.
			"integer length with leading zero byte",
			[]byte{
				seqByte, 0x08,
				byte(asn1.TagInteger), 0x82, 0x00, 0x01, 0x01,
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
		{
			// OCTET STRING length 1 written as 0x81 0x01.
			"octet string length in long form",
			[]byte{
				seqByte, 0x07,
				byte(asn1.TagInteger), 0x01, 0x01,
				byte(asn1.TagOctetString), 0x81, 0x01, 0xAA,
			},
		},
		{
			// UTF8String length 5 written as 0x81 0x05.
			"utf8 string length in long form",
			[]byte{
				seqByte, 0x0B,
				byte(asn1.TagUTF8String), 0x81, 0x05, 'x', '5', '0', '9', '!',
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := marshal.DecodeIdentity(tt.raw)
			require.ErrorIs(t, err, marshal.ErrNonMinimalLen,
				"a non-minimal DER length must be rejected: it is a second spelling of an identity we already accept in canonical form")
		})
	}
}

// TestDecodeIdentity_NonMinimalLengthWasADistinctCacheKey states the reason the
// laxness mattered, rather than just that it existed: the non-minimal spelling
// decoded to exactly the same identity yet hashed differently, so it occupied a
// second slot in every UniqueID()-keyed cache.
func TestDecodeIdentity_NonMinimalLengthWasADistinctCacheKey(t *testing.T) {
	canonical := marshal.EncodeIdentity(1, []byte{0xAA})
	require.Equal(t, byte(0x06), canonical[1], "fixture assumes a short-form outer length")

	// Rewrite the outer length 0x06 as the long form 0x81 0x06. Nothing else
	// about the value changes.
	nonMinimal := append([]byte{canonical[0], 0x81}, canonical[1:]...)

	res, err := marshal.DecodeIdentity(canonical)
	require.NoError(t, err)
	assert.Equal(t, int32(1), res.Int32)
	assert.Equal(t, []byte{0xAA}, res.Data)

	_, err = marshal.DecodeIdentity(nonMinimal)
	require.ErrorIs(t, err, marshal.ErrNonMinimalLen,
		"the long-form spelling of a short-form length must be rejected")

	assert.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(nonMinimal).UniqueID(),
		"UniqueID() hashes the raw bytes, so the non-minimal spelling was a distinct cache key")
}

// TestDecodeIdentity_AcceptsEveryLengthFormOurEncoderEmits guards the
// minimal-length check against over-tightening: appendTLV grows from the short
// form to the 0x81/0x82/0x83 long forms as the payload grows, and every one of
// those must keep decoding. A payload spanning each boundary is round-tripped.
func TestDecodeIdentity_AcceptsEveryLengthFormOurEncoderEmits(t *testing.T) {
	for _, size := range []int{0, 1, 0x7F, 0x80, 0xFF, 0x100, 0xFFFF, 0x10000} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i)
		}

		res, err := marshal.DecodeIdentity(marshal.EncodeIdentity(3, data))
		require.NoError(t, err, "our own encoder must always emit a decodable minimal length (payload %d bytes)", size)
		require.Len(t, res.Data, size)
		assert.Equal(t, int32(3), res.Int32)
	}

	// Same for the string variant, whose first field length also crosses the
	// short/long-form boundary.
	for _, size := range []int{0x7F, 0x80} {
		res, err := marshal.DecodeIdentity(marshal.Encode(marshal.Result{
			Str:  string(make([]byte, size)),
			Data: []byte{0x01},
		}))
		require.NoError(t, err, "string field of %d bytes must decode", size)
		assert.Len(t, res.Str, size)
	}
}

// ---------------------------------------------------------------------------
// Non-minimal INTEGER contents
// ---------------------------------------------------------------------------

// A minimal length is only half of a minimal INTEGER: the *contents* admit
// redundant spellings too. 02 02 00 05 declares a perfectly minimal length of
// two for a value that fits in one byte, so the length check above waves it
// through, and parseInt32 then sign-extended and accumulated the redundant
// leading byte away — yielding the same identity type under different raw
// bytes, one field over from the check that was just tightened.

// TestDecodeIdentity_RejectsNonMinimalIntegerEncoding covers each redundant
// leading-byte shape DER forbids, in the identity type field.
func TestDecodeIdentity_RejectsNonMinimalIntegerEncoding(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			// A leading 0x00 is only permitted when it stops a positive value
			// from being read as negative, which 0x05 does not need: the
			// minimal spelling of 5 is 02 01 05.
			"redundant leading zero",
			[]byte{
				seqByte, 0x07,
				byte(asn1.TagInteger), 0x02, 0x00, 0x05,
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
		{
			"two redundant leading zeros",
			[]byte{
				seqByte, 0x08,
				byte(asn1.TagInteger), 0x03, 0x00, 0x00, 0x05,
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
		{
			// A leading 0xFF is only permitted when it keeps a negative value
			// negative, which 0x85 (already negative) does not need.
			"redundant leading ff",
			[]byte{
				seqByte, 0x07,
				byte(asn1.TagInteger), 0x02, 0xFF, 0x85,
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
		{
			// Padded all the way out to parseInt32's 5-byte allowance, which
			// widened the set of spellings further.
			"zero-padded to five bytes",
			[]byte{
				seqByte, 0x0A,
				byte(asn1.TagInteger), 0x05, 0x00, 0x00, 0x00, 0x00, 0x05,
				byte(asn1.TagOctetString), 0x01, 0xAA,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := marshal.DecodeIdentity(tt.raw)
			require.ErrorIs(t, err, marshal.ErrNonMinimalInt,
				"a non-minimal DER integer must be rejected: it is a second spelling of an identity we already accept in canonical form")
		})
	}
}

// TestDecodeIdentity_NonMinimalIntegerWasADistinctCacheKey states why the
// laxness mattered, mirroring the length-encoding case above.
func TestDecodeIdentity_NonMinimalIntegerWasADistinctCacheKey(t *testing.T) {
	canonical := marshal.EncodeIdentity(5, []byte("payload"))

	res, err := marshal.DecodeIdentity(canonical)
	require.NoError(t, err)
	require.Equal(t, int32(5), res.Int32)

	// Same identity, type written as 02 02 00 05 instead of 02 01 05.
	padded := []byte{seqByte, 0x0D, byte(asn1.TagInteger), 0x02, 0x00, 0x05, byte(asn1.TagOctetString), 0x07}
	padded = append(padded, []byte("payload")...)

	_, err = marshal.DecodeIdentity(padded)
	require.ErrorIs(t, err, marshal.ErrNonMinimalInt,
		"the zero-padded spelling of the type field must be rejected")

	assert.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(padded).UniqueID(),
		"UniqueID() hashes the raw bytes, so the padded spelling was a distinct cache key")
}

// TestDecodeIdentity_AcceptsEveryIntegerOurEncoderEmits guards the integer
// check against over-tightening. encodeInt32 strips redundant leading bytes
// with exactly the rule parseInt32 now enforces, so the tree cannot reject its
// own output — including the one case where a leading 0x00 is *required*
// (a positive value whose top content byte has the high bit set) and the
// negative values that legitimately begin 0xFF.
func TestDecodeIdentity_AcceptsEveryIntegerOurEncoderEmits(t *testing.T) {
	for _, v := range []int32{
		0, 1, 5, 127,
		128,   // 0x00 0x80 — the required leading zero
		255,   // 0x00 0xFF
		32767, // 0x7F 0xFF, no leading zero needed
		32768, // 0x00 0x80 0x00
		math.MaxInt32,
		-1, // 0xFF
		-128,
		-129, // 0xFF 0x7F — leading 0xFF required
		math.MinInt32,
	} {
		res, err := marshal.DecodeIdentity(marshal.EncodeIdentity(v, []byte{0xAA}))
		require.NoError(t, err, "our own encoder must always emit a decodable minimal integer (value %d)", v)
		assert.Equal(t, v, res.Int32)
	}

	// encoding/asn1.Marshal is the other producer of these bytes (legacy
	// TypedIdentity encodings) and is likewise canonical.
	for _, v := range []int32{0, 127, 128, 255, math.MaxInt32, -1, -129, math.MinInt32} {
		res, err := marshal.DecodeIdentity(marshalInt(t, v, []byte{0xAA}))
		require.NoError(t, err, "encoding/asn1's integer encoding must remain decodable (value %d)", v)
		assert.Equal(t, v, res.Int32)
	}
}
