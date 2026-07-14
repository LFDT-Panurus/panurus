/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test extracting various types of data from a deserialized multi-signature

// Serialize a multi-id and deserialize a verifier from it
func TestDeserializeVerifier_Success(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	mockVerifier := &mock.Verifier{}
	verifierDES.DeserializeVerifierReturns(mockVerifier, nil)

	deserializer := NewTypedIdentityDeserializer(verifierDES, nil)
	id := &MultiIdentity{Identities: []token.Identity{[]byte("valid_multisig_identity")}}
	idRaw, err := id.Bytes()
	require.NoError(t, err)

	verifier, err := deserializer.DeserializeVerifier(context.Background(), Multisig, idRaw)
	require.NoError(t, err)
	assert.NotNil(t, verifier)

	// Verify the returned verifier is a Verifier wrapping the mock
	multiVerifier, ok := verifier.(*Verifier)
	assert.True(t, ok)
	assert.Len(t, multiVerifier.Verifiers, 1)
	assert.Equal(t, mockVerifier, multiVerifier.Verifiers[0])

	// Verify expected number of calls
	assert.Equal(t, 1, verifierDES.DeserializeVerifierCallCount())
}

// DeserializeVerifier must reject a zero-identity MultiIdentity at the trust boundary.
// This is the attacker path: directly asn1.Marshal a MultiIdentity{Identities: []}
// and wrap it in a TypedIdentity{Type: Multisig}, bypassing the WrapIdentities guard.
func TestDeserializeVerifier_EmptyIdentities(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, nil)

	// Attacker serializes MultiIdentity{Identities: []} directly
	emptyID := &MultiIdentity{Identities: []token.Identity{}}
	emptyRaw, err := emptyID.Bytes()
	require.NoError(t, err)

	verifier, err := deserializer.DeserializeVerifier(context.Background(), Multisig, emptyRaw)
	require.Error(t, err)
	assert.Nil(t, verifier)
	assert.Contains(t, err.Error(), "multisig identity has no members")

	// No sub-verifier deserialization should have been attempted
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
}

// Create an invalid raw multi-id and fail to deserialize a verifier from it
func TestDeserializeVerifier_InvalidMultisigIdentity(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, nil)
	id := []byte("invalid")

	verifier, err := deserializer.DeserializeVerifier(context.Background(), Multisig, id)
	require.Error(t, err)
	assert.Nil(t, verifier)

	// Verify no calls were made since deserialization failed early
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
}

// Serialize an invalid multi-id and fail to deserialize a verifier from it
func TestDeserializeVerifier_InvalidIdentity(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	verifierDES.DeserializeVerifierReturns(nil, errors.New("invalid identity"))

	deserializer := NewTypedIdentityDeserializer(verifierDES, nil)
	id := &MultiIdentity{Identities: []token.Identity{[]byte("invalid")}}
	idRaw, err := id.Bytes()
	require.NoError(t, err)

	verifier, err := deserializer.DeserializeVerifier(context.Background(), Multisig, idRaw)
	require.Error(t, err)
	assert.Nil(t, verifier)

	// Verify one call was made (it failed during deserialization)
	assert.Equal(t, 1, verifierDES.DeserializeVerifierCallCount())
}

// Test getting AuditInfo of multisig type from a raw id using (mock) deserialier and AuditInfoProviders
func TestGetOwnerAuditInfo_Success(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	matcher := &mock.Matcher{}
	matcher.MatchReturns(nil)
	auditInfoMatcher.GetAuditInfoMatcherReturns(matcher, nil)

	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)
	id := []byte("valid_multisig_identity")
	rawIdentity := []byte("valid_raw_identity")

	provider := &mock.AuditInfoProvider{}
	expectedAuditInfo := []byte("valid_audit_info")
	provider.GetAuditInfoReturns(expectedAuditInfo, nil)

	auditInfo, err := deserializer.GetAuditInfo(context.Background(), id, Multisig, rawIdentity, provider)
	require.NoError(t, err)
	assert.NotNil(t, auditInfo)
	assert.Equal(t, expectedAuditInfo, auditInfo)

	// Verify no calls to verifier (not used in this path)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify provider was called
	assert.Equal(t, 1, provider.GetAuditInfoCallCount())
}

