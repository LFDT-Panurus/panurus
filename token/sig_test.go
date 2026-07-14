/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignatureService_AuditorVerifier verifies that AuditorVerifier returns the correct verifier
func TestSignatureService_AuditorVerifier(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	expectedVerifier := &mock.Verifier{}
	deserializer.GetAuditorVerifierReturns(expectedVerifier, nil)

	id := []byte("auditor_identity")
	verifier, err := service.AuditorVerifier(t.Context(), id)

	require.NoError(t, err)
	assert.Equal(t, expectedVerifier, verifier)
}

// TestSignatureService_IssuerVerifier verifies that IssuerVerifier returns the correct verifier
func TestSignatureService_IssuerVerifier(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	expectedVerifier := &mock.Verifier{}
	deserializer.GetIssuerVerifierReturns(expectedVerifier, nil)

	id := []byte("issuer_identity")
	verifier, err := service.IssuerVerifier(t.Context(), id)

	require.NoError(t, err)
	assert.Equal(t, expectedVerifier, verifier)
}

// TestSignatureService_OwnerVerifier verifies that OwnerVerifier returns the correct verifier
func TestSignatureService_OwnerVerifier(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	expectedVerifier := &mock.Verifier{}
	deserializer.GetOwnerVerifierReturns(expectedVerifier, nil)

	id := []byte("owner_identity")
	verifier, err := service.OwnerVerifier(t.Context(), id)

	require.NoError(t, err)
	assert.Equal(t, expectedVerifier, verifier)
}

// TestSignatureService_GetSigner verifies that GetSigner retrieves the correct signer for an identity
func TestSignatureService_GetSigner(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	expectedSigner := &mock.Signer{}
	ip.GetSignerReturns(expectedSigner, nil)

	id := []byte("identity")
	signer, err := service.GetSigner(t.Context(), id)

	require.NoError(t, err)
	assert.Equal(t, expectedSigner, signer)
}

// TestSignatureService_RegisterSigner verifies that RegisterSigner correctly registers a new signer
func TestSignatureService_RegisterSigner(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	id := []byte("identity")
	signer := &mock.Signer{}
	verifier := &mock.Verifier{}

	ip.RegisterSignerReturns(nil)

	err := service.RegisterSigner(t.Context(), id, signer, verifier)

	require.NoError(t, err)
}

// TestSignatureService_IsMe verifies that IsMe correctly identifies if an identity belongs to the service
func TestSignatureService_IsMe(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	ip.IsMeReturns(true)

	id := []byte("identity")
	isMe := service.IsMe(t.Context(), id)

	assert.True(t, isMe)
}

// TestNewSignatureService verifies SignatureService constructor initializes fields correctly
func TestNewSignatureService(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := NewSignatureService(deserializer, ip)

	assert.NotNil(t, service)
	assert.Equal(t, deserializer, service.deserializer)
	assert.Equal(t, ip, service.identityProvider)
}

// TestSignatureService_RegisterEphemeralSigner verifies ephemeral signer registration
func TestSignatureService_RegisterEphemeralSigner(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	id := []byte("identity")
	signer := &mock.Signer{}
	verifier := &mock.Verifier{}

	ip.RegisterSignerReturns(nil)

	err := service.RegisterEphemeralSigner(t.Context(), id, signer, verifier)

	require.NoError(t, err)
	assert.Equal(t, 1, ip.RegisterSignerCallCount())
}

// TestSignatureService_AreMe verifies identity hash matching
func TestSignatureService_AreMe(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	id1 := []byte("identity1")
	id2 := []byte("identity2")
	expectedHashes := []string{"hash1", "hash2"}

	ip.AreMeReturns(expectedHashes)

	hashes := service.AreMe(t.Context(), id1, id2)

	assert.Equal(t, expectedHashes, hashes)
	assert.Equal(t, 1, ip.AreMeCallCount())
}

// TestSignatureService_GetAuditInfo verifies audit info retrieval for multiple identities
func TestSignatureService_GetAuditInfo(t *testing.T) {
	deserializer := &mock.Deserializer{}
	ip := &mock.IdentityProvider{}

	service := &SignatureService{
		deserializer:     deserializer,
		identityProvider: ip,
	}

	t.Run("success", func(t *testing.T) {
		id1 := []byte("identity1")
		id2 := []byte("identity2")
		auditInfo1 := []byte("audit_info_1")
		auditInfo2 := []byte("audit_info_2")

		ip.GetAuditInfoReturnsOnCall(0, auditInfo1, nil)
		ip.GetAuditInfoReturnsOnCall(1, auditInfo2, nil)

		result, err := service.GetAuditInfo(t.Context(), id1, id2)

		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, auditInfo1, result[0])
		assert.Equal(t, auditInfo2, result[1])
	})

	t.Run("error", func(t *testing.T) {
		id := []byte("identity")
		ip.GetAuditInfoReturns(nil, assert.AnError)

		result, err := service.GetAuditInfo(t.Context(), id)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to get audit info")
	})
}
