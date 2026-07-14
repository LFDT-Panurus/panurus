/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditInfoMatch(t *testing.T) {
	testAuditInfoMatch(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testAuditInfoMatch(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testAuditInfoMatch(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// create backend key manager
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// get an identity from backend
	backendID, err := backendKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	// deserialize the backend audit info
	backendAuditInfo, err := backendKM.DeserializeAuditInfo(t.Context(), backendID.AuditInfo)
	require.NoError(t, err)

	// create nym audit info
	nymAuditInfo := &AuditInfo{
		AuditInfo:       backendAuditInfo,
		IdemixSignature: backendID.Identity,
	}

	// create the nym (commitment to EID)
	nymEID := backendAuditInfo.EidNymAuditData.Nym.Bytes()

	// test Match with correct nym
	err = nymAuditInfo.Match(context.Background(), nymEID)
	require.NoError(t, err)

	// test Match with incorrect nym
	err = nymAuditInfo.Match(context.Background(), []byte("wrong-nym"))
	require.Error(t, err)
}

func TestDeserializeAuditInfo(t *testing.T) {
	testDeserializeAuditInfo(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testDeserializeAuditInfo(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testDeserializeAuditInfo(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// create backend key manager
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// get an identity from backend
	backendID, err := backendKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	// deserialize the backend audit info
	backendAuditInfo, err := backendKM.DeserializeAuditInfo(t.Context(), backendID.AuditInfo)
	require.NoError(t, err)

	// create nym audit info
	nymAuditInfo := &AuditInfo{
		AuditInfo:       backendAuditInfo,
		IdemixSignature: backendID.Identity,
	}

	// serialize it
	raw, err := json.Marshal(nymAuditInfo)
	require.NoError(t, err)

	// deserialize it
	deserialized, err := DeserializeAuditInfo(raw)
	require.NoError(t, err)
	assert.NotNil(t, deserialized)
	assert.NotNil(t, deserialized.AuditInfo)
	assert.NotNil(t, deserialized.IdemixSignature)
	assert.Equal(t, []byte(backendID.Identity), deserialized.IdemixSignature)
}

func TestDeserializeAuditInfoErrorPaths(t *testing.T) {
	// test with nil
	_, err := DeserializeAuditInfo(nil)
	require.Error(t, err)

	// test with empty bytes
	_, err = DeserializeAuditInfo([]byte{})
	require.Error(t, err)

	// test with invalid JSON
	_, err = DeserializeAuditInfo([]byte("invalid json"))
	require.Error(t, err)

	// test with valid JSON but missing AuditInfo
	auditInfo := &AuditInfo{
		IdemixSignature: []byte("signature"),
	}
	raw, err := json.Marshal(auditInfo)
	require.NoError(t, err)
	_, err = DeserializeAuditInfo(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no audit info found")

	// test with valid JSON but missing IdemixSignature
	auditInfo2 := &AuditInfo{
		AuditInfo: &crypto.AuditInfo{
			Attributes: [][]byte{[]byte("attr1")},
		},
	}
	raw2, err := json.Marshal(auditInfo2)
	require.NoError(t, err)
	_, err = DeserializeAuditInfo(raw2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no idemix signature found")

	// test with valid JSON but missing Attributes
	auditInfo3 := &AuditInfo{
		AuditInfo:       &crypto.AuditInfo{},
		IdemixSignature: []byte("signature"),
	}
	raw3, err := json.Marshal(auditInfo3)
	require.NoError(t, err)
	_, err = DeserializeAuditInfo(raw3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no attributes found")
}

func TestAuditInfoMatchErrorPaths(t *testing.T) {
	testAuditInfoMatchErrorPaths(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testAuditInfoMatchErrorPaths(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testAuditInfoMatchErrorPaths(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// create backend key manager
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// get two different identities
	backendID1, err := backendKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	config2, err := crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyStore2, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider2, err := crypto.NewBCCSP(keyStore2, curveID)
	require.NoError(t, err)
	backendKM2, err := idemix.NewKeyManager(config2, types.EidNymRhNym, cryptoProvider2)
	require.NoError(t, err)
	backendID2, err := backendKM2.Identity(t.Context(), nil)
	require.NoError(t, err)

	// deserialize the first audit info
	backendAuditInfo1, err := crypto.DeserializeAuditInfo(backendID1.AuditInfo)
	require.NoError(t, err)

	// create nym audit info with first identity's data
	nymAuditInfo := &AuditInfo{
		AuditInfo:       backendAuditInfo1,
		IdemixSignature: backendID1.Identity,
	}
	nymAuditInfo.Csp = cryptoProvider
	nymAuditInfo.IssuerPublicKey = backendKM.IssuerPublicKey
	nymAuditInfo.SchemaManager = backendKM.SchemaManager
	nymAuditInfo.Schema = backendKM.Schema

	// get nym from second identity
	backendAuditInfo2, err := crypto.DeserializeAuditInfo(backendID2.AuditInfo)
	require.NoError(t, err)
	nymEID2 := backendAuditInfo2.EidNymAuditData.Nym.Bytes()

	// test Match with mismatched nym (should fail)
	err = nymAuditInfo.Match(context.Background(), nymEID2)
	require.Error(t, err)
}
