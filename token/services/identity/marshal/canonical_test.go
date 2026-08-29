/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal_test

import (
	"encoding/asn1"
	"reflect"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests below cover the canonical-form requirement for identity decoding.
//
// DecodeIdentity used to read the outer SEQUENCE's declared length and throw it
// away ("skip SEQUENCE length; we trust inner bounds checks"), never checking
// that the parsed position reached the end of the buffer. Two different byte
// strings therefore decoded to the same logical TypedIdentity — while hashing
// to *different* Identity.UniqueID() values, because UniqueID() hashes the raw
// bytes rather than a canonicalised form of the decoded value. Since UniqueID()
// is the cache key throughout the identity/wallet layers (role/registry.go's
// fast path, provider.go's signer cache, …), a lenient parse means one logical
// identity can occupy two cache slots.

// TestDecodeIdentity_RejectsTrailingBytes is the issue's reproduction at the
// DecodeIdentity level: marshal a valid identity, append garbage, and confirm
// the decode now fails — and that the two byte strings really did hash to
// different UniqueID()s, which is what made the laxness matter.
func TestDecodeIdentity_RejectsTrailingBytes(t *testing.T) {
	canonical := marshal.EncodeIdentity(1, []byte("payload"))

	res, err := marshal.DecodeIdentity(canonical)
	require.NoError(t, err, "the canonical encoding must still decode")
	require.Equal(t, int32(1), res.Int32)

	for _, garbage := range [][]byte{
		{0x00},
		{0xFF, 0xFF},
		// A second, well-formed TLV appended after the SEQUENCE — the most
		// plausible shape of an accidental double-append.
		{byte(asn1.TagOctetString), 0x01, 0x00},
	} {
		padded := append(append([]byte{}, canonical...), garbage...)

		_, err := marshal.DecodeIdentity(padded)
		require.ErrorIs(t, err, marshal.ErrTrailingBytes,
			"bytes after the outer SEQUENCE must be rejected, not silently ignored")

		// The reason the lenient parse was a problem: the two byte strings
		// are distinct cache keys even though they decoded identically.
		assert.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(padded).UniqueID(),
			"UniqueID() hashes the raw bytes, so the padded form was a distinct cache key")
	}
}

// TestDecodeIdentity_RejectsUndeclaredExtraFieldInsideSequence covers the other
// direction: garbage smuggled *inside* the SEQUENCE by growing its declared
// length. The outer-length check alone would accept this, because the declared
// length does match the buffer end — the OCTET STRING end-of-buffer check is
// what rejects it.
func TestDecodeIdentity_RejectsUndeclaredExtraFieldInsideSequence(t *testing.T) {
	raw := []byte{
		seqByte, 0x09,
		byte(asn1.TagInteger), 0x01, 0x01,
		byte(asn1.TagOctetString), 0x01, 0xAA,
		// Undeclared third field, inside the SEQUENCE's declared length.
		byte(asn1.TagOctetString), 0x01, 0xBB,
	}
	require.Len(t, raw, 2+0x09, "fixture must be self-consistent: outer length covers the extra field")

	_, err := marshal.DecodeIdentity(raw)
	require.ErrorIs(t, err, marshal.ErrTrailingBytes,
		"an undeclared field after the OCTET STRING must be rejected")
}

// TestDecodeIdentity_RejectsOuterLengthOverrunningBuffer pins the *other* side
// of the outer-length check to ErrTruncated rather than ErrTrailingBytes: a
// declared length longer than the buffer is a short read, not padding.
func TestDecodeIdentity_RejectsOuterLengthOverrunningBuffer(t *testing.T) {
	raw := []byte{
		seqByte, 0x0A, // claims 10 bytes of content
		byte(asn1.TagInteger), 0x01, 0x01,
		byte(asn1.TagOctetString), 0x01, 0xAA, // only 6 present
	}

	_, err := marshal.DecodeIdentity(raw)
	require.ErrorIs(t, err, marshal.ErrTruncated,
		"an outer length exceeding the buffer is truncation, not trailing bytes")
}

// TestDecodeIdentity_StdlibEncodingsRemainDecodable is the interop guard for the
// tightened decoder: encoding/asn1.Marshal is the other producer of these bytes
// (legacy TypedIdentity encodings and the ref* structs below), and it emits
// canonical minimal-length DER, so every one of its outputs must still decode.
func TestDecodeIdentity_StdlibEncodingsRemainDecodable(t *testing.T) {
	// Sizes chosen to straddle the short/long-form length boundary in both
	// the first field and the OCTET STRING.
	for _, size := range []int{1, 0x7F, 0x80, 0x100} {
		data := make([]byte, size)

		res, err := marshal.DecodeIdentity(marshalInt(t, 42, data))
		require.NoError(t, err, "encoding/asn1's int encoding (%d-byte payload) must remain decodable", size)
		assert.Equal(t, int32(42), res.Int32)

		res, err = marshal.DecodeIdentity(marshalUTF8(t, string(make([]byte, size)), data))
		require.NoError(t, err, "encoding/asn1's utf8 encoding (%d-byte payload) must remain decodable", size)
		assert.Len(t, res.Str, size)
	}
}

