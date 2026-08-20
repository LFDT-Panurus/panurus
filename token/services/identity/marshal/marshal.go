/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal

import (
	"bytes"
	"encoding/asn1"
	"errors"
	"math"
	"unsafe"

	"github.com/LFDT-Panurus/panurus/token/driver"
)

// DER tag bytes — derived from encoding/asn1 constants.
// Primitive universal types: tag byte == tag number.
// SEQUENCE needs the constructed bit (0x20): 16 | 0x20 = 0x30.
const (
	tagInteger         = byte(asn1.TagInteger)         // 0x02
	tagOctetString     = byte(asn1.TagOctetString)     // 0x04
	tagUTF8String      = byte(asn1.TagUTF8String)      // 0x0C
	tagPrintableString = byte(asn1.TagPrintableString) // 0x13
	tagSequence        = byte(asn1.TagSequence) | 0x20 // 0x30
)

// Sentinel errors — no fmt.Errorf allocation on hot path
var (
	ErrTruncated     = errors.New("asn1: truncated data")
	ErrUnexpectedTag = errors.New("asn1: unexpected tag")
	ErrIntOverflow   = errors.New("asn1: integer overflows int32")
	ErrInvalidLen    = errors.New("asn1: invalid length encoding")
	ErrTrailingBytes = errors.New("asn1: trailing bytes after value")
	ErrNonMinimalLen = errors.New("asn1: non-minimal length encoding")
	ErrNonMinimalInt = errors.New("asn1: non-minimal integer encoding")
	// ErrNonCanonical means the input is not the encoding asn1.Marshal produces
	// for the destination type. For the four identity envelopes that is the same
	// thing as "not canonical DER" — a property their field types guarantee and
	// TestUnmarshalCanonicalDER_FourCallSitesHaveNoOptionalFields pins. For a type
	// with an optional/omitempty/time.Time field the two would diverge, which is
	// why UnmarshalCanonicalDER documents those as unsupported destinations.
	ErrNonCanonical = errors.New("asn1: not the canonical encoding for this type")

	// ErrInvalidDestination reports a caller programming error — a nil decode
	// destination — as opposed to the Err* values above, which all report
	// something wrong with the bytes being decoded. The *T parameter makes the
	// other misuses (non-pointer, untyped nil) compile errors instead.
	ErrInvalidDestination = errors.New("asn1: destination pointer is nil")
)

