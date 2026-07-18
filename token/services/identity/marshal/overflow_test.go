/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// lengthOverflowPayload is a DER SEQUENCE whose two length fields are each
// encoded in long form with 4 length-bytes set to 0xFF (i.e. length =
// 0xFFFFFFFF = 4294967295 decimal): an outer SEQUENCE, and an inner
// UTF8String.
//
// Historically, readLen's length accumulator was declared as plain `int`.
// On 64-bit platforms `int` is 64 bits and this value fits exactly, so
// DecodeIdentity's `np+l > len(b)` bounds check correctly rejected it as
// truncated. On a GOARCH=386/arm/mips-class platform, `int` is 32 bits, and
// the identical accumulation wrapped 0xFFFFFFFF around to -1, defeating
// that same bounds check and causing DecodeIdentity to slice with a
// negative length (independently confirmed under linux/386 emulation to
// panic with "slice bounds out of range [12:11]").
//
// readLen now accumulates in uint64 and bounds-checks the declared length
// against the remaining buffer in unsigned arithmetic before ever
// converting it to int, so this payload is rejected identically on every
// platform regardless of native int width.
var lengthOverflowPayload = []byte{
	0x30, 0x84, 0xFF, 0xFF, 0xFF, 0xFF, // SEQUENCE, long-form len = 0xFFFFFFFF
	0x0C, 0x84, 0xFF, 0xFF, 0xFF, 0xFF, // UTF8String, long-form len = 0xFFFFFFFF
	'A', 'B', 'C', 'D',
}

// TestReadLen_RejectsLengthExceedingRemainingBufferOnAnyPlatform proves that
// readLen rejects a declared length that exceeds the remaining buffer using
// unsigned arithmetic, independent of the platform's native int width. This
// is the fix for the platform-conditional overflow: previously, the same
// bounds check was done in plain `int` arithmetic after accumulating the
// length in a plain `int`, which wrapped around to a negative value on a
// 32-bit-int platform.
func TestReadLen_RejectsLengthExceedingRemainingBufferOnAnyPlatform(t *testing.T) {
	_, _, err := readLen(lengthOverflowPayload, 7)
	require.ErrorIs(t, err, ErrTruncated, "a declared length far exceeding the remaining buffer must be rejected, "+
		"not silently accepted and left for a later (possibly platform-dependent) bounds check")

	// Sanity: strconv.IntSize confirms which platform actually ran this
	// assertion, but the outcome above must hold on both 32- and 64-bit
	// platforms since readLen no longer depends on native int width.
	t.Logf("ran on a %d-bit int platform", strconv.IntSize)
}

// TestDecodeIdentity_LengthOverflowNoLongerPanics is the end-to-end
// regression test: a 4-byte length field with the high bit set must be
// rejected with a clean error by DecodeIdentity rather than panicking,
// regardless of platform. Before the fix, this only panicked on a 32-bit
// int platform (confirmed via linux/386 emulation) because readLen's own
// bounds check was platform-dependent; the fix removes that dependency
// entirely, so this assertion now holds unconditionally.
func TestDecodeIdentity_LengthOverflowNoLongerPanics(t *testing.T) {
	require.NotPanics(t, func() {
		_, err := DecodeIdentity(lengthOverflowPayload)
		require.Error(t, err, "an oversized length field must be rejected with an error")
	})
}