// Test failure to get AuditInfo from a valid raw id using an invalid id type
func TestGetAuditInfo_InvalidType(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)
	id := []byte("valid_multisig_identity")
	rawIdentity := []byte("valid_raw_identity")

	provider := &mock.AuditInfoProvider{}

	auditInfo, err := deserializer.GetAuditInfo(context.Background(), id, identity.Type(99), rawIdentity, provider)
	require.Error(t, err)
	assert.Nil(t, auditInfo)

	// Verify no calls to verifier (failed early on type check)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify no calls to provider (failed before reaching it)
	assert.Equal(t, 0, provider.GetAuditInfoCallCount())
}

// Test failure to deserialize a valid owner id into a AuditInfoMatcher using an invalid AuditInfo
func TestGetOwnerMatcher_InvalidAuditInfo(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)
	owner := []byte("valid_owner")
	auditInfo := []byte("invalid")

	matcher, err := deserializer.GetAuditInfoMatcher(context.Background(), owner, auditInfo)
	require.Error(t, err)
	assert.Nil(t, matcher)

	// Verify no calls to verifier (failed during audit info unmarshal)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify no calls to matcher (failed before reaching it)
	assert.Equal(t, 0, auditInfoMatcher.GetAuditInfoMatcherCallCount())
}

// Test deserializing a valid raw multi-id into a list of recipients
func TestRecipients_Success(t *testing.T) {
	deserializer := NewTypedIdentityDeserializer(nil, nil)
	id := &MultiIdentity{Identities: []token.Identity{[]byte("valid_multisig_identity")}}
	idRaw, err := id.Bytes()
	require.NoError(t, err)

	recipients, err := deserializer.Recipients(nil, Multisig, idRaw)
	require.NoError(t, err)
	assert.NotNil(t, recipients)
	assert.Equal(t, id.Identities, recipients)
}

// Test failure to deseriale an invalid raw multi-id into a list of recipients
func TestRecipients_InvalidRaw(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, nil)
	id := []byte("valid_multisig_identity")
	raw := []byte("invalid")

	recipients, err := deserializer.Recipients(id, Multisig, raw)
	require.Error(t, err)
	assert.Nil(t, recipients)

	// Verify no calls to verifier (not used in Recipients)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
}

// Test deserializing a valid raw id into an already exisiting AuditInfo for the specified id
func TestGetAuditInfo_AlreadyExists(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	id := []byte("test_id")
	rawIdentity := []byte("raw_identity")
	existingAuditInfo := []byte("existing_audit_info")

	provider := &mock.AuditInfoProvider{}
	// Configure to return existing audit info for test_id
	provider.GetAuditInfoCalls(func(ctx context.Context, identity token.Identity) ([]byte, error) {
		if string(identity) == "test_id" {
			return existingAuditInfo, nil
		}

		return []byte("valid_audit_info"), nil
	})

	auditInfo, err := deserializer.GetAuditInfo(context.Background(), id, Multisig, rawIdentity, provider)
	require.NoError(t, err)
	assert.Equal(t, existingAuditInfo, auditInfo)

	// Verify no calls to verifier (audit info already exists)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify provider was called once
	assert.Equal(t, 1, provider.GetAuditInfoCallCount())
}

// Test deserializing a valid raw id into an a known raw AuditInfo (set for the mock)
// and then deserializing the raw ai and testing that it is as expected
func TestGetAuditInfo_BuildNew(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	identities := []token.Identity{[]byte("valid_multisig_identity")}
	mi := &MultiIdentity{Identities: identities}
	rawIdentity, err := mi.Serialize()
	require.NoError(t, err)

	id := []byte("test_id")

	provider := &mock.AuditInfoProvider{}
	// Configure to return nil for test_id (forcing build new) and valid for others
	provider.GetAuditInfoCalls(func(ctx context.Context, identity token.Identity) ([]byte, error) {
		if string(identity) == "test_id" {
			return nil, nil
		}

		return []byte("valid_audit_info"), nil
	})

	auditInfo, err := deserializer.GetAuditInfo(context.Background(), id, Multisig, rawIdentity, provider)
	require.NoError(t, err)
	assert.NotNil(t, auditInfo)

	// Verify the audit info structure
	ai := &AuditInfo{}
	err = json.Unmarshal(auditInfo, ai)
	require.NoError(t, err)
	assert.Len(t, ai.IdentityAuditInfos, 1)
	assert.Equal(t, []byte("valid_audit_info"), ai.IdentityAuditInfos[0].AuditInfo)

	// Verify no calls to verifier (not used in GetAuditInfo)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify provider was called (once for test_id, once for the identity inside multisig)
	assert.Equal(t, 2, provider.GetAuditInfoCallCount())
}