// UnmarshalCanonicalDER decodes DER-encoded data into val with encoding/asn1 and
// accepts b only if b is the one canonical encoding of the value it decoded to.
//
// The destination is *T rather than any so that the compiler rejects the two
// misuses that used to be runtime errors — a non-pointer, and an untyped nil.
// A typed nil pointer is the only one left, and returns ErrInvalidDestination
// without inspecting b. Every other error value this returns describes the
// bytes, not the destination.
//
// This matters because Identity.UniqueID() hashes the *raw* identity bytes
// rather than a canonicalised form of the decoded value, and it is the cache
// key throughout the identity and wallet layers (role/registry.go's fast path,
// provider.go's signer cache, …). Any two byte strings that decode to the same
// logical identity but hash differently give that one identity two cache slots:
// a token paid to the non-canonical spelling still verifies (the verifier works
// on the decoded value) but never resolves to its owner's wallet (the lookup
// works on UniqueID()). Use this instead of calling asn1.Unmarshal directly
// whenever the bytes being decoded came off the wire.
//
// encoding/asn1.Unmarshal is lenient in two ways that both have to be closed:
//
//   - it reports bytes left over after the top-level value through its first
//     return value and is happy to ignore them;
//   - it silently discards SEQUENCE elements the destination struct has no
//     field for. Those never appear in rest — the top-level TLV did consume the
//     whole input — so checking rest alone leaves the SEQUENCE body itself as a
//     free-form channel for garbage. It is similarly happy to accept
//     T61String/IA5String/GeneralString where the struct asks for a UTF8String.
//
// The rest check catches the first. Re-encoding the decoded value and requiring
// it to reproduce b byte-for-byte catches both, and generalises to any future
// encoding/asn1 leniency, at the cost of one extra marshal of a small struct per
// decode. The hot TypedIdentity envelope path does not come through here: it
// uses DecodeIdentity, which walks the TLVs itself and pins every length.
//
// The property this establishes is precisely "b is something asn1.Marshal would
// have emitted for this type". That is the right definition here because every
// producer in this tree goes through asn1.Marshal, but it is narrower than "b is
// valid DER", so it constrains what val may be:
//
//   - Do not use it on a type with an `asn1:"optional"` or `asn1:"omitempty"`
//     field. asn1.Marshal omits such a field when it holds the zero value, while
//     asn1.Unmarshal accepts it explicitly present — a legal encoding that the
//     round-trip then rejects. (A field with `default:` is different: DER does
//     require the default to be absent, so rejecting an explicit one is correct.)
//   - Do not use it on a type with a time.Time field. UTCTime and
//     GeneralizedTime both decode, but asn1.Marshal picks one by year, so the
//     other is rejected.
//
// None of the four identity envelopes hit either case: they carry only int32,
// string and [][]byte fields, with no optional, omitempty or default tags. Check
// before adding a fifth caller.
func UnmarshalCanonicalDER[T any](b []byte, val *T) error {
	// asn1.Unmarshal would reject this too, but only as an untyped structure
	// error that a caller cannot tell apart from "these bytes are malformed" —
	// the one distinction this package's error values exist to make.
	if val == nil {
		return ErrInvalidDestination
	}

	rest, err := asn1.Unmarshal(b, val)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return ErrTrailingBytes
	}

	// asn1.Marshal takes the value, not a pointer to it. Typing val as *T makes
	// this a plain dereference: no reflection, and no pointer-to-pointer case to
	// reason about, because *T is exactly one level.
	reencoded, err := asn1.Marshal(*val)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, reencoded) {
		return ErrNonCanonical
	}

	return nil
}

// Result holds the decoded payload. IsInt distinguishes the two variants.
// Data is a zero-copy sub-slice of the input — do not modify input while using it.
type Result struct {
	IsInt bool
	Int32 int32  // valid when IsInt == true
	Str   string // valid when IsInt == false
	Data  []byte // zero-copy reference into input
}

