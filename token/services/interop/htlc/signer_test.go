/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"crypto"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

// ---- ClaimSigner ----

func TestClaimSignerSign(t *testing.T) {
	preImage := []byte("secret")
	tokenReq := []byte("txrequest")
	expectedSig := []byte("recipient-sig")

	signer := &mock.Signer{}
	signer.SignReturns(expectedSig, nil)

	cs := &htlc.ClaimSigner{Recipient: signer, Preimage: preImage}
	result, err := cs.Sign(tokenReq)
	require.NoError(t, err)

	var claimSig htlc.ClaimSignature
	require.NoError(t, json.Unmarshal(result, &claimSig))
	require.Equal(t, preImage, claimSig.Preimage)
	require.Equal(t, expectedSig, claimSig.RecipientSignature)

	// signer must be called with tokenReq+preImage
	require.Equal(t, 1, signer.SignCallCount())
	require.Equal(t, append(tokenReq, preImage...), signer.SignArgsForCall(0))
}

func TestClaimSignerSignError(t *testing.T) {
	signer := &mock.Signer{}
	signer.SignReturns(nil, errors.New("sign failed"))

	cs := &htlc.ClaimSigner{Recipient: signer, Preimage: []byte("p")}
	_, err := cs.Sign([]byte("req"))
	require.EqualError(t, err, "sign failed")
}

// ---- ClaimVerifier ----

func validClaimSigBytes(t *testing.T, preImage, recipientSig []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(htlc.ClaimSignature{Preimage: preImage, RecipientSignature: recipientSig})
	require.NoError(t, err)

	return raw
}

func claimHashInfo(preImage []byte) htlc.HashInfo {
	h := crypto.SHA256.New()
	h.Write(preImage)

	return htlc.HashInfo{
		Hash:         []byte(encoding.Base64.New().EncodeToString(h.Sum(nil))),
		HashFunc:     crypto.SHA256,
		HashEncoding: encoding.Base64,
	}
}

func TestClaimVerifierVerifySuccess(t *testing.T) {
	preImage := []byte("secret")
	tokenReq := []byte("txrequest")
	recipientSig := []byte("sig")

	verifier := &mock.Verifier{}
	verifier.VerifyReturns(nil)

	cv := &htlc.ClaimVerifier{Recipient: verifier, HashInfo: claimHashInfo(preImage)}
	require.NoError(t, cv.Verify(tokenReq, validClaimSigBytes(t, preImage, recipientSig)))

	// verifier called with tokenReq+preImage
	msg, _ := verifier.VerifyArgsForCall(0)
	require.Equal(t, append(tokenReq, preImage...), msg)
}

func TestClaimVerifierVerifyInvalidJSON(t *testing.T) {
	cv := &htlc.ClaimVerifier{Recipient: &mock.Verifier{}, HashInfo: claimHashInfo([]byte("p"))}
	require.Error(t, cv.Verify([]byte("req"), []byte("not-json")))
}

func TestClaimVerifierVerifyBadRecipientSignature(t *testing.T) {
	preImage := []byte("secret")
	verifier := &mock.Verifier{}
	verifier.VerifyReturns(errors.New("bad sig"))

	cv := &htlc.ClaimVerifier{Recipient: verifier, HashInfo: claimHashInfo(preImage)}
	err := cv.Verify([]byte("req"), validClaimSigBytes(t, preImage, []byte("sig")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to verify recipient signature")
}

func TestClaimVerifierVerifyHashMismatch(t *testing.T) {
	preImage := []byte("secret")
	verifier := &mock.Verifier{}
	verifier.VerifyReturns(nil)

	hi := claimHashInfo([]byte("different-preimage")) // hash of something else
	cv := &htlc.ClaimVerifier{Recipient: verifier, HashInfo: hi}
	err := cv.Verify([]byte("req"), validClaimSigBytes(t, preImage, []byte("sig")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "hash mismatch")
}

func TestClaimVerifierVerifyUnavailableHashFunc(t *testing.T) {
	preImage := []byte("secret")
	verifier := &mock.Verifier{}
	verifier.VerifyReturns(nil)

	cv := &htlc.ClaimVerifier{
		Recipient: verifier,
		HashInfo:  htlc.HashInfo{Hash: []byte("h"), HashFunc: crypto.Hash(999), HashEncoding: encoding.Base64},
	}
	err := cv.Verify([]byte("req"), validClaimSigBytes(t, preImage, []byte("sig")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "script hash function not available")
}

// ---- Verifier ----

func TestVerifierVerifyBeforeDeadline(t *testing.T) {
	preImage := []byte("secret")
	tokenReq := []byte("txrequest")

	recipientVerifier := &mock.Verifier{}
	recipientVerifier.VerifyReturns(nil)

	v := &htlc.Verifier{
		Recipient: recipientVerifier,
		Sender:    &mock.Verifier{},
		Deadline:  time.Now().Add(time.Hour),
		HashInfo:  claimHashInfo(preImage),
	}

	sigma := validClaimSigBytes(t, preImage, []byte("sig"))
	require.NoError(t, v.Verify(tokenReq, sigma))
	require.Equal(t, 1, recipientVerifier.VerifyCallCount())
}

func TestVerifierVerifyBeforeDeadlineInvalidClaim(t *testing.T) {
	recipientVerifier := &mock.Verifier{}
	recipientVerifier.VerifyReturns(errors.New("bad claim"))

	v := &htlc.Verifier{
		Recipient: recipientVerifier,
		Deadline:  time.Now().Add(time.Hour),
		HashInfo:  claimHashInfo([]byte("p")),
	}

	sigma := validClaimSigBytes(t, []byte("p"), []byte("sig"))
	err := v.Verify([]byte("req"), sigma)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed verifying htlc claim signature")
}

func TestVerifierVerifyAfterDeadline(t *testing.T) {
	senderVerifier := &mock.Verifier{}
	senderVerifier.VerifyReturns(nil)

	v := &htlc.Verifier{
		Sender:   senderVerifier,
		Deadline: time.Now().Add(-time.Hour),
		HashInfo: claimHashInfo([]byte("p")),
	}

	require.NoError(t, v.Verify([]byte("req"), []byte("sender-sig")))
	require.Equal(t, 1, senderVerifier.VerifyCallCount())
}

func TestVerifierVerifyAfterDeadlineInvalidReclaim(t *testing.T) {
	senderVerifier := &mock.Verifier{}
	senderVerifier.VerifyReturns(errors.New("bad reclaim"))

	v := &htlc.Verifier{
		Sender:   senderVerifier,
		Deadline: time.Now().Add(-time.Hour),
	}

	err := v.Verify([]byte("req"), []byte("sig"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "deadline elapsed, failed verifying htlc reclaim signature")
}