// ---------------------------------------------------------------------------
// UnmarshalCanonicalDER
// ---------------------------------------------------------------------------

// TestUnmarshalCanonicalDER_RejectsTrailingBytes covers the shared helper that the
// four previously-lax identity call sites now route through. Each of them
// discarded encoding/asn1.Unmarshal's "rest" return.
func TestUnmarshalCanonicalDER_RejectsTrailingBytes(t *testing.T) {
	canonical, err := asn1.Marshal(refInt{Value: 7, Data: []byte("body")})
	require.NoError(t, err)

	var dst refInt
	require.NoError(t, marshal.UnmarshalCanonicalDER(canonical, &dst),
		"the canonical encoding must still decode")
	assert.Equal(t, int32(7), dst.Value)

	padded := append(append([]byte{}, canonical...), 0xDE, 0xAD)

	// Baseline: the stdlib call the four sites used happily ignores the tail.
	rest, err := asn1.Unmarshal(padded, &refInt{})
	require.NoError(t, err)
	require.NotEmpty(t, rest, "encoding/asn1 reports the tail rather than rejecting it — that return was discarded")

	require.ErrorIs(t, marshal.UnmarshalCanonicalDER(padded, &refInt{}), marshal.ErrTrailingBytes,
		"UnmarshalCanonicalDER must reject what asn1.Unmarshal merely reports")
}

// TestUnmarshalCanonicalDER_PropagatesDecodeErrors makes sure the canonicality wrapper
// does not swallow or reclassify a genuine decode failure.
func TestUnmarshalCanonicalDER_PropagatesDecodeErrors(t *testing.T) {
	err := marshal.UnmarshalCanonicalDER([]byte("not asn1"), &refInt{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, marshal.ErrTrailingBytes,
		"a malformed value must surface its own error, not be reported as trailing bytes")
}

// refIntWithExtra is refInt plus one undeclared trailing element. Marshalling
// it produces bytes that decode into refInt cleanly — encoding/asn1 consumes
// only as many SEQUENCE elements as the destination struct has fields and
// silently drops the remainder — while carrying a payload refInt never
// described. This is what an attacker writes.
type refIntWithExtra struct {
	Value int32
	Data  []byte
	Extra int32
}

// TestUnmarshalCanonicalDER_RejectsExtraElementInsideSequence covers the vector that
// the rest check alone cannot see. Rest reports bytes *after* the top-level
// TLV, but the malleability lives *inside* it: move the garbage into the
// SEQUENCE body, let the outer length grow to cover it, and rest is empty
// because the outer TLV did consume the whole input. The SEQUENCE body is then
// an unlimited channel for garbage, and each spelling is a distinct
// Identity.UniqueID() for one logical identity — the very bug the trailing-byte
// check was added to close.
func TestUnmarshalCanonicalDER_RejectsExtraElementInsideSequence(t *testing.T) {
	canonical, err := asn1.Marshal(refInt{Value: 7, Data: []byte("body")})
	require.NoError(t, err)

	smuggled, err := asn1.Marshal(refIntWithExtra{Value: 7, Data: []byte("body"), Extra: 42})
	require.NoError(t, err)
	require.NotEqual(t, canonical, smuggled, "the two encodings must really differ")

	// Baseline: the stdlib decodes the smuggled form into refInt without
	// complaint and with nothing left over, so the rest check sees a clean
	// parse. Both properties are what make this a bypass rather than noise.
	var lenient refInt
	rest, err := asn1.Unmarshal(smuggled, &lenient)
	require.NoError(t, err)
	require.Empty(t, rest, "the extra element is inside the SEQUENCE, so rest is empty — nothing for the rest check to catch")
	require.Equal(t, refInt{Value: 7, Data: []byte("body")}, lenient,
		"the smuggled form decodes to exactly the same value as the canonical one")

	// ...and the two spellings key the caches differently, which is why it matters.
	require.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(smuggled).UniqueID(),
		"UniqueID() hashes the raw bytes, so the smuggled form is a distinct cache key")

	require.ErrorIs(t, marshal.UnmarshalCanonicalDER(smuggled, &refInt{}), marshal.ErrNonCanonical,
		"an element smuggled inside the SEQUENCE must be rejected")
}

