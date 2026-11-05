/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"encoding/asn1"
	"testing"

	"github.com/stretchr/testify/require"
)

// fuzzSeeds is valid material generated once per fuzz target, with
// truncated and malformed variants of it.
type fuzzSeeds struct {
	w                   *testWallet
	id, sig, ai, info   []byte
	msg                 []byte
	conf, remoteConfRaw []byte
}

func newFuzzSeeds(f *testing.F) *fuzzSeeds {
	f.Helper()
	w := newTestWallet(f, "alice")
	d, err := w.km.Identity(f.Context(), nil)
	require.NoError(f, err)
	msg := []byte("m")
	sig, err := d.Signer.Sign(msg)
	require.NoError(f, err)
	conf, err := (&Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}).Marshal()
	require.NoError(f, err)
	remote, err := (&Config{Version: ProtocolVersionV1, Name: "remote", IPK: w.ipk}).Marshal()
	require.NoError(f, err)

	return &fuzzSeeds{w: w, id: d.Identity, sig: sig, ai: d.AuditInfo, info: d.SignerInfo, msg: msg, conf: conf, remoteConfRaw: remote}
}

// variants returns raw, empty, truncated and malformed versions of raw.
func variants(raw []byte) [][]byte {
	flipped := append([]byte(nil), raw...)
	flipped[len(flipped)/2] ^= 0x01
	out := [][]byte{
		raw,
		{},
		raw[:1],
		raw[:len(raw)/2],
		raw[:len(raw)-1],
		append(append([]byte(nil), raw...), 0),
		flipped,
	}
	if raw[0] == '{' {
		out = append(out,
			append(append([]byte(nil), raw...), '}'),
			append(append([]byte(nil), raw...), ']'),
			append([]byte(`{"x":1,`), raw[1:]...),
		)
	}

	return out
}

func FuzzDeserializers(f *testing.F) {
	s := newFuzzSeeds(f)
	des, err := NewDeserializer(s.w.ipk)
	require.NoError(f, err)
	f.Cleanup(des.Close)
	f.Add([]byte{0x30, 0x00}, []byte("{}"), []byte{0x30, 0x00})
	// identities of the right length: zero, a coefficient out of range, a bad end marker
	zero := make([]byte, len(s.id))
	high := append([]byte(nil), s.id...)
	high[0], high[1] = 0xff, 0xff
	marker := append([]byte(nil), s.id...)
	marker[len(marker)-1] = 0x03
	for _, id := range [][]byte{zero, high, marker} {
		f.Add(id, s.ai, s.sig)
	}
	// signatures with swapped, short or extra elements
	sig, err := unmarshalSignature(s.sig)
	require.NoError(f, err)
	swapped, err := asn1.Marshal(Signature{NymSK: sig.NymRH, NymRH: sig.NymSK, Pres: sig.Pres, Sok: sig.Sok})
	require.NoError(f, err)
	short, err := asn1.Marshal(Signature{NymSK: sig.NymSK[1:], NymRH: sig.NymRH, Pres: sig.Pres, Sok: sig.Sok})
	require.NoError(f, err)
	extra, err := asn1.Marshal(struct{ A, B, C, D, E []byte }{sig.NymSK, sig.NymRH, sig.Pres, sig.Sok, []byte{0}})
	require.NoError(f, err)
	for _, sigma := range [][]byte{swapped, short, extra} {
		f.Add(s.id, s.ai, sigma)
	}
	for _, id := range variants(s.id) {
		f.Add(id, s.ai, s.sig)
	}
	for _, ai := range variants(s.ai) {
		f.Add(s.id, ai, s.sig)
	}
	for _, sig := range variants(s.sig) {
		f.Add(s.id, s.ai, sig)
	}
	f.Fuzz(func(t *testing.T, id, ai, sig []byte) {
		ctx := context.Background()
		if v, err := des.DeserializeVerifier(ctx, id); err == nil {
			_ = v.Verify(s.msg, sig)
		}
		_, _ = DeserializeAuditInfo(ai)
		_ = des.MatchIdentity(ctx, id, ai)
		_, _ = des.DeserializeAuditInfo(ctx, id, ai)
		_, _ = des.Info(ctx, id, ai)
	})
}

func FuzzDeserializeAuditInfo(f *testing.F) {
	s := newFuzzSeeds(f)
	for _, ai := range variants(s.ai) {
		f.Add(ai)
	}
	f.Add([]byte("{}"))
	f.Add([]byte(`{"eid":"a","EID":"b"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		ai, err := DeserializeAuditInfo(raw)
		if err != nil {
			return
		}
		// canonical: what is accepted re-encodes to the input
		again, err := ai.Bytes()
		require.NoError(t, err)
		require.Equal(t, raw, again)
	})
}

func FuzzSignerInfoDecode(f *testing.F) {
	s := newFuzzSeeds(f)
	for _, info := range variants(s.info) {
		f.Add(s.id, info)
	}
	for _, id := range variants(s.id) {
		f.Add(id, s.info)
	}
	f.Add([]byte{}, []byte("{}"))
	f.Fuzz(func(t *testing.T, id, raw []byte) {
		info, err := decodeSignerInfo(raw)
		if err != nil {
			return
		}
		if _, err := openOpening(s.w.km.siKey, signerInfoAD(id, info.NymRH, info.Pres), info.EncOpeningSK); err != nil {
			return
		}
		// only the stored signer info of the stored identity decrypts
		require.Equal(t, s.id, id)
		require.Equal(t, s.info, raw)
	})
}

func FuzzNewConfigFromRaw(f *testing.F) {
	s := newFuzzSeeds(f)
	for _, c := range variants(s.conf) {
		f.Add(c)
	}
	for _, c := range variants(s.remoteConfRaw) {
		f.Add(c)
	}
	f.Add([]byte(`{"version":1,"VERSION":2}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = NewConfigFromRaw(s.w.ipk, raw)
	})
}
