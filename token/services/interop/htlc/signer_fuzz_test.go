/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"crypto"
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

const maxFuzzClaimBytes = 64 << 10

// FuzzClaimVerifierVerifyNoPanic hunts for inputs that panic ClaimVerifier.Verify instead of
// returning an error. Both of its inputs are attacker-controlled: claimSignature is the signature
// an unlocking party supplies, and the HashInfo is read from the spent token's owner script, so
// hashFunc and hashEncoding are arbitrary uints that may name a function not linked in the binary.
func FuzzClaimVerifierVerifyNoPanic(f *testing.F) {
	preImage := []byte("secret")
	valid, err := json.Marshal(htlc.ClaimSignature{Preimage: preImage, RecipientSignature: []byte("sig")})
	require.NoError(f, err)

	seeds := [][]byte{
		nil,
		{},
		[]byte("not json"),
		[]byte("{"),
		[]byte(`{}`),
		[]byte(`{"Preimage":null,"RecipientSignature":null}`),
		valid,
	}
	// Available functions, plus the out-of-range values that make New() return nil (#2558).
	encodings := []uint{uint(encoding.None), uint(encoding.Base64), uint(encoding.Hex), 99}
	hashFuncs := []uint{uint(crypto.SHA256), uint(crypto.SHA512), 0, 999}
	for _, seed := range seeds {
		for _, e := range encodings {
			for _, h := range hashFuncs {
				f.Add(seed, h, e)
			}
		}
	}

	f.Fuzz(func(t *testing.T, claimSignature []byte, hashFunc, hashEncoding uint) {
		if len(claimSignature) > maxFuzzClaimBytes {
			t.Skip("input beyond the size any claim signature can reach in practice")
		}
		verifier := &mock.Verifier{}
		verifier.VerifyReturns(nil)

		cv := &htlc.ClaimVerifier{
			Recipient: verifier,
			HashInfo: htlc.HashInfo{
				Hash:         []byte("image"),
				HashFunc:     crypto.Hash(hashFunc),
				HashEncoding: encoding.Encoding(hashEncoding),
			},
		}
		require.NotPanics(t, func() {
			_ = cv.Verify([]byte("req"), claimSignature)
		})
	})
}