// Test failure to deserialize an invalid raw id into AuditInfo
func TestGetAuditInfo_InvalidRawIdentity(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	id := []byte("test_id")
	rawIdentity := []byte("invalid")

	provider := &mock.AuditInfoProvider{}

	auditInfo, err := deserializer.GetAuditInfo(context.Background(), id, Multisig, rawIdentity, provider)
	require.Error(t, err)
	assert.Nil(t, auditInfo)

	// Verify no calls to verifier (failed during deserialization)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify provider was called once (for the id check)
	assert.Equal(t, 1, provider.GetAuditInfoCallCount())
}

// Test failure to deserialize a valid raw id into AuditInfo
// when the (mock) AuditInfoProvider fails for some arbitrary reason
func TestGetAuditInfo_ProviderError(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	id := []byte("test_id")
	rawIdentity := []byte("raw")

	provider := &mock.AuditInfoProvider{}
	provider.GetAuditInfoReturns(nil, errors.New("provider error"))

	auditInfo, err := deserializer.GetAuditInfo(context.Background(), id, Multisig, rawIdentity, provider)
	require.Error(t, err)
	assert.Nil(t, auditInfo)

	// Verify no calls to verifier (provider error occurred first)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify provider was called once
	assert.Equal(t, 1, provider.GetAuditInfoCallCount())
}

// Deserialize a valid serialized multi-identity of type multi-sig
// into an AuditInfoMatcher that matches with a specified AuditInfo
// and test that the matcher has the expected features
func TestGetAuditInfoMatcher_Success(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	matcher := &mock.Matcher{}
	matcher.MatchReturns(nil)
	auditInfoMatcher.GetAuditInfoMatcherReturns(matcher, nil)

	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	identities := []token.Identity{[]byte("valid_multisig_identity")}
	mi := &MultiIdentity{Identities: identities}
	rawIdentity, err := mi.Serialize()
	require.NoError(t, err)

	typedID, err := identity.WrapWithType(Multisig, rawIdentity)
	require.NoError(t, err)

	ai := &AuditInfo{
		IdentityAuditInfos: []IdentityAuditInfo{
			{AuditInfo: []byte("valid_audit_info")},
		},
	}
	auditInfoBytes, err := ai.Bytes()
	require.NoError(t, err)

	result, err := deserializer.GetAuditInfoMatcher(context.Background(), typedID, auditInfoBytes)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify the returned matcher is an InfoMatcher wrapping the mock
	infoMatcher, ok := result.(*InfoMatcher)
	assert.True(t, ok)
	assert.Len(t, infoMatcher.AuditInfoMatcher, 1)
	assert.Equal(t, matcher, infoMatcher.AuditInfoMatcher[0])

	// Verify no calls to verifier (not used in GetAuditInfoMatcher)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify matcher was called once
	assert.Equal(t, 1, auditInfoMatcher.GetAuditInfoMatcherCallCount())
}

// Test failure to extract an AuditInfoMatcher given an invalid raw owner id
func TestGetAuditInfoMatcher_InvalidOwner(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	owner := []byte("invalid")
	auditInfo := []byte("valid_audit_info")

	matcher, err := deserializer.GetAuditInfoMatcher(context.Background(), owner, auditInfo)
	require.Error(t, err)
	assert.Nil(t, matcher)

	// Verify no calls to verifier (failed during owner unmarshal)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify no calls to matcher (failed before reaching it)
	assert.Equal(t, 0, auditInfoMatcher.GetAuditInfoMatcherCallCount())
}

