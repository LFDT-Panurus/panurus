/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package marshal_test

import (
	"bytes"
	"encoding/asn1"
	"math"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Reference structs — drive encoding/asn1.Marshal to emit specific wire tags.
// The struct tags control which ASN.1 string type gets written on the wire.
// ---------------------------------------------------------------------------

type refInt struct {
	Value int32
	Data  []byte
}

// refUTF8 → emits UTF8String (tag 0x0C) per asn1.TagUTF8String
type refUTF8 struct {
	Value string `asn1:"utf8"`
	Data  []byte
}

// refPrintable → emits PrintableString (tag 0x13) per asn1.TagPrintableString.
// Valid characters are restricted to: A-Z a-z 0-9 space ' ( ) + , - . / : = ?
type refPrintable struct {
	Value string `asn1:"printable"`
	Data  []byte
}

// ---------------------------------------------------------------------------
// Helpers — testing.TB works for both *testing.T and *testing.B
// ---------------------------------------------------------------------------

func marshalInt(tb testing.TB, v int32, data []byte) []byte {
	tb.Helper()
	b, err := asn1.Marshal(refInt{Value: v, Data: data})
	if err != nil {
		tb.Fatalf("asn1.Marshal(int): %v", err)
	}

	return b
}

func marshalUTF8(tb testing.TB, s string, data []byte) []byte {
	tb.Helper()
	b, err := asn1.Marshal(refUTF8{Value: s, Data: data})
	if err != nil {
		tb.Fatalf("asn1.Marshal(utf8): %v", err)
	}

	return b
}

func marshalPrintable(tb testing.TB, s string, data []byte) []byte {
	tb.Helper()
	b, err := asn1.Marshal(refPrintable{Value: s, Data: data})
	if err != nil {
		tb.Fatalf("asn1.Marshal(printable): %v", err)
	}

	return b
}

// dataEq treats nil and []byte{} as equal: DER OCTET STRING length 0
// always decodes to a non-nil empty slice regardless of the original nil input.
func dataEq(a, b []byte) bool {
	return len(a) == 0 && len(b) == 0 || bytes.Equal(a, b)
}

// seqByte is byte(asn1.TagSequence)|0x20 — the DER SEQUENCE tag.
// Defined once so error-case byte literals stay readable.
const seqByte = byte(asn1.TagSequence) | 0x20 // 0x30

// ---------------------------------------------------------------------------
// Integer variant
// ---------------------------------------------------------------------------

func TestDecodeIntVariant(t *testing.T) {
	tests := []struct {
		name  string
		value int32
		data  []byte
	}{
		{"zero/nil-data", 0, nil},
		{"zero/empty-data", 0, []byte{}},
		{"positive/small", 42, []byte{0x01, 0x02, 0x03}},
		{"positive/max-int32", math.MaxInt32, []byte("hello")},
		{"negative/-1", -1, []byte{0xFF}},
		{"negative/min-int32", math.MinInt32, []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		{"large-data/300-bytes", 1, bytes.Repeat([]byte{0xAB}, 300)},
		{"single-byte-data", 100, []byte{0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := marshalInt(t, tt.value, tt.data)
			got, err := marshal.DecodeIdentity(enc)
			if err != nil {
				t.Fatalf("Decode(): %v", err)
			}
			if !got.IsInt {
				t.Fatal("IsInt = false, want true")
			}
			if got.Int32 != tt.value {
				t.Errorf("Int32 = %d, want %d", got.Int32, tt.value)
			}
			if !dataEq(got.Data, tt.data) {
				t.Errorf("Data = %x, want %x", got.Data, tt.data)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UTF8String variant
// ---------------------------------------------------------------------------

func TestDecodeUTF8Variant(t *testing.T) {
	tests := []struct {
		name  string
		value string
		data  []byte
	}{
		{"empty-str/nil-data", "", nil},
		{"ascii", "hello", []byte{0x01, 0x02, 0x03}},
		{"latin-ext", "héllo wörld", []byte{0xAA, 0xBB}},
		{"cjk", "日本語", []byte{0xCA, 0xFE}},
		{"emoji", "Go: 高速 ASN.1 \U0001F680", []byte("payload")},
		{"empty-data", "test", []byte{}},
		{"long-str/200", string(bytes.Repeat([]byte("x"), 200)), []byte{0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := marshalUTF8(t, tt.value, tt.data)
			got, err := marshal.DecodeIdentity(enc)
			if err != nil {
				t.Fatalf("Decode(): %v", err)
			}
			if got.IsInt {
				t.Fatal("IsInt = true, want false")
			}
			if got.Str != tt.value {
				t.Errorf("Str = %q, want %q", got.Str, tt.value)
			}
			if !dataEq(got.Data, tt.data) {
				t.Errorf("Data = %x, want %x", got.Data, tt.data)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PrintableString variant
// Valid charset: A-Z  a-z  0-9  space  ' ( ) + , - . / : = ?
// ---------------------------------------------------------------------------

func TestDecodePrintableVariant(t *testing.T) {
	tests := []struct {
		name  string
		value string
		data  []byte
	}{
		{"empty-str", "", nil},
		{"ascii-lower", "hello world", []byte{0x01, 0x02}},
		{"ascii-upper", "HELLO WORLD", []byte{0xAA}},
		{"mixed-case", "Hello World", []byte("payload")},
		{"alphanumeric", "ABC123", []byte{0x00}},
		{"with-punctuation", "CN=Angelo De Caro", []byte{0xFF}},
		{"colon-slash", "http://example.com", []byte{0x01}},
		{"plus-equals", "key=value+extra", []byte{0x02}},
		{"empty-data", "test", []byte{}},
		{"long-str/200", string(bytes.Repeat([]byte("A"), 200)), []byte{0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := marshalPrintable(t, tt.value, tt.data)

			// Dynamically find where the first field's tag starts
			// by skipping the SEQUENCE length encoding.
			tagIdx := 2
			if len(enc) > 1 && enc[1] > 0x80 {
				// enc[1] & 0x7F gives the number of length bytes
				tagIdx += int(enc[1] & 0x7F)
			}

			wantTag := byte(asn1.TagPrintableString) // 0x13
			if len(enc) > tagIdx && enc[tagIdx] != wantTag {
				t.Fatalf("encoding/asn1 did not emit PrintableString: got tag 0x%02x at index %d, want 0x%02x",
					enc[tagIdx], tagIdx, wantTag)
			}

			got, err := marshal.DecodeIdentity(enc)
			if err != nil {
				t.Fatalf("Decode(): %v", err)
			}
			if got.IsInt {
				t.Fatal("IsInt = true, want false")
			}
			if got.Str != tt.value {
				t.Errorf("Str = %q, want %q", got.Str, tt.value)
			}
			if !dataEq(got.Data, tt.data) {
				t.Errorf("Data = %x, want %x", got.Data, tt.data)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Round-trip: our Encode → our Decode
// ---------------------------------------------------------------------------

func TestRoundTripInt(t *testing.T) {
	cases := []struct {
		v int32
		d []byte
	}{
		{0, nil},
		{42, []byte("round-trip")},
		{math.MaxInt32, []byte{0xFF, 0x00}},
		{math.MinInt32, []byte{0x01}},
		{-100, bytes.Repeat([]byte{0x55}, 128)},
	}
	for _, c := range cases {
		r := marshal.Result{IsInt: true, Int32: c.v, Data: c.d}
		enc := marshal.Encode(r)
		got, err := marshal.DecodeIdentity(enc)
		if err != nil {
			t.Fatalf("Decode(Encode(int32(%d))): %v", c.v, err)
		}
		if !got.IsInt || got.Int32 != c.v || !dataEq(got.Data, c.d) {
			t.Errorf("mismatch: got {IsInt:%v Int32:%d}, want {true %d}", got.IsInt, got.Int32, c.v)
		}
	}
}

func TestRoundTripStr(t *testing.T) {
	cases := []struct {
		s string
		d []byte
	}{
		{"", nil},
		{"hello", []byte{0x01, 0x02}},
		{"héllo 日本語 \U0001F680", []byte("data")},
	}
	for _, c := range cases {
		r := marshal.Result{IsInt: false, Str: c.s, Data: c.d}
		enc := marshal.Encode(r)
		got, err := marshal.DecodeIdentity(enc)
		if err != nil {
			t.Fatalf("Decode(Encode(str %q)): %v", c.s, err)
		}
		if got.IsInt || got.Str != c.s || !dataEq(got.Data, c.d) {
			t.Errorf("mismatch: got {IsInt:%v Str:%q}", got.IsInt, got.Str)
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-validation: our Encode → encoding/asn1.Unmarshal
// Verifies that our encoder emits canonical DER the stdlib accepts.
// Note: Unmarshal accepts UTF8String into a plain string field even with
// the `utf8` struct tag, and accepts PrintableString without any tag.
// ---------------------------------------------------------------------------

func TestEncodeProducesValidDER_Int(t *testing.T) {
	cases := []struct {
		v int32
		d []byte
	}{
		{42, []byte("data")},
		{math.MaxInt32, []byte{0x01}},
		{math.MinInt32, []byte{0xFF}},
		{-1, []byte("neg")},
		{0, []byte{}},
	}
	for _, c := range cases {
		enc := marshal.Encode(marshal.Result{IsInt: true, Int32: c.v, Data: c.d})
		var dst refInt
		rest, err := asn1.Unmarshal(enc, &dst)
		if err != nil {
			t.Errorf("asn1.Unmarshal(Encode(int32(%d))): %v", c.v, err)

			continue
		}
		if len(rest) != 0 {
			t.Errorf("trailing bytes: %x", rest)
		}
		if dst.Value != c.v || !dataEq(dst.Data, c.d) {
			t.Errorf("value mismatch: got %d, want %d", dst.Value, c.v)
		}
	}
}

func TestEncodeProducesValidDER_Str(t *testing.T) {
	cases := []struct {
		s string
		d []byte
	}{
		{"hello", []byte("data")},
		{"unicode 日本", []byte{0x01}},
		{"", []byte{0xAB}},
	}
	for _, c := range cases {
		enc := marshal.Encode(marshal.Result{IsInt: false, Str: c.s, Data: c.d})
		var dst refUTF8
		rest, err := asn1.Unmarshal(enc, &dst)
		if err != nil {
			t.Errorf("asn1.Unmarshal(Encode(str %q)): %v", c.s, err)

			continue
		}
		if len(rest) != 0 {
			t.Errorf("trailing bytes: %x", rest)
		}
		if dst.Value != c.s || !dataEq(dst.Data, c.d) {
			t.Errorf("value mismatch: got %q, want %q", dst.Value, c.s)
		}
	}
}

// ---------------------------------------------------------------------------
// Error cases — byte literals use encoding/asn1 tag constants
// ---------------------------------------------------------------------------

func TestDecodeErrors(t *testing.T) {
	validInt := marshalInt(t, 42, []byte{0x01})

	tests := []struct {
		name    string
		input   []byte
		wantErr error
	}{
		{
			"empty input",
			[]byte{},
			marshal.ErrUnexpectedTag,
		},
		{
			"wrong outer tag — OCTET STRING instead of SEQUENCE",
			[]byte{byte(asn1.TagOctetString), 0x03, 0x01, 0x02, 0x03},
			marshal.ErrUnexpectedTag,
		},
		{
			"unsupported first field tag — BOOLEAN",
			[]byte{
				seqByte, 0x06,
				byte(asn1.TagBoolean), 0x01, 0xFF,
				byte(asn1.TagOctetString), 0x01, 0x00,
			},
			marshal.ErrUnexpectedTag,
		},
		{
			"unsupported first field tag — BIT STRING",
			[]byte{
				seqByte, 0x05,
				byte(asn1.TagBitString), 0x03, 0x00, 0xFF, 0x00,
			},
			marshal.ErrUnexpectedTag,
		},
		{
			"truncated — only outer tag byte, no length",
			[]byte{seqByte},
			marshal.ErrTruncated,
		},
		{
			"integer present but no OCTET STRING follows",
			func() []byte {
				b, _ := asn1.Marshal(struct{ V int32 }{1})

				return b
			}(),
			marshal.ErrTruncated,
		},
		{
			"integer body truncated",
			// SEQUENCE { INTEGER claims 4 bytes but only 1 present }
			[]byte{seqByte, 0x04, byte(asn1.TagInteger), 0x04, 0x01},
			marshal.ErrTruncated,
		},
		{
			"first byte mutated to wrong tag",
			func() []byte {
				b := make([]byte, len(validInt))
				copy(b, validInt)
				b[0] = byte(asn1.TagInteger) // INTEGER instead of SEQUENCE

				return b
			}(),
			marshal.ErrUnexpectedTag,
		},
		{
			"zero-length integer",
			// SEQUENCE { INTEGER (len 0), OCTET STRING }
			[]byte{seqByte, 0x05, byte(asn1.TagInteger), 0x00, byte(asn1.TagOctetString), 0x01, 0x00},
			marshal.ErrIntOverflow,
		},
		{
			"integer too large (6 bytes)",
			[]byte{seqByte, 0x0B, byte(asn1.TagInteger), 0x06, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, byte(asn1.TagOctetString), 0x01, 0x00},
			marshal.ErrIntOverflow,
		},
		{
			"integer overflow (max int32 exceeded)",
			// 0x00 0x80 0x00 0x00 0x00 is 2^31 (positive but requires 5 bytes or just slightly too large for int32)
			// math.MaxInt32 = 0x7FFFFFFF
			// 0x00 0x80 0x00 0x00 0x00 = 0x80000000 = 2147483648
			[]byte{seqByte, 0x0A, byte(asn1.TagInteger), 0x05, 0x00, 0x80, 0x00, 0x00, 0x00, byte(asn1.TagOctetString), 0x01, 0x00},
			marshal.ErrIntOverflow,
		},
		{
			"integer underflow (min int32 exceeded)",
			// 0x80 0x00 0x00 0x00 0x00 is -2^32 or something like that.
			// 0xFF 0x7F 0xFF 0xFF 0xFF = -0x80000001 = -2147483649
			[]byte{seqByte, 0x0A, byte(asn1.TagInteger), 0x05, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, byte(asn1.TagOctetString), 0x01, 0x00},
			marshal.ErrIntOverflow,
		},
		{
			"readLen truncated (n=2 but only 1 byte left)",
			[]byte{seqByte, 0x02, byte(asn1.TagInteger), 0x82, 0x01},
			marshal.ErrInvalidLen,
		},
		{
			"readLen n > 4",
			[]byte{seqByte, 0x07, byte(asn1.TagInteger), 0x85, 0x01, 0x02, 0x03, 0x04, 0x05},
			marshal.ErrInvalidLen,
		},
		{
			"readLen n=0 (0x80)",
			[]byte{seqByte, 0x02, byte(asn1.TagInteger), 0x80},
			marshal.ErrInvalidLen,
		},
		{
			"truncated OCTET STRING tag",
			[]byte{seqByte, 0x03, byte(asn1.TagInteger), 0x01, 0x00},
			marshal.ErrTruncated,
		},
		{
			"wrong tag for OCTET STRING",
			[]byte{seqByte, 0x05, byte(asn1.TagInteger), 0x01, 0x00, byte(asn1.TagInteger), 0x01, 0x00},
			marshal.ErrUnexpectedTag,
		},
		{
			"truncated OCTET STRING length",
			[]byte{seqByte, 0x04, byte(asn1.TagInteger), 0x01, 0x00, byte(asn1.TagOctetString)},
			marshal.ErrTruncated,
		},
		{
			"truncated OCTET STRING data",
			[]byte{seqByte, 0x06, byte(asn1.TagInteger), 0x01, 0x00, byte(asn1.TagOctetString), 0x02, 0x01},
			marshal.ErrTruncated,
		},
		{
			"truncated UTF8String data",
			[]byte{seqByte, 0x03, byte(asn1.TagUTF8String), 0x02, 0x01},
			marshal.ErrTruncated,
		},
		{
			"truncated after SEQUENCE tag",
			[]byte{seqByte, 0x01},
			marshal.ErrTruncated,
		},
		{
			"outer readLen failed",
			[]byte{seqByte, 0x81},
			marshal.ErrInvalidLen,
		},
		{
			"empty SEQUENCE",
			[]byte{seqByte, 0x00},
			marshal.ErrTruncated,
		},
		{
			"readLen failure inside UTF8String",
			[]byte{seqByte, 0x02, byte(asn1.TagUTF8String), 0x81},
			marshal.ErrInvalidLen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := marshal.DecodeIdentity(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Decode() err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// String compatibility
// ---------------------------------------------------------------------------

func TestStringCompatibility(t *testing.T) {
	tests := []struct {
		name         string
		str          string
		identityType int32
	}{
		{
			name:         "x509",
			str:          x509.IdentityTypeString,
			identityType: x509.IdentityType,
		},
		{
			name:         "idemix",
			str:          idemix.IdentityTypeString,
			identityType: idemix.IdentityType,
		},
		{
			name:         "idemixnym",
			str:          idemixnym.IdentityTypeString,
			identityType: idemixnym.IdentityType,
		},
		{
			name:         "multisig",
			str:          multisig.MultisigString,
			identityType: multisig.Multisig,
		},
		{
			name:         "htlc",
			str:          htlc.ScriptTypeString,
			identityType: htlc.ScriptType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := marshal.Encode(marshal.Result{
				Str:  tt.str,
				Data: []byte("an_identity"),
			})
			res, err := marshal.DecodeIdentity(raw)
			require.NoError(t, err)
			assert.True(t, res.IsInt)
			assert.Equal(t, tt.str, res.Str)
			assert.Equal(t, []byte("an_identity"), res.Data)
			assert.Equal(t, tt.identityType, res.Int32)
		})
	}
}

func TestEncodeIdentity(t *testing.T) {
	data := []byte("some-data")
	enc := marshal.EncodeIdentity(1, data)
	res, err := marshal.DecodeIdentity(enc)
	require.NoError(t, err)
	assert.True(t, res.IsInt)
	assert.Equal(t, int32(1), res.Int32)
	assert.Equal(t, data, res.Data)
}

func TestAppendTLVVariations(t *testing.T) {
	// Case l < 0x80 already covered by other tests, but let's be explicit
	r1 := marshal.Result{IsInt: true, Int32: 1, Data: bytes.Repeat([]byte{0x01}, 10)}
	enc1 := marshal.Encode(r1)
	res1, err := marshal.DecodeIdentity(enc1)
	require.NoError(t, err)
	assert.Equal(t, r1.Data, res1.Data)

	// Case 0x80 <= l < 0x100 (tag 0x81)
	r2 := marshal.Result{IsInt: true, Int32: 1, Data: bytes.Repeat([]byte{0x02}, 150)}
	enc2 := marshal.Encode(r2)
	assert.Equal(t, byte(0x81), enc2[len(enc2)-150-2]) // tag for long form (1 byte length)
	assert.Equal(t, byte(150), enc2[len(enc2)-150-1])  // length byte for OCTET STRING
	res2, err := marshal.DecodeIdentity(enc2)
	require.NoError(t, err)
	assert.Equal(t, r2.Data, res2.Data)

	// Case l >= 0x100 (tag 0x82)
	r3 := marshal.Result{IsInt: true, Int32: 1, Data: bytes.Repeat([]byte{0x03}, 300)}
	enc3 := marshal.Encode(r3)
	assert.Equal(t, byte(0x82), enc3[len(enc3)-300-3]) // tag for long form (2 byte length)
	assert.Equal(t, byte(300>>8), enc3[len(enc3)-300-2])
	assert.Equal(t, byte(300&0xFF), enc3[len(enc3)-300-1])
	res3, err := marshal.DecodeIdentity(enc3)
	require.NoError(t, err)
	assert.Equal(t, r3.Data, res3.Data)
}

// ---------------------------------------------------------------------------
// Benchmarks — fast decoder vs encoding/asn1 stdlib (paired for each variant)
// ---------------------------------------------------------------------------

var sink marshal.Result

func BenchmarkDecodeInt_Fast(b *testing.B) {
	enc := marshalInt(b, 42, []byte("benchmark payload"))
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		var err error
		sink, err = marshal.DecodeIdentity(enc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeUTF8_Fast(b *testing.B) {
	enc := marshalUTF8(b, "benchmark string", []byte("benchmark payload"))
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		var err error
		sink, err = marshal.DecodeIdentity(enc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodePrintable_Fast(b *testing.B) {
	enc := marshalPrintable(b, "benchmark string", []byte("benchmark payload"))
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		var err error
		sink, err = marshal.DecodeIdentity(enc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeInt_Stdlib(b *testing.B) {
	enc := marshalInt(b, 42, []byte("benchmark payload"))
	b.ResetTimer()
	b.ReportAllocs()
	var dst refInt
	for range b.N {
		if _, err := asn1.Unmarshal(enc, &dst); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeUTF8_Stdlib(b *testing.B) {
	enc := marshalUTF8(b, "benchmark string", []byte("benchmark payload"))
	b.ResetTimer()
	b.ReportAllocs()
	var dst refUTF8
	for range b.N {
		if _, err := asn1.Unmarshal(enc, &dst); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodePrintable_Stdlib(b *testing.B) {
	enc := marshalPrintable(b, "benchmark string", []byte("benchmark payload"))
	b.ResetTimer()
	b.ReportAllocs()
	var dst refPrintable
	for range b.N {
		if _, err := asn1.Unmarshal(enc, &dst); err != nil {
			b.Fatal(err)
		}
	}
}
