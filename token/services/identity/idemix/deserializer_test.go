/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/schema"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDeserializer(t *testing.T) {
	testNewDeserializer(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewDeserializer(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewDeserializer(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// init
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// key manager
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)

	// get an identity
	identityDescriptor, err := keyManager.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	auditInfoRaw := identityDescriptor.AuditInfo

	// instantiate a deserializer and check that it fils
	_, err = NewDeserializer(config.Ipk, -1)
	require.Error(t, err)
	_, err = NewDeserializer(nil, curveID)
	require.Error(t, err)
	_, err = NewDeserializer([]byte{}, curveID)
	require.Error(t, err)
	_, err = NewDeserializer([]byte{0, 1, 2}, curveID)
	require.Error(t, err)

	// instantiate a deserializer and validate it
	d, err := NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)
	assert.NotNil(t, d)
	assert.Equal(t, fmt.Sprintf("Idemix with IPK [%s]", utils.Hashable(d.Ipk).String()), d.String())
	_, err = d.DeserializeVerifier(t.Context(), nil)
	require.Error(t, err)
	_, err = d.DeserializeVerifier(t.Context(), []byte{})
	require.Error(t, err)
	_, err = d.DeserializeVerifier(t.Context(), []byte{0, 1, 2, 3})
	require.Error(t, err)
	verifier1, err := d.DeserializeVerifierAgainstNymEID(id, nil)
	require.NoError(t, err)
	verifier2, err := d.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	signer, err := keyManager.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)

	// sign and verify
	msg := []byte("test message")
	sigma, err := signer.Sign(msg)
	require.NoError(t, err)
	err = verifier1.Verify(msg, sigma)
	require.NoError(t, err)
	err = verifier2.Verify(msg, sigma)
	require.NoError(t, err)

	// check audit info
	auditInfo, err := d.DeserializeAuditInfo(t.Context(), nil, auditInfoRaw)
	require.NoError(t, err)
	assert.NotNil(t, auditInfo)
	assert.Equal(t, "alice", auditInfo.EnrollmentID())
	assert.Equal(t, "150", auditInfo.RevocationHandle())
	auditInfoDeser := &AuditInfoDeserializer{}
	// check invalid input
	_, err = auditInfoDeser.DeserializeAuditInfo(t.Context(), nil, nil)
	require.Error(t, err)
	_, err = auditInfoDeser.DeserializeAuditInfo(t.Context(), nil, []byte{})
	require.Error(t, err)
	_, err = auditInfoDeser.DeserializeAuditInfo(t.Context(), nil, []byte{0, 1, 2, 3})
	require.Error(t, err)
	auditInfo2, err := auditInfoDeser.DeserializeAuditInfo(t.Context(), nil, auditInfoRaw)
	require.NoError(t, err)
	assert.Equal(t, "alice", auditInfo2.EnrollmentID())
	assert.Equal(t, "150", auditInfo2.RevocationHandle())

	// match audit info
	auditInfoMatcher, err := d.GetAuditInfoMatcher(t.Context(), id, auditInfoRaw)
	require.NoError(t, err)
	assert.NotNil(t, auditInfoMatcher)
	require.NoError(t, auditInfoMatcher.Match(t.Context(), id))
	require.NoError(t, d.MatchIdentity(t.Context(), id, auditInfoRaw))

	// check info
	info, err := d.Info(t.Context(), id, []byte{})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: []"))
	info, err = d.Info(t.Context(), id, nil)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: []"))
	_, err = d.Info(t.Context(), id, []byte{0, 1, 2})
	require.Error(t, err)
	info, err = d.Info(t.Context(), id, auditInfoRaw)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: [alice]"))
}

// TestDeserializerErrorPaths tests various error paths in different deserializer construction paths
func TestDeserializerErrorPaths(t *testing.T) {
	testDeserializerErrorPaths(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testDeserializerErrorPaths(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
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

	// Test NewDeserializerWithBCCSP with empty IPK
	_, err = NewDeserializerWithBCCSP(
		schema.NewDefaultManager(),
		schema.DefaultSchema,
		nil,
		types.ExpectEidNymRhNym,
		nil,
		cryptoProvider,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no issuer public key provided")

	// Test NewDeserializerWithBCCSP with invalid schema
	_, err = NewDeserializerWithBCCSP(
		schema.NewDefaultManager(),
		"invalid-schema",
		config.Ipk,
		types.ExpectEidNymRhNym,
		nil,
		cryptoProvider,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not obtain PublicKeyImportOpts")

	// Test DeserializeVerifierAgainstNymEID with invalid identity
	d, err := NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)
	_, err = d.DeserializeVerifierAgainstNymEID([]byte{0, 1, 2}, nil)
	require.Error(t, err)

	// Test creating an Information string based on invalid raw audit info
	_, err = d.Info(context.Background(), []byte("test-id"), []byte{0, 1, 2})
	require.Error(t, err)
}

// TestAuditInfoDeserializerEdgeCases tests that the AuditInfoDeserializer fails when given no raw data
func TestAuditInfoDeserializerEdgeCases(t *testing.T) {
	deserializer := &AuditInfoDeserializer{}

	// Test with empty bytes
	_, err := deserializer.DeserializeAuditInfo(context.Background(), nil, []byte{})
	require.Error(t, err)

	// Test with nil
	_, err = deserializer.DeserializeAuditInfo(context.Background(), nil, nil)
	require.Error(t, err)
}
