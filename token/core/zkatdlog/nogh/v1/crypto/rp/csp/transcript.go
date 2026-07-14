/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"crypto/sha256"

	mathlib "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
)

type Transcript struct {
	fsState []byte
	Curve   *mathlib.Curve
}

// InitHasher initializes the Fiat-Shamir transcript with proper domain separation.
// The domain separator should be a protocol-specific label to prevent cross-protocol attacks.
func (tr *Transcript) InitHasher() {
	tr.InitHasherWithDomain("CSP-RangeProof-v1")
}

// InitHasherWithDomain initializes the transcript with a custom domain separator.
// This provides domain separation between different proof types and protocol versions.
func (tr *Transcript) InitHasherWithDomain(domainSeparator string) {
	key := sha256.Sum256([]byte(domainSeparator))
	tr.fsState = key[:]
}

// Absorb the message from transcript
func (tr *Transcript) Absorb(hashBytes []byte) {
	bytesToHash := append(tr.fsState, hashBytes...)
	newHash := sha256.Sum256(bytesToHash)
	tr.fsState = newHash[:]
}

func (tr *Transcript) State() []byte {
	return tr.fsState
}

func (tr *Transcript) SetState(state []byte) {
	tr.fsState = state
}

// Squeeze out a challenge
func (tr *Transcript) Squeeze() (*mathlib.Zr, error) {
	raw, err := asn1.MarshalStd(tr.fsState)
	if err != nil {
		return nil, err
	}
	x := tr.Curve.HashToZr(raw)
	tr.Absorb(x.Bytes())

	return x, nil
}