// TestUnmarshalCanonicalDER_RejectsAlternateStringTags covers a second spelling the
// rest check cannot see: encoding/asn1 accepts T61String, IA5String and
// GeneralString wherever a struct field asks for a UTF8String, so flipping one
// tag byte yields identical decoded content under different raw bytes.
func TestUnmarshalCanonicalDER_RejectsAlternateStringTags(t *testing.T) {
	canonical, err := asn1.Marshal(refUTF8{Value: "policy", Data: []byte("body")})
	require.NoError(t, err)

	// The UTF8String tag is the first element's tag, at the first byte of the
	// SEQUENCE body: [SEQUENCE, len, tag, ...].
	require.Equal(t, byte(asn1.TagUTF8String), canonical[2], "fixture assumption: canonical[2] is the string tag")

	for _, tag := range []byte{
		byte(asn1.TagT61String),
		byte(asn1.TagIA5String),
		byte(asn1.TagGeneralString),
	} {
		retagged := append([]byte{}, canonical...)
		retagged[2] = tag

		// Baseline: same value, no leftovers — invisible to the rest check.
		var lenient refUTF8
		rest, err := asn1.Unmarshal(retagged, &lenient)
		require.NoError(t, err, "tag 0x%02X: encoding/asn1 accepts this where UTF8String was declared", tag)
		require.Empty(t, rest)
		require.Equal(t, "policy", lenient.Value)

		require.ErrorIs(t, marshal.UnmarshalCanonicalDER(retagged, &refUTF8{}), marshal.ErrNonCanonical,
			"tag 0x%02X: a non-UTF8String spelling of the same string must be rejected", tag)
	}
}

// TestUnmarshalCanonicalDER_AcceptsWhatStdlibMarshalEmits is the guard against
// over-tightening. The canonicality check is a re-encode-and-compare, so it can
// only ever reject what encoding/asn1.Marshal would not have produced; every
// producer in this tree goes through that same Marshal. Values are chosen to
// straddle the short/long-form length boundary and to cover the nil, empty and
// multi-element slice shapes the four real call sites carry.
func TestUnmarshalCanonicalDER_AcceptsWhatStdlibMarshalEmits(t *testing.T) {
	for _, size := range []int{0, 1, 0x7F, 0x80, 0x100} {
		canonical, err := asn1.Marshal(refInt{Value: 42, Data: make([]byte, size)})
		require.NoError(t, err)
		require.NoError(t, marshal.UnmarshalCanonicalDER(canonical, &refInt{}),
			"a %d-byte payload written by encoding/asn1.Marshal must remain decodable", size)
	}

	// Slice-of-slice shapes: MultiIdentity, PolicyIdentity, MultiSignature and
	// PolicySignature all carry one, and a signature slot is legitimately nil
	// when an OR branch went unsigned.
	type refSlices struct {
		Items [][]byte
	}
	for name, items := range map[string][][]byte{
		"nil slice":          nil,
		"empty slice":        {},
		"nil element":        {nil},
		"empty element":      {{}},
		"mixed elements":     {[]byte("a"), nil, []byte("ccc")},
		"long-form elements": {make([]byte, 0x80), make([]byte, 0x100)},
	} {
		canonical, err := asn1.Marshal(refSlices{Items: items})
		require.NoError(t, err)
		require.NoError(t, marshal.UnmarshalCanonicalDER(canonical, &refSlices{}),
			"%s: encoding/asn1.Marshal's own output must survive the round-trip check", name)
	}
}

// TestUnmarshalCanonicalDER_FalseRejectsOptionalAndOmitEmpty documents the boundary of
// what UnmarshalCanonicalDER can be pointed at, and fails if that boundary moves.
//
// The check establishes "b is something asn1.Marshal would have emitted for this
// type", which is narrower than "b is valid DER". For a field tagged optional or
// omitempty, asn1.Marshal drops the field when it holds the zero value while
// asn1.Unmarshal accepts it explicitly present — a legal encoding the round-trip
// then rejects. That is a false rejection, not a caught attack.
//
// None of the four identity envelopes carries such a field, so nothing is broken
// today; this test exists so a fifth caller with one of these tags trips over it
// here rather than in production. See the constraint list on UnmarshalCanonicalDER.
func TestUnmarshalCanonicalDER_FalseRejectsOptionalAndOmitEmpty(t *testing.T) {
	// Every field explicitly present, which is legal DER in all three cases.
	explicitBytes, err := asn1.Marshal(struct {
		A int32
		B []byte
	}{A: 1, B: []byte{}})
	require.NoError(t, err)

	explicitInt, err := asn1.Marshal(struct {
		A int32
		B int32
	}{A: 1, B: 0})
	require.NoError(t, err)

	var omitEmpty struct {
		A int32
		B []byte `asn1:"omitempty"`
	}
	var optional struct {
		A int32
		B int32 `asn1:"optional"`
	}

	require.ErrorIs(t, marshal.UnmarshalCanonicalDER(explicitBytes, &omitEmpty), marshal.ErrNonCanonical,
		"omitempty: an explicitly-present empty field is legal DER but not what asn1.Marshal emits — do not point UnmarshalCanonicalDER at such a type")
	require.ErrorIs(t, marshal.UnmarshalCanonicalDER(explicitInt, &optional), marshal.ErrNonCanonical,
		"optional: an explicitly-present zero field is legal DER but not what asn1.Marshal emits — do not point UnmarshalCanonicalDER at such a type")

	// A `default:` field is the one that looks similar but is genuinely
	// non-canonical: DER requires a value equal to the default to be absent, so
	// rejecting the explicit spelling is correct rather than a false rejection.
	explicitDefault, err := asn1.Marshal(struct {
		A int32
		B int32
	}{A: 1, B: 7})
	require.NoError(t, err)

	var withDefault struct {
		A int32
		B int32 `asn1:"optional,default:7"`
	}
	require.ErrorIs(t, marshal.UnmarshalCanonicalDER(explicitDefault, &withDefault), marshal.ErrNonCanonical,
		"default: DER requires a value equal to the default to be absent, so this rejection is correct")
}

