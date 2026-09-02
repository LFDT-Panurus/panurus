/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sigobserve_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signingIdentity is a driver.Signer that also serializes, i.e. a driver.SigningIdentity.
type signingIdentity struct {
	*dmock.Signer
	serialized []byte
}

func (s *signingIdentity) Serialize() ([]byte, error) { return s.serialized, nil }

func TestInstrumentSignerReturnsTheSignerUnwrapped(t *testing.T) {
	signer := &dmock.Signer{}

	assert.Same(t, signer, sigobserve.InstrumentSigner(signer, nil, "hash", sigobserve.RoleOwner))
	assert.Same(t, signer, sigobserve.InstrumentSigner(signer, sigobserve.Nop, "hash", sigobserve.RoleOwner))
	assert.Nil(t, sigobserve.InstrumentSigner(nil, &recorder{}, "hash", sigobserve.RoleOwner))
}

func TestInstrumentSignerReportsSign(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		r := &recorder{}
		signer := &dmock.Signer{}
		signer.SignReturns([]byte("sigma"), nil)

		wrapped := sigobserve.InstrumentSigner(signer, r, "hash", sigobserve.RoleOwner)
		sigma, err := wrapped.Sign([]byte("message"))
		require.NoError(t, err)
		assert.Equal(t, []byte("sigma"), sigma, "the wrapper must be transparent")
		assert.Equal(t, []byte("message"), signer.SignArgsForCall(0))

		e := r.one(t)
		assert.Equal(t, sigobserve.OpSign, e.Op)
		assert.Equal(t, "hash", e.Principal)
		assert.Equal(t, sigobserve.RoleOwner, e.Role)
		assert.Equal(t, sigobserve.OutcomeOK, e.Outcome)
	})

	t.Run("failure", func(t *testing.T) {
		r := &recorder{}
		signer := &dmock.Signer{}
		expected := errors.New("no key")
		signer.SignReturns(nil, expected)

		wrapped := sigobserve.InstrumentSigner(signer, r, "hash", sigobserve.RoleIssuer)
		_, err := wrapped.Sign([]byte("message"))
		require.ErrorIs(t, err, expected)

		e := r.one(t)
		assert.Equal(t, sigobserve.OutcomeError, e.Outcome)
		assert.Equal(t, expected, e.Err)
	})
}

// TestInstrumentSignerPreservesSigningIdentity pins the behaviour callers depend on: wrapping a
// signer must not hide its Serialize method, or every wallet that resolves a signing identity
// through the provider would break.
func TestInstrumentSignerPreservesSigningIdentity(t *testing.T) {
	r := &recorder{}
	signer := &signingIdentity{Signer: &dmock.Signer{}, serialized: []byte("raw-identity")}
	signer.SignReturns([]byte("sigma"), nil)

	wrapped := sigobserve.InstrumentSigner(signer, r, "hash", sigobserve.RoleOwner)
	si, ok := wrapped.(driver.SigningIdentity)
	require.True(t, ok, "a wrapped SigningIdentity must remain a SigningIdentity")

	raw, err := si.Serialize()
	require.NoError(t, err)
	assert.Equal(t, []byte("raw-identity"), raw)

	_, err = si.Sign([]byte("message"))
	require.NoError(t, err)
	assert.Equal(t, sigobserve.OpSign, r.one(t).Op, "Serialize is not instrumented, Sign is")
}

func TestInstrumentVerifierReturnsTheVerifierUnwrapped(t *testing.T) {
	verifier := &dmock.Verifier{}

	assert.Same(t, verifier, sigobserve.InstrumentVerifier(verifier, nil, "hash", sigobserve.RoleOwner))
	assert.Same(t, verifier, sigobserve.InstrumentVerifier(verifier, sigobserve.Nop, "hash", sigobserve.RoleOwner))
	assert.Nil(t, sigobserve.InstrumentVerifier(nil, &recorder{}, "hash", sigobserve.RoleOwner))
}

func TestInstrumentVerifierReportsVerify(t *testing.T) {
	t.Run("accepted signature", func(t *testing.T) {
		r := &recorder{}
		verifier := &dmock.Verifier{}
		verifier.VerifyReturns(nil)

		wrapped := sigobserve.InstrumentVerifier(verifier, r, "hash", sigobserve.RoleAuditor)
		require.NoError(t, wrapped.Verify([]byte("message"), []byte("sigma")))

		message, sigma := verifier.VerifyArgsForCall(0)
		assert.Equal(t, []byte("message"), message)
		assert.Equal(t, []byte("sigma"), sigma)

		e := r.one(t)
		assert.Equal(t, sigobserve.OpVerify, e.Op)
		assert.Equal(t, sigobserve.RoleAuditor, e.Role)
		assert.Equal(t, sigobserve.OutcomeOK, e.Outcome)
	})

	t.Run("rejected signature", func(t *testing.T) {
		r := &recorder{}
		verifier := &dmock.Verifier{}
		expected := errors.New("invalid signature")
		verifier.VerifyReturns(expected)

		wrapped := sigobserve.InstrumentVerifier(verifier, r, "hash", sigobserve.RoleOwner)
		require.ErrorIs(t, wrapped.Verify([]byte("message"), []byte("sigma")), expected)

		e := r.one(t)
		assert.Equal(t, sigobserve.OutcomeInvalid, e.Outcome, "a rejected signature is the signal to watch")
	})
}
