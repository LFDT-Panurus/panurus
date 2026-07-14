/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/mock"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDeserializer(t *testing.T) {
	testNewDeserializer(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewDeserializer(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewDeserializer(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// init backend
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// create idemix backend key manager and deserializer
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)
	assert.NotNil(t, backendDeserializer)

	// create idemixnym key manager to get proper nym identities
	identityStoreService := &mock.IdentityStoreService{}
	nymKM := NewKeyManager(backendKM, identityStoreService)
	require.NotNil(t, nymKM)

	// create idemixnym deserializer
	d := NewDeserializer(backendDeserializer)
	require.NotNil(t, d)
	assert.Equal(t, fmt.Sprintf("IdemixNym on [%s]", backendDeserializer), d.String())

	// get a nym identity to test with
	identityDescriptor, err := nymKM.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	auditInfoRaw := identityDescriptor.AuditInfo

	// test DeserializeVerifier with invalid inputs
	_, err = d.DeserializeVerifier(t.Context(), nil)
	require.NoError(t, err) // Returns verifier with nil NymEID
	_, err = d.DeserializeVerifier(t.Context(), []byte{})
	require.NoError(t, err) // Returns verifier with empty NymEID

	// test DeserializeVerifier with valid input
	verifier, err := d.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	assert.NotNil(t, verifier)

	// test DeserializeAuditInfo
	auditInfo, err := d.DeserializeAuditInfo(t.Context(), nil, auditInfoRaw)
	require.NoError(t, err)
	assert.NotNil(t, auditInfo)
	assert.Equal(t, "alice", auditInfo.EnrollmentID())

	// test GetAuditInfoMatcher
	matcher, err := d.GetAuditInfoMatcher(t.Context(), id, auditInfoRaw)
	require.NoError(t, err)
	assert.NotNil(t, matcher)

	// test MatchIdentity
	err = d.MatchIdentity(t.Context(), id, auditInfoRaw)
	require.NoError(t, err)

	// test Info
	info, err := d.Info(t.Context(), id, auditInfoRaw)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: [alice]"))

	// test Info with empty audit info
	info, err = d.Info(t.Context(), id, []byte{})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: []"))

	// test Info with nil audit info
	info, err = d.Info(t.Context(), id, nil)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: []"))
}

func TestDeserializerErrorPaths(t *testing.T) {
	testDeserializerErrorPaths(t, "../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testDeserializerErrorPaths(t, "../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testDeserializerErrorPaths(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// create backend key manager and deserializer
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)

	// create idemixnym key manager and deserializer
	identityStoreService := &mock.IdentityStoreService{}
	nymKM := NewKeyManager(backendKM, identityStoreService)
	require.NotNil(t, nymKM)
	d := NewDeserializer(backendDeserializer)
	require.NotNil(t, d)

	// test DeserializeAuditInfo with invalid input
	_, err = d.DeserializeAuditInfo(t.Context(), nil, nil)
	require.Error(t, err)
	_, err = d.DeserializeAuditInfo(t.Context(), nil, []byte{})
	require.Error(t, err)
	_, err = d.DeserializeAuditInfo(t.Context(), nil, []byte{0, 1, 2})
	require.Error(t, err)

	// test GetAuditInfoMatcher with invalid input
	_, err = d.GetAuditInfoMatcher(t.Context(), []byte("test-id"), []byte{0, 1, 2})
	require.Error(t, err)

	// test MatchIdentity with invalid audit info
	err = d.MatchIdentity(t.Context(), []byte("test-id"), []byte{0, 1, 2})
	require.Error(t, err)

	// test Info with invalid audit info
	_, err = d.Info(t.Context(), []byte("test-id"), []byte{0, 1, 2})
	require.Error(t, err)

	// get a valid identity to test mismatched audit info
	identityDescriptor, err := nymKM.Identity(context.Background(), nil)
	require.NoError(t, err)

	// create another key manager with different config
	config2, err := crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyStore2, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider2, err := crypto.NewBCCSP(keyStore2, curveID)
	require.NoError(t, err)
	backendKM2, err := idemix.NewKeyManager(config2, types.EidNymRhNym, cryptoProvider2)
	require.NoError(t, err)
	nymKM2 := NewKeyManager(backendKM2, identityStoreService)
	require.NotNil(t, nymKM2)
	identityDescriptor2, err := nymKM2.Identity(context.Background(), nil)
	require.NoError(t, err)

	// test MatchIdentity with mismatched identity and audit info
	err = d.MatchIdentity(context.Background(), identityDescriptor2.Identity, identityDescriptor.AuditInfo)
	require.Error(t, err)
}

func TestDeserializeAuditInfoEdgeCases(t *testing.T) {
	config, err := crypto.NewConfig("../idemix/testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)

	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	d := NewDeserializer(backendDeserializer)

	// Test with empty bytes
	_, err = d.DeserializeAuditInfo(context.Background(), nil, []byte{})
	require.Error(t, err)

	// Test with nil
	_, err = d.DeserializeAuditInfo(context.Background(), nil, nil)
	require.Error(t, err)

	// Test with invalid JSON
	_, err = d.DeserializeAuditInfo(context.Background(), nil, []byte("invalid json"))
	require.Error(t, err)
}
