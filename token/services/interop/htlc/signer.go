/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ClaimSignature is the claim signature of an htlc script
type ClaimSignature struct {
	RecipientSignature []byte
	Preimage           []byte
}

// ClaimSigner is the signer for the claim of an htlc script
type ClaimSigner struct {
	Recipient driver.Signer
	Preimage  []byte
}

// Sign returns a signature of the recipient over the token request and preimage
func (cs *ClaimSigner) Sign(tokenRequestAndTxID []byte) ([]byte, error) {
	msg := concatTokenRequestTxIDPreimage(tokenRequestAndTxID, cs.Preimage)
	sigma, err := cs.Recipient.Sign(msg)
	if err != nil {
		return nil, err
	}

	claimSignature := ClaimSignature{
		Preimage:           cs.Preimage,
		RecipientSignature: sigma,
	}

	return json.Marshal(claimSignature)
}

func concatTokenRequestTxIDPreimage(tokenRequestAndTxID []byte, preImage []byte) []byte {
	var msg []byte
	msg = append(msg, tokenRequestAndTxID...)
	msg = append(msg, preImage...)

	return msg
}

// ClaimVerifier is the verifier of a ClaimSignature
type ClaimVerifier struct {
	Recipient driver.Verifier
	HashInfo  HashInfo
}

// Verify verifies that the passed signature is valid and that the contained preimage matches the hash info
func (cv *ClaimVerifier) Verify(tokenRequestAndTxID, claimSignature []byte) error {
	sig := &ClaimSignature{}
	err := json.Unmarshal(claimSignature, sig)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal claim signature")
	}

	msg := concatTokenRequestTxIDPreimage(tokenRequestAndTxID, sig.Preimage)
	if err := cv.Recipient.Verify(msg, sig.RecipientSignature); err != nil {
		return errors.WithMessagef(err, "failed to verify recipient signature")
	}

	if !cv.HashInfo.HashFunc.Available() {
		return errors.Errorf("script hash function not available [%d]", cv.HashInfo.HashFunc)
	}
	hash := cv.HashInfo.HashFunc.New()
	if _, err = hash.Write(sig.Preimage); err != nil {
		return errors.Wrapf(err, "failed to compute hash image")
	}
	image := hash.Sum(nil)
	image = []byte(cv.HashInfo.HashEncoding.New().EncodeToString(image))

	if !bytes.Equal(cv.HashInfo.Hash, image) {
		return errors.Errorf("hash mismatch: SHA(%x) = %x != %x", sig.Preimage, image, cv.HashInfo.Hash)
	}

	return nil
}

// Verifier checks if an htlc script can be claimed or reclaimed
type Verifier struct {
	Recipient driver.Verifier
	Sender    driver.Verifier
	Deadline  time.Time
	HashInfo  HashInfo
	// ClockFunc returns the current time used to evaluate the deadline.
	// It defaults to time.Now when nil.
	// Callers that have access to the Fabric block timestamp should inject it
	// here to ensure deterministic, clock-skew-safe deadline enforcement.
	ClockFunc func() time.Time
}

func (v *Verifier) now() time.Time {
	if v.ClockFunc != nil {
		return v.ClockFunc()
	}

	return time.Now()
}

// Verify verifies the claim or reclaim signature
func (v *Verifier) Verify(msg []byte, sigma []byte) error {
	// if timeout has not elapsed, only claim is allowed
	if v.now().Before(v.Deadline) {
		cv := &ClaimVerifier{
			Recipient: v.Recipient,
			HashInfo: HashInfo{
				Hash:         v.HashInfo.Hash,
				HashFunc:     v.HashInfo.HashFunc,
				HashEncoding: v.HashInfo.HashEncoding,
			},
		}
		if err := cv.Verify(msg, sigma); err != nil {
			return errors.WithMessagef(err, "failed verifying htlc claim signature")
		}

		return nil
	}
	// if timeout has elapsed, only a reclaim is possible
	if err := v.Sender.Verify(msg, sigma); err != nil {
		return errors.WithMessagef(err, "deadline elapsed, failed verifying htlc reclaim signature")
	}

	return nil
}
