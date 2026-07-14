/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deserializer

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	identitydrivermock "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a typed identity
func createTypedIdentity(t *testing.T, idType identity.Type, rawIdentity []byte) driver.Identity {
	t.Helper()

	ti := identity.TypedIdentity{
		Type:     idType,
		Identity: rawIdentity,
	}
	bytes, err := ti.Bytes()
	require.NoError(t, err)

	return bytes
}

// TestTypedAuditInfoMatcher tests the TypedAuditInfoMatcher
func TestTypedAuditInfoMatcher(t *testing.T) {
	// Tests the TypedAuditInfoMatcher running a valid match
	// i.e. the typed ID is correctly deserialized and the Match
	// succeeds on the deserialized id
	t.Run("Success", func(t *testing.T) {
		matcher := &drivermock.Matcher{}
		matcher.MatchCalls(func(ctx context.Context, id []byte) error {
			assert.Equal(t, []byte("raw-identity"), id)

			return nil
		})

		typedMatcher := &TypedAuditInfoMatcher{matcher: matcher}
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))

		err := typedMatcher.Match(context.Background(), typedID)
		require.NoError(t, err)
		assert.Equal(t, 1, matcher.MatchCallCount())
	})

	// Tests the TypedAuditInfoMatcher running a failing match
	// due to mismatching typed identity
	t.Run("UnmarshalError", func(t *testing.T) {
		matcher := &drivermock.Matcher{}
		typedMatcher := &TypedAuditInfoMatcher{matcher: matcher}

		err := typedMatcher.Match(context.Background(), []byte("invalid-identity"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal identity")
		assert.Equal(t, 0, matcher.MatchCallCount())
	})

	// Tests the TypedAuditInfoMatcher running a failing match
	// due to arbitrary matching issue
	t.Run("MatchError", func(t *testing.T) {
		expectedErr := errors.New("match failed")
		matcher := &drivermock.Matcher{}
		matcher.MatchReturns(expectedErr)

		typedMatcher := &TypedAuditInfoMatcher{matcher: matcher}
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))

		err := typedMatcher.Match(context.Background(), typedID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to match identity")
		require.ErrorIs(t, err, expectedErr)
		assert.Equal(t, 1, matcher.MatchCallCount())
	})
}