// DecodeIdentity parses a DER SEQUENCE containing either
// [INTEGER, OCTET STRING] or [UTF8String, OCTET STRING].
func DecodeIdentity(b []byte) (Result, error) {
	var r Result

	// Outer SEQUENCE
	if len(b) == 0 || b[0] != tagSequence {
		return r, ErrUnexpectedTag
	}
	seqLen, pos, err := readLen(b, 1)
	if err != nil {
		return r, err
	}
	// The outer SEQUENCE must declare exactly the bytes it was given: no
	// fewer (trailing garbage after the value) and no more (a truncated
	// buffer). Without this, b and b+garbage decode identically while
	// hashing to different Identity.UniqueID()s — see UnmarshalCanonicalDER.
	if pos+seqLen > len(b) {
		return r, ErrTruncated
	}
	if pos+seqLen < len(b) {
		return r, ErrTrailingBytes
	}

	// Dispatch on first element's tag
	if pos >= len(b) {
		return r, ErrTruncated
	}
	switch b[pos] {
	case tagInteger:
		pos++
		l, np, err := readLen(b, pos)
		if err != nil {
			return r, err
		}
		if np+l > len(b) {
			return r, ErrTruncated
		}
		v, err := parseInt32(b[np : np+l])
		if err != nil {
			return r, err
		}
		r.IsInt = true
		r.Int32 = v
		pos = np + l

		// In Decode(), merge both string types into a single case:
	case tagUTF8String, tagPrintableString:
		pos++
		l, np, err := readLen(b, pos)
		if err != nil {
			return r, err
		}
		if np+l > len(b) {
			return r, ErrTruncated
		}
		r.Str = unsafe.String(unsafe.SliceData(b[np:np+l]), l)
		pos = np + l

	default:
		return r, ErrUnexpectedTag
	}

	// OCTET STRING
	if pos >= len(b) {
		return r, ErrTruncated
	}
	if b[pos] != tagOctetString {
		return r, ErrUnexpectedTag
	}
	pos++
	l, np, err := readLen(b, pos)
	if err != nil {
		return r, err
	}
	if np+l > len(b) {
		return r, ErrTruncated
	}
	// The OCTET STRING is the last declared field, and the outer SEQUENCE's
	// length was already pinned to len(b) above, so anything left over here
	// is an undeclared extra field smuggled inside the SEQUENCE.
	if np+l != len(b) {
		return r, ErrTrailingBytes
	}
	r.Data = b[np : np+l] // zero-copy

	if !r.IsInt {
		// convert string to int for legacy reasons
		switch r.Str {
		case driver.X509IdentityTypeString:
			r.Int32 = driver.X509IdentityType
			r.IsInt = true
		case driver.IdemixIdentityTypeString:
			r.Int32 = driver.IdemixIdentityType
			r.IsInt = true
		case driver.IdemixNymIdentityTypeString:
			r.Int32 = driver.IdemixNymIdentityType
			r.IsInt = true
		case driver.MultiSigIdentityTypeString:
			r.Int32 = driver.MultiSigIdentityType
			r.IsInt = true
		case driver.HTLCScriptIdentityTypeString:
			r.Int32 = driver.HTLCScriptIdentityType
			r.IsInt = true
		}
	}

	return r, nil
}

// readLen decodes a DER length at b[pos]. Returns (length, nextPos, err).
func readLen(b []byte, pos int) (int, int, error) {
	if pos >= len(b) {
		return 0, 0, ErrTruncated
	}
	fb := b[pos]
	if fb < 0x80 { // short form
		return int(fb), pos + 1, nil
	}
	n := int(fb & 0x7F)
	if n == 0 || n > 4 || pos+1+n > len(b) { // cap at 4 bytes = 4 GiB
		return 0, 0, ErrInvalidLen
	}
	pos++
	// DER admits exactly one length encoding per length, and the long form
	// must not carry leading zero bytes. Rejecting the alternatives keeps one
	// logical identity to one byte string: a length written as 0x82 0x00 0x06
	// instead of 0x06 decodes to the same identity but hashes to a different
	// Identity.UniqueID(), which is the cache key across the identity and
	// wallet layers. See UnmarshalCanonicalDER for the full reasoning.
	if b[pos] == 0x00 {
		return 0, 0, ErrNonMinimalLen
	}
	// Accumulate in uint64: at most 4 bytes, so l can never exceed
	// 0xFFFFFFFF and this addition/shift can never overflow, regardless of
	// the platform's native int width.
	var l uint64
	for i := range n {
		l = l<<8 | uint64(b[pos+i])
	}
	pos += n
	// ...and a length the short form could have carried must use it. Checked
	// after accumulating so it also catches a multi-byte form whose bytes
	// happen to encode a small value (e.g. 0x84 0x00 0x00 0x00 0x06), which
	// the leading-zero check above already rejects but which would otherwise
	// slip through for hypothetical wider forms.
	if l < 0x80 {
		return 0, 0, ErrNonMinimalLen
	}
	// Reject a declared length that claims more bytes than remain in the
	// buffer here, in unsigned arithmetic, before ever converting it to
	// int. Doing the equivalent check with a plain-int accumulator (as
	// before) is platform-dependent: on a 32-bit-int platform a 4-byte
	// length with the high bit set wraps around to a negative int, which
	// defeats every caller's own "np+l > len(b)" bounds check.
	//nolint:gosec // pos <= len(b) here (enforced by the pos+1+n > len(b) check above), so len(b)-pos is never negative
	if l > uint64(len(b)-pos) {
		return 0, 0, ErrTruncated
	}

	return int(l), pos, nil
}

