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
// serialization (for example hashing the 0x04 prefix byte) cannot pass unnoticed.
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

	// A scalar at or above the curve order must be rejected rather than silently reduced modulo the
	// order into a different, unrelated key.
	overflowing := bytes.Repeat([]byte{0xff}, PrivateKeyLength)
	_, err = NewSignerFromBytes(overflowing)
	require.Error(t, err, "a scalar exceeding the curve order must be rejected")
}

// TestGoldenFixtureEndorsement pins the fixture endorsement (statedelta_digest_fixture.json): the
// signer must reproduce, byte for byte, the committed signatures over the frozen digest with the
// well-known test keys 1 and 2 (RFC 6979 makes them deterministic; they were independently validated
// with ethers v6). The Solidity side accepts the same bytes in test/GoEndorsement.t.sol, closing the
// Go -> contract signature loop the forge suites previously simulated with vm.sign.
func TestGoldenFixtureEndorsement(t *testing.T) {
	digest, err := hexTo32("40126b61e08efb5a33787c559f3e7178ad5c006fb7429062b54d442c6a0680e8")
	require.NoError(t, err)

	expected := []string{
		"a5d457ae780201b59f8db4f289d8da196efa221af4238f9ffa91c6fd3c9911a91fa9e818dad23a0f90c63b169d7eec329aab8dc57000a2fe89ee6075a8cc28e11b",
		"1eb6b84df3e54f94499db60106da76fc74bef5b3ba81d4def15dd5fa84b88a52597013c0464375e0a09b15cb1c32b621fc1ecad44771b0d3fd2b3aa5909fd8b11b",
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