// TestEIDRHDeserializer tests the EIDRHDeserializer
// that deserializes audit information including
// the Enrollment ID and the Revocation Handle
func TestEIDRHDeserializer(t *testing.T) {
	// test success in getting the EID from the EIDRHDeserializer
	t.Run("GetEnrollmentID_Success", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()

		mockAuditInfo := &identitydrivermock.AuditInfo{}
		mockAuditInfo.EnrollmentIDReturns("test-eid")
		mockAuditInfo.RevocationHandleReturns("test-rh")

		mockDeserializer := &identitydrivermock.AuditInfoDeserializer{}
		mockDeserializer.DeserializeAuditInfoReturns(mockAuditInfo, nil)

		deserializer.AddDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		eid, err := deserializer.GetEnrollmentID(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.Equal(t, "test-eid", eid)
		assert.Equal(t, 1, mockDeserializer.DeserializeAuditInfoCallCount())
		assert.Equal(t, 1, mockAuditInfo.EnrollmentIDCallCount())
	})

	// test success in getting the RevocationHandler from the EIDRHDeserializer
	t.Run("GetRevocationHandler_Success", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()

		mockAuditInfo := &identitydrivermock.AuditInfo{}
		mockAuditInfo.EnrollmentIDReturns("test-eid")
		mockAuditInfo.RevocationHandleReturns("test-rh")

		mockDeserializer := &identitydrivermock.AuditInfoDeserializer{}
		mockDeserializer.DeserializeAuditInfoReturns(mockAuditInfo, nil)

		deserializer.AddDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		rh, err := deserializer.GetRevocationHandler(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.Equal(t, "test-rh", rh)
		assert.Equal(t, 1, mockDeserializer.DeserializeAuditInfoCallCount())
		assert.Equal(t, 1, mockAuditInfo.RevocationHandleCallCount())
	})

	// test success in getting the both the EID and the RevocationHandler from the
	// EIDRHDeserializer using GetEIDAndRH
	t.Run("GetEIDAndRH_Success", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()

		mockAuditInfo := &identitydrivermock.AuditInfo{}
		mockAuditInfo.EnrollmentIDReturns("test-eid")
		mockAuditInfo.RevocationHandleReturns("test-rh")

		mockDeserializer := &identitydrivermock.AuditInfoDeserializer{}
		mockDeserializer.DeserializeAuditInfoReturns(mockAuditInfo, nil)

		deserializer.AddDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		eid, rh, err := deserializer.GetEIDAndRH(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.Equal(t, "test-eid", eid)
		assert.Equal(t, "test-rh", rh)
		assert.Equal(t, 1, mockDeserializer.DeserializeAuditInfoCallCount())
		assert.Equal(t, 1, mockAuditInfo.EnrollmentIDCallCount())
		assert.Equal(t, 1, mockAuditInfo.RevocationHandleCallCount())
	})

	// test error when trying to deserialize and extract the EID
	// when the audit info is nil
	t.Run("NilAuditInfo", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))

		_, err := deserializer.GetEnrollmentID(context.Background(), typedID, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil audit info")
	})

	// test error when trying to deserialize and extract the EID
	// when the audit info is empty
	t.Run("EmptyAuditInfo", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))

		_, err := deserializer.GetEnrollmentID(context.Background(), typedID, []byte{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil audit info")
	})

	// test error when trying to deserialize and extract the EID
	// when the typed identity is invalid
	t.Run("UnmarshalError", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()

		_, err := deserializer.GetEnrollmentID(context.Background(), []byte("invalid"), []byte("audit-info"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal to TypedIdentity")
	})

	// test error when trying to deserialize and extract the EID
	// when the typed identity is of an unrecognized type
	t.Run("NoDeserializerFound", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		_, err := deserializer.GetEnrollmentID(context.Background(), typedID, []byte("audit-info"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no deserializer found")
	})

	// test error when trying to deserialize and extract the EID
	// when the NewEIDRHDeserializer returns with an arbitrary error
	t.Run("DeserializeAuditInfoError", func(t *testing.T) {
		deserializer := NewEIDRHDeserializer()

		mockDeserializer := &identitydrivermock.AuditInfoDeserializer{}
		mockDeserializer.DeserializeAuditInfoReturns(nil, errors.New("deserialize error"))

		deserializer.AddDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		_, err := deserializer.GetEnrollmentID(context.Background(), typedID, []byte("audit-info"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to deserialize audit info")
		assert.Equal(t, 1, mockDeserializer.DeserializeAuditInfoCallCount())
	})
}

// TestTypedSignerDeserializerMultiplex tests the TypedSignerDeserializerMultiplex
// that deserializes a transaction signer
func TestTypedSignerDeserializerMultiplex(t *testing.T) {
	// test success in getting the signer from the TypedSignerDeserializerMultiplex
	t.Run("DeserializeSigner_Success", func(t *testing.T) {
		multiplex := NewTypedSignerDeserializerMultiplex()

		mockSigner := &drivermock.Signer{}
		mockDeserializer := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer.DeserializeSignerReturns(mockSigner, nil)

		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		signer, err := multiplex.DeserializeSigner(context.Background(), typedID)

		require.NoError(t, err)
		assert.NotNil(t, signer)
		assert.Equal(t, mockSigner, signer)
		assert.Equal(t, 1, mockDeserializer.DeserializeSignerCallCount())
	})

	// test error when trying to deserialize and extract the signer
	// when the typed identity is invalid
	t.Run("DeserializeSigner_UnmarshalError", func(t *testing.T) {
		multiplex := NewTypedSignerDeserializerMultiplex()

		_, err := multiplex.DeserializeSigner(context.Background(), []byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal to TypedIdentity")
	})

	// test error when trying to deserialize and extract the signer
	// when the TypedSignerDeserializerMultiplex wasn't set
	// with any TypedSignerDeserializer
	t.Run("DeserializeSigner_NoDeserializerFound", func(t *testing.T) {
		multiplex := NewTypedSignerDeserializerMultiplex()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		_, err := multiplex.DeserializeSigner(context.Background(), typedID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no deserializer found")
	})

	// Test that when the TypedSignerDeserializerMultiplex is set with two deserializers
	// for the same type, then if the first one succeeds then the 2nd one isn't used
	t.Run("DeserializeSigner_MultipleDeserializers_FirstSucceeds", func(t *testing.T) {
		multiplex := NewTypedSignerDeserializerMultiplex()

		mockSigner := &drivermock.Signer{}
		mockDeserializer1 := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer1.DeserializeSignerReturns(mockSigner, nil)

		mockDeserializer2 := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer2.DeserializeSignerReturns(nil, errors.New("should not be called"))

		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer2)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		signer, err := multiplex.DeserializeSigner(context.Background(), typedID)

		require.NoError(t, err)
		assert.NotNil(t, signer)
		assert.Equal(t, mockSigner, signer)
		assert.Equal(t, 1, mockDeserializer1.DeserializeSignerCallCount())
		assert.Equal(t, 0, mockDeserializer2.DeserializeSignerCallCount())
	})

	// Test that when the TypedSignerDeserializerMultiplex is set with two deserializers
	// for the same type, then if the first one fails then the 2nd one is given a chance
	t.Run("DeserializeSigner_MultipleDeserializers_SecondSucceeds", func(t *testing.T) {
		multiplex := NewTypedSignerDeserializerMultiplex()

		mockDeserializer1 := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer1.DeserializeSignerReturns(nil, errors.New("first fails"))

		mockSigner := &drivermock.Signer{}
		mockDeserializer2 := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer2.DeserializeSignerReturns(mockSigner, nil)

		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer2)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		signer, err := multiplex.DeserializeSigner(context.Background(), typedID)

		require.NoError(t, err)
		assert.NotNil(t, signer)
		assert.Equal(t, mockSigner, signer)
		assert.Equal(t, 1, mockDeserializer1.DeserializeSignerCallCount())
		assert.Equal(t, 1, mockDeserializer2.DeserializeSignerCallCount())
	})

	// Test that when the TypedSignerDeserializerMultiplex is set with two deserializers
	// for the same type, then if both of them fail then the deserialization fails
	t.Run("DeserializeSigner_AllDeserializersFail", func(t *testing.T) {
		multiplex := NewTypedSignerDeserializerMultiplex()

		mockDeserializer1 := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer1.DeserializeSignerReturns(nil, errors.New("first fails"))

		mockDeserializer2 := &identitydrivermock.TypedSignerDeserializer{}
		mockDeserializer2.DeserializeSignerReturns(nil, errors.New("second fails"))

		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedSignerDeserializer(identity.Type(99), mockDeserializer2)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		_, err := multiplex.DeserializeSigner(context.Background(), typedID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to deserialize verifier")
		assert.Equal(t, 1, mockDeserializer1.DeserializeSignerCallCount())
		assert.Equal(t, 1, mockDeserializer2.DeserializeSignerCallCount())
	})
}

// TestTypedVerifierDeserializerMultiplex tests the TypedVerifierDeserializerMultiplex
// that deserializes a signature verifier
func TestTypedVerifierDeserializerMultiplex(t *testing.T) {
	// test success in getting the verifier from the TypedVerifierDeserializerMultiplex
	t.Run("DeserializeVerifier_Success", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockVerifier := &drivermock.Verifier{}
		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.DeserializeVerifierReturns(mockVerifier, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		verifier, err := multiplex.DeserializeVerifier(context.Background(), typedID)

		require.NoError(t, err)
		assert.NotNil(t, verifier)
		assert.Equal(t, mockVerifier, verifier)
		assert.Equal(t, 1, mockDeserializer.DeserializeVerifierCallCount())
	})

	// test error when trying to deserialize and extract the verifier
	// when the typed identity is invalid
	t.Run("DeserializeVerifier_UnmarshalError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		_, err := multiplex.DeserializeVerifier(context.Background(), []byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal to TypedIdentity")
	})

	// test error when trying to deserialize and extract the verifier
	// when the TypedVerifierDeserializerMultiplex wasn't set
	// with any TypedVerifierDeserializer
	t.Run("DeserializeVerifier_NoDeserializerFound", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		_, err := multiplex.DeserializeVerifier(context.Background(), typedID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no deserializer found")
	})

	// Test that when the TypedVerifierDeserializerMultiplex is set with a deserializer
	// that fails then the deserialization fails when using an id of the corresponding type
	t.Run("DeserializeVerifier_AllDeserializersFail", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.DeserializeVerifierReturns(nil, errors.New("deserialize failed"))

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		_, err := multiplex.DeserializeVerifier(context.Background(), typedID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to deserialize verifier")
		assert.Equal(t, 1, mockDeserializer.DeserializeVerifierCallCount())
	})

	// Test success in getting the recipient identities from a typed identity
	t.Run("Recipients_Success", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.RecipientsReturns([]driver.Identity{[]byte("recipient")}, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		recipients, err := multiplex.Recipients(typedID)

		require.NoError(t, err)
		assert.NotNil(t, recipients)
		assert.Len(t, recipients, 1)
		assert.Equal(t, 1, mockDeserializer.RecipientsCallCount())
	})

	// Test that getting the recipient identities from a nil identity
	// returns a nil recipients list
	t.Run("Recipients_NoneIdentity", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		recipients, err := multiplex.Recipients(driver.Identity(nil))
		require.NoError(t, err)
		assert.Nil(t, recipients)
	})

	// Test that getting the recipient identities from an invalid identity
	// returns error
	t.Run("Recipients_UnmarshalError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		_, err := multiplex.Recipients([]byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal to TypedIdentity")
	})

	// test that getting the recipient identities with a typed id of an unrecognized type
	// fails as expected
	t.Run("Recipients_NoDeserializerFound", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		_, err := multiplex.Recipients(typedID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no deserializer found")
	})

	// Test that when the TypedVerifierDeserializerMultiplex is set with a deserializer
	// that fails then the deserialization fails when using an id of the corresponding type
	t.Run("Recipients_AllDeserializersFail", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.RecipientsReturns(nil, errors.New("recipients failed"))

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		_, err := multiplex.Recipients(typedID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to deserializer recipients")
		assert.Equal(t, 1, mockDeserializer.RecipientsCallCount())
	})

	// Test success in getting an AuditInfoMatcher from a TypedVerifierDeserializerMultiplex
	// that was set with a TypedVerifierDeserializer of the type corresponding to the type of the queried id
	t.Run("GetAuditInfoMatcher_Success", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockMatcher := &drivermock.Matcher{}
		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.GetAuditInfoMatcherReturns(mockMatcher, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		matcher, err := multiplex.GetAuditInfoMatcher(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.NotNil(t, matcher)
		// The matcher is wrapped in a TypedAuditInfoMatcher
		typedMatcher, ok := matcher.(*TypedAuditInfoMatcher)
		require.True(t, ok, "expected matcher to be a *TypedAuditInfoMatcher")
		assert.Equal(t, mockMatcher, typedMatcher.matcher)
		assert.Equal(t, 1, mockDeserializer.GetAuditInfoMatcherCallCount())
	})

	// Test that getting the AuditInfoMatcher using a nil identity
	// returns a nil matcher
	t.Run("GetAuditInfoMatcher_NoneIdentity", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		matcher, err := multiplex.GetAuditInfoMatcher(context.Background(), driver.Identity(nil), []byte("audit-info"))
		require.NoError(t, err)
		assert.Nil(t, matcher)
	})

	// Test that getting the AuditInfoMatcher using an invalid identity
	// returns the expected error
	t.Run("GetAuditInfoMatcher_UnmarshalError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		_, err := multiplex.GetAuditInfoMatcher(context.Background(), []byte("invalid"), []byte("audit-info"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal to TypedIdentity")
	})

	// Test that when trying to get an AuditInfoMatcher
	// with an id of an unrecognized type then no deserializer is found
	t.Run("GetAuditInfoMatcher_NoDeserializerFound", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		_, err := multiplex.GetAuditInfoMatcher(context.Background(), typedID, []byte("audit-info"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no deserializer found")
	})

	// Test success when adding a TypedVerifierDeserializer to a TypedVerifierDeserializerMultiplex
	// and then using that TypedVerifierDeserializer to match a valid identity of the same type
	t.Run("MatchIdentity_Success", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockMatcher := &drivermock.Matcher{}
		mockMatcher.MatchReturns(nil)

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.GetAuditInfoMatcherReturns(mockMatcher, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		err := multiplex.MatchIdentity(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.Equal(t, 1, mockDeserializer.GetAuditInfoMatcherCallCount())
		assert.Equal(t, 1, mockMatcher.MatchCallCount())
	})

	// Test failure when trying to use a TypedVerifierDeserializerMultiplex to match an invalid identity
	t.Run("MatchIdentity_UnmarshalError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		err := multiplex.MatchIdentity(context.Background(), []byte("invalid"), []byte("audit-info"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal identity")
	})

	// Test failure when matching with an id of a type for which no deserializer was added
	// to a TypedVerifierDeserializerMultiplex
	t.Run("MatchIdentity_GetMatcherError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		err := multiplex.MatchIdentity(context.Background(), typedID, []byte("audit-info"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting audit info matcher")
	})

	// Test that matching fails when the deserializer corresponding to the matched id's type
	// fails for some arbitrary reason
	t.Run("MatchIdentity_MatchError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		expectedErr := errors.New("match failed")
		mockMatcher := &drivermock.Matcher{}
		mockMatcher.MatchReturns(expectedErr)

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.GetAuditInfoMatcherReturns(mockMatcher, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		err := multiplex.MatchIdentity(context.Background(), typedID, []byte("audit-info"))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to match identity to audit infor")
		require.ErrorIs(t, err, expectedErr)
		assert.Equal(t, 1, mockDeserializer.GetAuditInfoMatcherCallCount())
		assert.Equal(t, 1, mockMatcher.MatchCallCount())
	})

	// Test success getting AuditInfo for an id of a type that corresponds to a deserializer on
	// a TypedVerifierDeserializerMultiplex and using an AuditInfoProvider
	t.Run("GetAuditInfo_Success", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.GetAuditInfoReturns([]byte("audit-info"), nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		mockProvider := &drivermock.AuditInfoProvider{}
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		auditInfo, err := multiplex.GetAuditInfo(context.Background(), typedID, mockProvider)

		require.NoError(t, err)
		assert.NotNil(t, auditInfo)
		assert.Equal(t, 1, mockDeserializer.GetAuditInfoCallCount())
	})

	// Test failure trying to get AuditInfo from a TypedVerifierDeserializerMultiplex for invalid id
	t.Run("GetAuditInfo_UnmarshalError", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockProvider := &drivermock.AuditInfoProvider{}
		_, err := multiplex.GetAuditInfo(context.Background(), []byte("invalid"), mockProvider)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal to TypedIdentity")
	})

	// Test failure trying to get AuditInfo from a TypedVerifierDeserializerMultiplex for an id of a type
	// for which no deserializer was set on the TypedVerifierDeserializerMultiplex
	t.Run("GetAuditInfo_NoDeserializerFound", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()
		typedID := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))

		mockProvider := &drivermock.AuditInfoProvider{}
		_, err := multiplex.GetAuditInfo(context.Background(), typedID, mockProvider)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no deserializer found")
	})

	// Test failure trying to get AuditInfo from a TypedVerifierDeserializerMultiplex where all the deserializers
	// that correspond to the type of the given id fail
	t.Run("GetAuditInfo_AllDeserializersFail", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer.GetAuditInfoReturns(nil, errors.New("get audit info failed"))

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer)

		mockProvider := &drivermock.AuditInfoProvider{}
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		_, err := multiplex.GetAuditInfo(context.Background(), typedID, mockProvider)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find a valid deserializer for audit info")
		assert.Equal(t, 1, mockDeserializer.GetAuditInfoCallCount())
	})

	// Add two TypedVerifierDeserializers to a TypedVerifierDeserializerMultiplex where the two deserializers
	// corrrespond to two different types, and then test success in extracting those deserializers using two typed ids
	// of the corresponding types
	t.Run("AddTypedVerifierDeserializer_MultipleTypes", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockVerifier1 := &drivermock.Verifier{}
		mockDeserializer1 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer1.DeserializeVerifierReturns(mockVerifier1, nil)

		mockVerifier2 := &drivermock.Verifier{}
		mockDeserializer2 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer2.DeserializeVerifierReturns(mockVerifier2, nil)

		// Add first deserializer for type1
		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer1)

		// Add second deserializer for type2
		multiplex.AddTypedVerifierDeserializer(identity.Type(98), mockDeserializer2)

		// Verify both types work
		typedID1 := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		verifier1, err := multiplex.DeserializeVerifier(context.Background(), typedID1)
		require.NoError(t, err)
		assert.NotNil(t, verifier1)
		assert.Equal(t, 1, mockDeserializer1.DeserializeVerifierCallCount())

		typedID2 := createTypedIdentity(t, identity.Type(98), []byte("raw-identity"))
		verifier2, err := multiplex.DeserializeVerifier(context.Background(), typedID2)
		require.NoError(t, err)
		assert.NotNil(t, verifier2)
		assert.Equal(t, 1, mockDeserializer2.DeserializeVerifierCallCount())
	})

	// Test success when trying to get an AuditInfoMatcher from a TypedVerifierDeserializerMultiplex
	// with two TypedVerifierDeserializers of the same type corresponding to the queried id,
	// where the first deserializer match fails but the second's succeeds.
	t.Run("GetMatcher_MultipleDeserializers_SecondSucceeds", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer1 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer1.GetAuditInfoMatcherReturns(nil, errors.New("first matcher fails"))

		mockMatcher := &drivermock.Matcher{}
		mockDeserializer2 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer2.GetAuditInfoMatcherReturns(mockMatcher, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer2)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		matcher, err := multiplex.GetAuditInfoMatcher(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.NotNil(t, matcher)
		// The matcher is wrapped in a TypedAuditInfoMatcher
		typedMatcher, ok := matcher.(*TypedAuditInfoMatcher)
		require.True(t, ok, "expected matcher to be a *TypedAuditInfoMatcher")
		assert.Equal(t, mockMatcher, typedMatcher.matcher)
		assert.Equal(t, 1, mockDeserializer1.GetAuditInfoMatcherCallCount())
		assert.Equal(t, 1, mockDeserializer2.GetAuditInfoMatcherCallCount())
	})

	// Test success when trying to get a DeserializeVerifier from a TypedVerifierDeserializerMultiplex
	// with two TypedVerifierDeserializers of the same type corresponding to the queried id,
	// where the first deserializer's verifier extractor fails but the second's succeeds.
	t.Run("DeserializeVerifier_MultipleDeserializers_SecondSucceeds", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer1 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer1.DeserializeVerifierReturns(nil, errors.New("first fails"))

		mockVerifier := &drivermock.Verifier{}
		mockDeserializer2 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer2.DeserializeVerifierReturns(mockVerifier, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer2)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		verifier, err := multiplex.DeserializeVerifier(context.Background(), typedID)

		require.NoError(t, err)
		assert.NotNil(t, verifier)
		assert.Equal(t, mockVerifier, verifier)
		assert.Equal(t, 1, mockDeserializer1.DeserializeVerifierCallCount())
		assert.Equal(t, 1, mockDeserializer2.DeserializeVerifierCallCount())
	})

	// Test success when trying to use a TypedVerifierDeserializerMultiplex to get the recipients corresponding to
	// a typed id, where the TypedVerifierDeserializerMultiplex was set with
	// two TypedVerifierDeserializers of the same type corresponding to the queried id,
	// where the first deserializer recipient-getting function fails but the second's succeeds.
	t.Run("Recipients_MultipleDeserializers_SecondSucceeds", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer1 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer1.RecipientsReturns(nil, errors.New("first fails"))

		mockDeserializer2 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer2.RecipientsReturns([]driver.Identity{[]byte("recipient")}, nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer2)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		recipients, err := multiplex.Recipients(typedID)

		require.NoError(t, err)
		assert.NotNil(t, recipients)
		assert.Len(t, recipients, 1)
		assert.Equal(t, 1, mockDeserializer1.RecipientsCallCount())
		assert.Equal(t, 1, mockDeserializer2.RecipientsCallCount())
	})

	// Test success when trying to use a TypedVerifierDeserializerMultiplex to get the AuditInfo corresponding to
	// a typed id, where the TypedVerifierDeserializerMultiplex was set with
	// two TypedVerifierDeserializers of the same type corresponding to the queried id,
	// where the first deserializer AuditInfo-getting function fails but the second's succeeds.
	t.Run("GetAuditInfo_MultipleDeserializers_SecondSucceeds", func(t *testing.T) {
		multiplex := NewTypedVerifierDeserializerMultiplex()

		mockDeserializer1 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer1.GetAuditInfoReturns(nil, errors.New("first fails"))

		mockDeserializer2 := &identitydrivermock.TypedVerifierDeserializer{}
		mockDeserializer2.GetAuditInfoReturns([]byte("audit-info"), nil)

		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer1)
		multiplex.AddTypedVerifierDeserializer(identity.Type(99), mockDeserializer2)

		mockProvider := &drivermock.AuditInfoProvider{}
		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		auditInfo, err := multiplex.GetAuditInfo(context.Background(), typedID, mockProvider)

		require.NoError(t, err)
		assert.NotNil(t, auditInfo)
		assert.Equal(t, 1, mockDeserializer1.GetAuditInfoCallCount())
		assert.Equal(t, 1, mockDeserializer2.GetAuditInfoCallCount())
	})
}