// parseInt32 decodes a DER big-endian signed integer into int32.
//
// Like readLen, it insists on the minimal encoding: DER admits exactly one
// spelling per integer value, and accepting the alternatives would reintroduce
// the malleability the length check closes one field over. 02 02 00 05 carries
// the same 5 as 02 01 05 but hashes to a different Identity.UniqueID() — see
// UnmarshalCanonicalDER for why that is the bug and not a cosmetic difference.
func parseInt32(b []byte) (int32, error) {
	if len(b) == 0 || len(b) > 5 {
		return 0, ErrIntOverflow
	}
	// A leading 0x00 is redundant unless it is there to keep a positive value
	// from being read as negative; a leading 0xFF is redundant unless it is
	// there to keep a negative value negative.
	if len(b) > 1 {
		if b[0] == 0x00 && b[1]&0x80 == 0 {
			return 0, ErrNonMinimalInt
		}
		if b[0] == 0xFF && b[1]&0x80 != 0 {
			return 0, ErrNonMinimalInt
		}
	}
	var v int64
	if b[0]&0x80 != 0 {
		v = -1 // sign-extend
	}
	for _, c := range b {
		v = v<<8 | int64(c)
	}
	if v > math.MaxInt32 || v < math.MinInt32 {
		return 0, ErrIntOverflow
	}

	return int32(v), nil
}

// Encode serializes a Result back to DER for testing/interop.
func Encode(r Result) []byte {
	var first []byte
	if r.IsInt {
		first = appendTLV(nil, tagInteger, encodeInt32(r.Int32))
	} else {
		first = appendTLV(nil, tagUTF8String, []byte(r.Str))
	}
	body := append(first, appendTLV(nil, tagOctetString, r.Data)...)

	return appendTLV(nil, tagSequence, body)
}

// EncodeIdentity serializes the pair (int32, []byte)
func EncodeIdentity(t int32, data []byte) []byte {
	return appendTLV(nil, tagSequence, append(
		appendTLV(nil, tagInteger, encodeInt32(t)),
		appendTLV(nil, tagOctetString, data)...,
	))
}

func appendTLV(dst []byte, tag byte, val []byte) []byte {
	dst = append(dst, tag)
	l := len(val)
	switch {
	case l < 0x80:
		dst = append(dst, byte(l))
	case l < 0x100:
		dst = append(dst, 0x81, byte(l))
	case l < 0x10000:
		//nolint:gosec // l < 0x10000 here, so each byte() truncation below is a deliberate low-byte mask, not overflow
		dst = append(dst, 0x82, byte(l>>8), byte(l))
	case l < 0x1000000:
		//nolint:gosec // l < 0x1000000 here, so each byte() truncation below is a deliberate low-byte mask, not overflow
		dst = append(dst, 0x83, byte(l>>16), byte(l>>8), byte(l))
	default:
		// readLen caps the long-form length at 4 length-bytes (4 GiB), so
		// this is the widest form it can decode back.
		//nolint:gosec // appendTLV is only ever called internally with l == len(val), never with an attacker-controlled 4GiB+ value; each byte() truncation below is a deliberate low-byte mask
		dst = append(dst, 0x84, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
	}

	return append(dst, val...)
}

func encodeInt32(v int32) []byte {
	var b [4]byte
	//nolint:gosec // v is int32, shifts are safe
	b[0] = byte(v >> 24)
	//nolint:gosec // v is int32, shifts are safe
	b[1] = byte(v >> 16)
	//nolint:gosec // v is int32, shifts are safe
	b[2] = byte(v >> 8)
	//nolint:gosec // v is int32, shifts are safe
	b[3] = byte(v)
	i := 0
	for i < 3 && b[i] == 0x00 && b[i+1]&0x80 == 0 {
		i++
	}
	for i < 3 && b[i] == 0xFF && b[i+1]&0x80 != 0 {
		i++
	}

	return b[i:]
}
