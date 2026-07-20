/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// keyScalar returns a 32-byte private-key scalar with the given low byte, e.g. keyScalar(1) is the
// well-known test key 0x...01.
func keyScalar(v byte) []byte {
	raw := make([]byte, PrivateKeyLength)
	raw[PrivateKeyLength-1] = v

	return raw
}

func testSigner(t *testing.T, scalarLowByte byte) *Signer {
	t.Helper()
	s, err := NewSignerFromBytes(keyScalar(scalarLowByte))
	require.NoError(t, err)

	return s
}

// TestGoldenAddresses pins the address derivation against the well-known addresses of private keys
// 1 and 2. These are independently reproducible with any Ethereum tooling, so a wrong pubkey
// serialization (for example hashing the 0x04 prefix byte, design §8) cannot pass unnoticed.
func TestGoldenAddresses(t *testing.T) {
	// client.Address.Hex() is plain lowercase hex (no EIP-55 checksum casing).
	assert.Equal(t, "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf", testSigner(t, 1).Address().Hex())
	assert.Equal(t, "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf", testSigner(t, 2).Address().Hex())
}

// TestSignFormat checks the wire format the contract expects: 65 bytes, v in {27,28}, low-s.
func TestSignFormat(t *testing.T) {
	s := testSigner(t, 1)
	for _, msg := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		sig, err := s.Sign(crypto.Keccak256Hash([]byte(msg)))
		require.NoError(t, err)
		assert.Len(t, sig, SignatureLength)
		assert.Contains(t, []byte{27, 28}, sig[64], "v must be 27 or 28")
		assert.LessOrEqual(t, bytes.Compare(sig[32:64], secp256k1HalfN[:]), 0, "s must be low")
	}
}

// TestSignDeterministic pins RFC 6979 determinism: the same key and digest always produce the same
// signature. The Phase-B fixture endorsement relies on this to stay reproducible.
func TestSignDeterministic(t *testing.T) {
	s := testSigner(t, 1)
	digest := crypto.Keccak256Hash([]byte("deterministic"))
	sig1, err := s.Sign(digest)
	require.NoError(t, err)
	sig2, err := s.Sign(digest)
	require.NoError(t, err)
	assert.Equal(t, sig1, sig2)
}

// TestSignRecoverRoundTrip is the core property: RecoverAddress(digest, Sign(digest)) yields the
// signer's address, for multiple keys and digests.
func TestSignRecoverRoundTrip(t *testing.T) {
	for _, low := range []byte{1, 2, 0x42} {
		s := testSigner(t, low)
		for _, msg := range []string{"issue", "transfer", "setup"} {
			digest := crypto.Keccak256Hash([]byte(msg))
			sig, err := s.Sign(digest)
			require.NoError(t, err)

			got, err := RecoverAddress(digest, sig)
			require.NoError(t, err)
			assert.Equal(t, s.Address(), got)
		}
	}
}

// TestRecoverTamperedDigest checks that a signature over one digest does not recover the signer's
// address for another digest (it recovers an unrelated address, which the contract then rejects as
// an unauthorized signer).
func TestRecoverTamperedDigest(t *testing.T) {
	s := testSigner(t, 1)
	sig, err := s.Sign(crypto.Keccak256Hash([]byte("original")))
	require.NoError(t, err)

	got, err := RecoverAddress(crypto.Keccak256Hash([]byte("tampered")), sig)
	require.NoError(t, err)
	assert.NotEqual(t, s.Address(), got)
}

// TestRecoverRejectsMalformed mirrors the contract's format rules on the Go side.
func TestRecoverRejectsMalformed(t *testing.T) {
	s := testSigner(t, 1)
	digest := crypto.Keccak256Hash([]byte("msg"))
	sig, err := s.Sign(digest)
	require.NoError(t, err)

	t.Run("wrong length", func(t *testing.T) {
		_, err := RecoverAddress(digest, sig[:64])
		require.Error(t, err)
	})

	t.Run("bad v", func(t *testing.T) {
		for _, v := range []byte{0, 1, 26, 29} {
			bad := append([]byte(nil), sig...)
			bad[64] = v
			_, err := RecoverAddress(digest, bad)
			require.Error(t, err, "v=%d must be rejected", v)
		}
	})

	t.Run("high s", func(t *testing.T) {
		// The malleable counterpart: s' = N - s (and v flipped) recovers to the same address but
		// must be rejected by the low-s rule, exactly as on-chain.
		n := []byte{
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe,
			0xba, 0xae, 0xdc, 0xe6, 0xaf, 0x48, 0xa0, 0x3b, 0xbf, 0xd2, 0x5e, 0x8c, 0xd0, 0x36, 0x41, 0x41,
		}
		bad := append([]byte(nil), sig...)
		borrow := 0
		for i := 31; i >= 0; i-- {
			d := int(n[i]) - int(sig[32+i]) - borrow
			if d < 0 {
				d += 256
				borrow = 1
			} else {
				borrow = 0
			}
			bad[32+i] = byte(d) // #nosec G115 -- d is normalized to [0,255] by the borrow above
		}
		if bad[64] == 27 {
			bad[64] = 28
		} else {
			bad[64] = 27
		}
		_, err := RecoverAddress(digest, bad)
		require.Error(t, err, "high-s signature must be rejected")
	})
}

// TestNewSignerFromBytesRejectsInvalid covers the key-material guards.
func TestNewSignerFromBytesRejectsInvalid(t *testing.T) {
	_, err := NewSignerFromBytes(make([]byte, 31))
	require.Error(t, err, "short scalar must be rejected")

	_, err = NewSignerFromBytes(make([]byte, PrivateKeyLength))
	require.Error(t, err, "zero scalar must be rejected")
}

// TestGoldenFixtureEndorsement pins the fixture endorsement (statedelta_digest_fixture.json): the
// signer must reproduce, byte for byte, the committed signatures over the frozen digest with the
// well-known test keys 1 and 2 (RFC 6979 makes them deterministic; they were independently validated
// with ethers v6). The Solidity side accepts the same bytes in test/GoEndorsement.t.sol, closing the
// Go -> contract signature loop the forge suites previously simulated with vm.sign.
func TestGoldenFixtureEndorsement(t *testing.T) {
	digest, err := hexTo32("c9326b72636896424aabe0039efef420df6cd18811b82db3237260110f39b64d")
	require.NoError(t, err)

	expected := []string{
		"61f5eef325cc3086223597422c5217d0dfdc88ee5dfe9d223b54d9030ff3900b08f17c1fa26cf286bab6ef7d83e69c2ee58da218bd51cd6ffd31af44e561ecbd1c",
		"c81abb08a72fe90d3658b2e819aebb1a02e1be82001d46d4a9ccdd7747efb46e6267065c0a8ac036ed2a4a9bb94a9384956c00e2878dfc50562ebe323778663e1c",
	}
	for i, low := range []byte{1, 2} {
		sig, err := testSigner(t, low).Sign(digest)
		require.NoError(t, err)
		assert.Equal(t, expected[i], hex.EncodeToString(sig), "key %d signature diverges from the fixture", low)
	}
}

func hexTo32(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, err
	}
	copy(out[:], b)

	return out, nil
}