// Tests the TypedIdentityVerifierDeserializer under various success and failure paths
func TestTypedIdentityVerifierDeserializer(t *testing.T) {
	// Test success in extracting a verifier from a raw id using a TypedIdentityVerifierDeserializer
	t.Run("DeserializeVerifier_Success", func(t *testing.T) {
		mockVerifier := &drivermock.Verifier{}
		mockVerifierDeserializer := &drivermock.VerifierDeserializer{}
		mockVerifierDeserializer.DeserializeVerifierReturns(mockVerifier, nil)

		mockMatcherDeserializer := &drivermock.MatcherDeserializer{}

		deserializer := NewTypedIdentityVerifierDeserializer(mockVerifierDeserializer, mockMatcherDeserializer)

		verifier, err := deserializer.DeserializeVerifier(context.Background(), identity.Type(99), []byte("raw-identity"))
		require.NoError(t, err)
		assert.NotNil(t, verifier)
		assert.Equal(t, mockVerifier, verifier)
		assert.Equal(t, 1, mockVerifierDeserializer.DeserializeVerifierCallCount())
	})

	// Test success in extracting a Recipients from a raw id using a TypedIdentityVerifierDeserializer
	t.Run("Recipients_Success", func(t *testing.T) {
		mockVerifierDeserializer := &drivermock.VerifierDeserializer{}
		mockMatcherDeserializer := &drivermock.MatcherDeserializer{}

		deserializer := NewTypedIdentityVerifierDeserializer(mockVerifierDeserializer, mockMatcherDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		recipients, err := deserializer.Recipients(typedID, identity.Type(99), []byte("raw-identity"))

		require.NoError(t, err)
		assert.Len(t, recipients, 1)
		assert.Equal(t, typedID, recipients[0])
	})

	// Test success in extracting AuditInfo for a typed raw id using a TypedIdentityVerifierDeserializer
	// and an AuditInfoProvider
	t.Run("GetAuditInfo_Success", func(t *testing.T) {
		mockVerifierDeserializer := &drivermock.VerifierDeserializer{}
		mockMatcherDeserializer := &drivermock.MatcherDeserializer{}

		deserializer := NewTypedIdentityVerifierDeserializer(mockVerifierDeserializer, mockMatcherDeserializer)

		provider := &drivermock.AuditInfoProvider{}
		provider.GetAuditInfoReturns([]byte("audit-info"), nil)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		auditInfo, err := deserializer.GetAuditInfo(context.Background(), typedID, identity.Type(99), []byte("raw-identity"), provider)

		require.NoError(t, err)
		assert.Equal(t, []byte("audit-info"), auditInfo)
		assert.Equal(t, 1, provider.GetAuditInfoCallCount())
	})

	// Test failure in extracting AuditInfo for a typed raw id using a TypedIdentityVerifierDeserializer
	// and an AuditInfoProvider that fails for some arbitrary reason
	t.Run("GetAuditInfo_ProviderError", func(t *testing.T) {
		mockVerifierDeserializer := &drivermock.VerifierDeserializer{}
		mockMatcherDeserializer := &drivermock.MatcherDeserializer{}

		deserializer := NewTypedIdentityVerifierDeserializer(mockVerifierDeserializer, mockMatcherDeserializer)

		expectedErr := errors.New("provider error")
		provider := &drivermock.AuditInfoProvider{}
		provider.GetAuditInfoReturns(nil, expectedErr)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		_, err := deserializer.GetAuditInfo(context.Background(), typedID, identity.Type(99), []byte("raw-identity"), provider)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting audit info for recipient identity")
		require.ErrorIs(t, err, expectedErr)
		assert.Equal(t, 1, provider.GetAuditInfoCallCount())
	})

	// Test success in extracting an AuditInfoMatcher for a typed raw id using a TypedIdentityVerifierDeserializer
	t.Run("GetAuditInfoMatcher_Success", func(t *testing.T) {
		mockVerifierDeserializer := &drivermock.VerifierDeserializer{}

		mockMatcher := &drivermock.Matcher{}
		mockMatcherDeserializer := &drivermock.MatcherDeserializer{}
		mockMatcherDeserializer.GetAuditInfoMatcherReturns(mockMatcher, nil)

		deserializer := NewTypedIdentityVerifierDeserializer(mockVerifierDeserializer, mockMatcherDeserializer)

		typedID := createTypedIdentity(t, identity.Type(99), []byte("raw-identity"))
		matcher, err := deserializer.GetAuditInfoMatcher(context.Background(), typedID, []byte("audit-info"))

		require.NoError(t, err)
		assert.NotNil(t, matcher)
		assert.Equal(t, mockMatcher, matcher)
		assert.Equal(t, 1, mockMatcherDeserializer.GetAuditInfoMatcherCallCount())
	})
}