// TestUnmarshalCanonicalDER_FourCallSitesHaveNoOptionalFields is the guard that keeps
// the constraint above true of the real types, so the documented caveat cannot
// quietly stop applying if someone adds a field.
func TestUnmarshalCanonicalDER_FourCallSitesHaveNoOptionalFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[multisig.MultiIdentity](),
		reflect.TypeFor[multisig.MultiSignature](),
		reflect.TypeFor[boolpolicy.PolicyIdentity](),
		reflect.TypeFor[boolpolicy.PolicySignature](),
	} {
		for f := range typ.Fields() {
			tag := f.Tag.Get("asn1")
			for _, bad := range []string{"optional", "omitempty", "default"} {
				assert.NotContains(t, tag, bad,
					"%s.%s carries asn1:%q — UnmarshalCanonicalDER round-trips through asn1.Marshal and would false-reject legal encodings of this field",
					typ.Name(), f.Name, tag)
			}
			assert.NotEqual(t, reflect.TypeFor[time.Time](), f.Type,
				"%s.%s is a time.Time — UTCTime and GeneralizedTime both decode but asn1.Marshal emits only one, so UnmarshalCanonicalDER would false-reject the other",
				typ.Name(), f.Name)
		}
	}
}

// TestUnmarshalCanonicalDER_RejectsInvalidDestination pins the one destination
// misuse that is still expressible. The signature takes *T, so the other two are
// now compile errors rather than test cases:
//
//	marshal.UnmarshalCanonicalDER(b, refInt{})  // non-pointer: does not compile
//	marshal.UnmarshalCanonicalDER(b, nil)       // untyped nil: T cannot be inferred
//
// A typed nil pointer still type-checks, and encoding/asn1 would reject it only
// as an untyped structure error indistinguishable from "these bytes are
// malformed" — the distinction this package's sentinel errors exist to make.
func TestUnmarshalCanonicalDER_RejectsInvalidDestination(t *testing.T) {
	canonical, err := asn1.Marshal(refInt{Value: 7, Data: []byte("body")})
	require.NoError(t, err)

	var nilPtr *refInt
	require.ErrorIs(t, marshal.UnmarshalCanonicalDER(canonical, nilPtr), marshal.ErrInvalidDestination,
		"a nil destination is a caller bug and must not be reported as a problem with the bytes")

	// The guard must not have cost the happy path: the same bytes into a proper
	// destination still decode.
	var dst refInt
	require.NoError(t, marshal.UnmarshalCanonicalDER(canonical, &dst))
	require.Equal(t, int32(7), dst.Value)
}

// TestUnmarshalCanonicalDER_InvalidDestinationIsNotAByteError is the distinction
// the guard exists to make: ErrInvalidDestination must not be confused with any
// of the sentinels that describe the input, so a caller branching on "was this a
// malleability rejection?" cannot be misled by its own programming error.
func TestUnmarshalCanonicalDER_InvalidDestinationIsNotAByteError(t *testing.T) {
	var nilPtr *refInt
	err := marshal.UnmarshalCanonicalDER([]byte("not asn1"), nilPtr)
	require.ErrorIs(t, err, marshal.ErrInvalidDestination,
		"the destination is checked before the bytes, so a bad destination wins even for garbage input")

	for _, byteErr := range []error{
		marshal.ErrNonCanonical, marshal.ErrTrailingBytes,
		marshal.ErrNonMinimalLen, marshal.ErrNonMinimalInt,
	} {
		require.NotErrorIs(t, err, byteErr)
	}
}