// Serialize a multi-id of a multi-sig type with two specified ids and then
// fail because instead of getting two matchers we set GetAuditInfoMatcher to match with just one
func TestGetAuditInfoMatcher_CountMismatch(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	identities := []token.Identity{[]byte("id1"), []byte("id2")}
	mi := &MultiIdentity{Identities: identities}
	rawIdentity, err := mi.Serialize()
	require.NoError(t, err)

	typedID, err := identity.WrapWithType(Multisig, rawIdentity)
	require.NoError(t, err)

	// Only one audit info for two identities
	ai := &AuditInfo{
		IdentityAuditInfos: []IdentityAuditInfo{
			{AuditInfo: []byte("audit1")},
		},
	}
	auditInfoBytes, err := ai.Bytes()
	require.NoError(t, err)

	matcher, err := deserializer.GetAuditInfoMatcher(context.Background(), typedID, auditInfoBytes)
	require.Error(t, err)
	assert.Nil(t, matcher)
	assert.Contains(t, err.Error(), "expected")
	assert.Contains(t, err.Error(), "audit info but received")

	// Verify no calls to verifier (count mismatch detected before matcher creation)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify no calls to matcher (count mismatch detected before calling it)
	assert.Equal(t, 0, auditInfoMatcher.GetAuditInfoMatcherCallCount())
}

// Create a valid multi-id of type multi-sig and fail to deserialize a matcher from it
// because (the mock) GetAuditInfoMatcher is set to fail
func TestGetAuditInfoMatcher_MatcherError(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	auditInfoMatcher := &mock.AuditInfoMatcher{}
	auditInfoMatcher.GetAuditInfoMatcherReturns(nil, errors.New("matcher error"))

	deserializer := NewTypedIdentityDeserializer(verifierDES, auditInfoMatcher)

	identities := []token.Identity{[]byte("valid_multisig_identity")}
	mi := &MultiIdentity{Identities: identities}
	rawIdentity, err := mi.Serialize()
	require.NoError(t, err)

	typedID, err := identity.WrapWithType(Multisig, rawIdentity)
	require.NoError(t, err)

	ai := &AuditInfo{
		IdentityAuditInfos: []IdentityAuditInfo{
			{AuditInfo: []byte("audit1")},
		},
	}
	auditInfoBytes, err := ai.Bytes()
	require.NoError(t, err)

	matcher, err := deserializer.GetAuditInfoMatcher(context.Background(), typedID, auditInfoBytes)
	require.Error(t, err)
	assert.Nil(t, matcher)

	// Verify no calls to verifier (matcher creation failed)
	assert.Equal(t, 0, verifierDES.DeserializeVerifierCallCount())
	// Verify matcher was called once (and returned error)
	assert.Equal(t, 1, auditInfoMatcher.GetAuditInfoMatcherCallCount())
}

// Serialize a raw multi-Audit-Info with two specified AuditInfos
// and test that the result is like the original
func TestAuditInfoDeserializer_DeserializeAuditInfo_Success(t *testing.T) {
	deserializer := &AuditInfoDeserializer{}

	ai := &AuditInfo{
		IdentityAuditInfos: []IdentityAuditInfo{
			{AuditInfo: []byte("audit1")},
			{AuditInfo: []byte("audit2")},
		},
	}
	raw, err := ai.Bytes()
	require.NoError(t, err)

	result, err := deserializer.DeserializeAuditInfo(context.Background(), nil, raw)
	require.NoError(t, err)
	assert.NotNil(t, result)

	resultAI, ok := result.(*AuditInfo)
	assert.True(t, ok)
	assert.Equal(t, ai, resultAI)
}

// Test failure to deserialize Audit-Info from invalid raw data
func TestAuditInfoDeserializer_DeserializeAuditInfo_Invalid(t *testing.T) {
	deserializer := &AuditInfoDeserializer{}

	result, err := deserializer.DeserializeAuditInfo(context.Background(), nil, []byte("invalid"))
	require.Error(t, err)
	assert.Nil(t, result)
}
