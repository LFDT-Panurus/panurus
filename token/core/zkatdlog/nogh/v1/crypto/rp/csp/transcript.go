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
	// scratch is a reused input buffer for Absorb's fsState||hashBytes
	// concatenation. Without it, every Absorb call would append into a
	// freshly grown slice (fsState's capacity never exceeds its length),
	// forcing a heap allocation per call across the many Absorb calls per
	// CSP/range-proof round.
	scratch []byte
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
	need := len(tr.fsState) + len(hashBytes)
	if cap(tr.scratch) < need {
		tr.scratch = make([]byte, need)
	}
	buf := tr.scratch[:need]
	copy(buf, tr.fsState)
	copy(buf[len(tr.fsState):], hashBytes)

	newHash := sha256.Sum256(buf)
	// fsState is replaced with a fresh array (not a view into scratch) so a
	// slice previously returned by State() is never mutated by later Absorb calls.
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
