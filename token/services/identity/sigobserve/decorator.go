/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigobserve

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
)

// InstrumentSigner wraps signer so that every Sign call is reported to o as an OpSign event.
// The wrapper is transparent: it returns exactly what the wrapped signer returns.
//
// When o drops events (nil or Nop) the signer is returned unwrapped, so instrumentation that
// is switched off costs nothing on the signing path. The wrapper preserves the richer identity
// interfaces the signer satisfies: a driver.SigningIdentity keeps its Serialize through the
// wrapper, and a driver.FullIdentity keeps its Verify too (reported as an OpVerify event, as
// InstrumentVerifier does). Without this a FullIdentity - which the real x509 signers are -
// would be narrowed to a bare Signer, and a downstream type assertion to driver.Verifier or
// driver.FullIdentity would silently yield ok == false instead of failing to compile.
//
// Events are reported with context.Background(): Sign has no context of its own, and
// storing a resolution-time context in the wrapper would pin a request-scoped span for the
// full lifetime of the signer (which is process lifetime in the common case). The identity
// and role fields on the event are enough for attribution and correlation.
func InstrumentSigner(signer driver.Signer, o Observer, principal string, role Role) driver.Signer {
	if signer == nil || o == nil || o == Nop {
		return signer
	}

	base := instrumentedSigner{signer: signer, observer: o, principal: principal, role: role}
	si, isSigningIdentity := signer.(driver.SigningIdentity)
	if !isSigningIdentity {
		return &base
	}

	sid := instrumentedSigningIdentity{instrumentedSigner: base, identity: si}
	if ver, ok := signer.(driver.Verifier); ok {
		// The signer is a driver.FullIdentity (SigningIdentity + Verifier); keep it one.
		return &instrumentedFullIdentity{instrumentedSigningIdentity: sid, verifier: ver}
	}

	return &sid
}

// InstrumentVerifier wraps verifier so that every Verify call is reported to o as an
// OpVerify event, with a failed verification reported as OutcomeInvalid. The wrapper is
// transparent, and a verifier is returned unwrapped when o drops events.
//
// Events are reported with context.Background() for the same reason as InstrumentSigner.
func InstrumentVerifier(verifier driver.Verifier, o Observer, principal string, role Role) driver.Verifier {
	if verifier == nil || o == nil || o == Nop {
		return verifier
	}

	return &instrumentedVerifier{verifier: verifier, observer: o, principal: principal, role: role}
}

// instrumentedSigner reports the timing and outcome of each Sign call.
type instrumentedSigner struct {
	signer    driver.Signer
	observer  Observer
	principal string
	role      Role
}

// Sign signs message with the wrapped signer, reporting the call as an OpSign event.
func (s *instrumentedSigner) Sign(message []byte) ([]byte, error) {
	t := Start(s.observer, OpSign, s.principal, s.role)
	sigma, err := s.signer.Sign(message)
	t.Done(context.Background(), err)

	return sigma, err
}

// instrumentedSigningIdentity is an instrumentedSigner that also forwards Serialize, for
// wrapped signers that are driver.SigningIdentity.
type instrumentedSigningIdentity struct {
	instrumentedSigner
	identity driver.SigningIdentity
}

// Serialize returns the byte representation of the wrapped signing identity. It is not
// instrumented: it moves no secret and performs no cryptography.
func (s *instrumentedSigningIdentity) Serialize() ([]byte, error) {
	return s.identity.Serialize()
}

// instrumentedFullIdentity is an instrumentedSigningIdentity that also forwards Verify, for
// wrapped signers that are driver.FullIdentity. Without it a FullIdentity would lose its
// Verifier identity through the wrapper.
type instrumentedFullIdentity struct {
	instrumentedSigningIdentity
	verifier driver.Verifier
}

// Verify checks sigma over message with the wrapped identity, reporting the call as an
// OpVerify event whose outcome distinguishes a rejected signature from a successful one -
// exactly as InstrumentVerifier does for a standalone verifier.
func (s *instrumentedFullIdentity) Verify(message, sigma []byte) error {
	t := Start(s.observer, OpVerify, s.principal, s.role)
	err := s.verifier.Verify(message, sigma)
	t.DoneVerify(context.Background(), err)

	return err
}

// instrumentedVerifier reports the timing and outcome of each Verify call.
type instrumentedVerifier struct {
	verifier  driver.Verifier
	observer  Observer
	principal string
	role      Role
}

// Verify checks sigma over message with the wrapped verifier, reporting the call as an
// OpVerify event whose outcome distinguishes a rejected signature from a successful one.
func (v *instrumentedVerifier) Verify(message, sigma []byte) error {
	t := Start(v.observer, OpVerify, v.principal, v.role)
	err := v.verifier.Verify(message, sigma)
	t.DoneVerify(context.Background(), err)

	return err
}
