/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/IBM/idemix/bccsp/types"
	idemixmsp "github.com/IBM/idemix/msp"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/schema"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	kvs2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	_ "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/memory"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKeyManager(t *testing.T) {
	testNewKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewKeyManager(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewKeyManager(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	tracker := kvs2.NewTrackedMemoryFrom(kvs)
	keyStore, err := crypto.NewKeyStore(curveID, tracker)
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// check that version is enforced
	config.Version = 0
	_, err = NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.Error(t, err)
	require.EqualError(t, err, "unsupported protocol version [0]")
	config.Version = crypto.ProtobufProtocolVersionV1

	// new key manager loaded from file
	assert.Empty(t, config.Signer.Ski)
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)
	assert.False(t, keyManager.IsRemote())
	assert.True(t, keyManager.Anonymous())
	assert.Equal(t, "alice", keyManager.EnrollmentID())
	assert.Equal(t, IdentityType, keyManager.IdentityType())
	assert.Equal(t, fmt.Sprintf("Idemix KeyManager [%s]", utils.Hashable(keyManager.Ipk).String()), keyManager.String())
	assert.Equal(t, 1, tracker.PutCounter)
	assert.Equal(t, 0, tracker.GetCounter)

	// the config has been updated, load a new key manager
	assert.NotEmpty(t, config.Signer.Ski)
	keyManager, err = NewKeyManager(config, types.Standard, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)
	assert.False(t, keyManager.IsRemote())
	assert.True(t, keyManager.Anonymous())
	assert.Equal(t, "alice", keyManager.EnrollmentID())
	assert.Equal(t, IdentityType, keyManager.IdentityType())
	assert.Equal(t, fmt.Sprintf("Idemix KeyManager [%s]", utils.Hashable(keyManager.Ipk).String()), keyManager.String())
	assert.Equal(t, 1, tracker.PutCounter) // this is still 1 because the key is loaded using the SKI
	assert.Equal(t, 1, tracker.GetCounter) // one get for the user key
	assert.Equal(t, tracker.GetHistory[0].Key, hex.EncodeToString(config.Signer.Ski))

	// load a new key manager again
	assert.NotEmpty(t, config.Signer.Ski)
	keyManager, err = NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)
	assert.False(t, keyManager.IsRemote())
	assert.True(t, keyManager.Anonymous())
	assert.Equal(t, "alice", keyManager.EnrollmentID())
	assert.Equal(t, IdentityType, keyManager.IdentityType())
	assert.Equal(t, fmt.Sprintf("Idemix KeyManager [%s]", utils.Hashable(keyManager.Ipk).String()), keyManager.String())
	assert.Equal(t, 1, tracker.PutCounter) // this is still 1 because the key is loaded using the SKI
	assert.Equal(t, 2, tracker.GetCounter) // another get for the user key
	assert.Equal(t, tracker.GetHistory[1].Key, hex.EncodeToString(config.Signer.Ski))

	// invalid sig type
	_, err = NewKeyManager(config, -1, cryptoProvider)
	require.Error(t, err)
	require.EqualError(t, err, "unsupported signature type -1")

	assert.Equal(t, 1, tracker.PutCounter)
	assert.Equal(t, 3, tracker.GetCounter) // another get
	assert.Equal(t, tracker.GetHistory[2].Key, hex.EncodeToString(config.Signer.Ski))

	// no config
	_, err = NewKeyManager(nil, types.EidNymRhNym, cryptoProvider)
	require.Error(t, err)
	require.EqualError(t, err, "no idemix config provided")

	// no signer in config
	config.Signer = nil
	_, err = NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.Error(t, err)
	require.EqualError(t, err, "no signer information found")

	// nothing changed
	assert.Equal(t, 1, tracker.PutCounter)
	assert.Equal(t, 3, tracker.GetCounter)
}

func TestIdentityWithEidRhNymPolicy(t *testing.T) {
	testIdentityWithEidRhNymPolicy(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testIdentityWithEidRhNymPolicy(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testIdentityWithEidRhNymPolicy(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	registry := view.NewServiceProvider()
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterService(kvs))
	storage := kvs2.NewIdentityStore(kvs, token.TMSID{Network: "pineapple"})
	identityProvider := identity.NewProvider(logging.MustGetLogger(), storage, deserializer.NewTypedSignerDeserializerMultiplex(), nil, nil)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	tracker := kvs2.NewTrackedMemoryFrom(kvs)
	keyStore, err := crypto.NewKeyStore(curveID, tracker)
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// init key manager
	// with invalid sig type
	_, err = NewKeyManager(config, -1, cryptoProvider)
	require.Error(t, err)
	require.EqualError(t, err, "unsupported signature type -1")
	// correctly
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)

	// get an identity and check it
	identityDescriptor, err := keyManager.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	audit := identityDescriptor.AuditInfo
	require.NoError(t, identityProvider.RegisterSigner(t.Context(), id, identityDescriptor.Signer, identityDescriptor.Verifier, identityDescriptor.SignerInfo, false))
	assert.NotNil(t, id)
	assert.NotNil(t, audit)
	info, err := keyManager.Info(t.Context(), id, audit)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: [alice]"))
	// check SKI
	idSKI, err := crypto.SKIFromIdentity(id)
	require.NoError(t, err)
	require.Equal(t, identityDescriptor.Signer.(*crypto.SigningIdentity).NymPublicKey.SKI(), idSKI)

	// get another identity and compare the info
	identityDescriptor2, err := keyManager.Identity(t.Context(), audit)
	require.NoError(t, err)
	id2 := identityDescriptor2.Identity
	audit2 := identityDescriptor2.AuditInfo
	assert.NotNil(t, id2)
	assert.NotNil(t, audit2)
	info2, err := keyManager.Info(t.Context(), id2, audit2)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info2, "Idemix: [alice]"))
	assert.Equal(t, audit, audit2)
	// check SKI
	idSKI, err = crypto.SKIFromIdentity(id2)
	require.NoError(t, err)
	require.Equal(t, identityDescriptor2.Signer.(*crypto.SigningIdentity).NymPublicKey.SKI(), idSKI)

	// deserialize the audit information
	auditInfo, err := keyManager.DeserializeAuditInfo(t.Context(), audit)
	require.NoError(t, err)
	require.NoError(t, auditInfo.Match(t.Context(), id))
	require.NoError(t, auditInfo.Match(t.Context(), id2))
	auditInfo2, err := keyManager.DeserializeAuditInfo(t.Context(), audit2)
	require.NoError(t, err)
	require.NoError(t, auditInfo2.Match(t.Context(), id))
	require.NoError(t, auditInfo2.Match(t.Context(), id2))

	assert.Equal(t, 3, tracker.GetCounter)

	// deserialize an invalid signer
	_, err = keyManager.DeserializeSigner(t.Context(), nil)
	require.Error(t, err)
	_, err = keyManager.DeserializeSigner(t.Context(), []byte{})
	require.Error(t, err)
	_, err = keyManager.DeserializeSigner(t.Context(), []byte{0, 1, 2})
	require.Error(t, err)
	assert.Equal(t, 3, tracker.GetCounter)
	// deserialize a valid signer
	signer, err := keyManager.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, 5, tracker.GetCounter) // this is due the call to Sign used to test if the signer belong to this key manager
	assert.Equal(t, hex.EncodeToString(keyManager.userKeySKI), tracker.GetHistory[4].Key)

	// deserialize an invalid verifier
	_, err = keyManager.DeserializeVerifier(t.Context(), nil)
	require.Error(t, err)
	_, err = keyManager.DeserializeVerifier(t.Context(), []byte{})
	require.Error(t, err)
	_, err = keyManager.DeserializeVerifier(t.Context(), []byte{0, 1, 2})
	require.Error(t, err)
	// deserialize a valid verifier
	verifier, err := keyManager.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	// sign and verify
	sigma, err := signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))
	assert.Equal(t, 7, tracker.GetCounter)
	assert.Equal(t, tracker.GetHistory[3].Key, tracker.GetHistory[5].Key)
	assert.Equal(t, tracker.GetHistory[3].Value, tracker.GetHistory[5].Value)
	assert.Equal(t, hex.EncodeToString(keyManager.userKeySKI), tracker.GetHistory[6].Key)
	assert.Equal(t, tracker.GetHistory[4].Value, tracker.GetHistory[6].Value)
}

func TestIdentityStandard(t *testing.T) {
	testIdentityStandard(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testIdentityStandard(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testIdentityStandard(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	registry := view.NewServiceProvider()

	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterService(kvs))

	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)

	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	p, err := NewKeyManager(config, types.Standard, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	identityDescriptor, err := p.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	audit := identityDescriptor.AuditInfo
	assert.NotNil(t, id)
	assert.Nil(t, audit)
	// check SKI
	idSKI, err := crypto.SKIFromIdentity(id)
	require.NoError(t, err)
	require.Equal(t, identityDescriptor.Signer.(*crypto.SigningIdentity).NymPublicKey.SKI(), idSKI)

	signer, err := p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err := p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err := signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))

	keyStore, err = crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err = crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	p, err = NewKeyManager(config, types.Standard, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	_, err = p.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.NotNil(t, id)
	assert.Nil(t, audit)

	signer, err = p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err = p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err = signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))

	keyStore, err = crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err = crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	p, err = NewKeyManager(config, Any, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	_, err = p.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.NotNil(t, id)
	assert.Nil(t, audit)

	signer, err = p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err = p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err = signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))
}

func TestAuditWithEidRhNymPolicy(t *testing.T) {
	testAuditWithEidRhNymPolicy(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testAuditWithEidRhNymPolicy(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testAuditWithEidRhNymPolicy(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	registry := view.NewServiceProvider()

	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterService(kvs))

	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	p, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	config, err = crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyStore, err = crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err = crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	p2, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p2)

	identityDescriptor, err := p.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	audit := identityDescriptor.AuditInfo
	assert.NotNil(t, id)
	assert.NotNil(t, audit)
	// check SKI
	idSKI, err := crypto.SKIFromIdentity(id)
	require.NoError(t, err)
	require.Equal(t, identityDescriptor.Signer.(*crypto.SigningIdentity).NymPublicKey.SKI(), idSKI)

	identityDescriptor2, err := p2.Identity(t.Context(), nil)
	require.NoError(t, err)
	id2 := identityDescriptor2.Identity
	audit2 := identityDescriptor2.AuditInfo
	assert.NotNil(t, id2)
	assert.NotNil(t, audit2)
	// check SKI
	idSKI, err = crypto.SKIFromIdentity(id2)
	require.NoError(t, err)
	require.Equal(t, identityDescriptor2.Signer.(*crypto.SigningIdentity).NymPublicKey.SKI(), idSKI)

	auditInfo, err := p.DeserializeAuditInfo(t.Context(), audit)
	require.NoError(t, err)
	require.NoError(t, auditInfo.Match(t.Context(), id))
	require.Error(t, auditInfo.Match(t.Context(), id2))

	auditInfo, err = p2.DeserializeAuditInfo(t.Context(), audit)
	require.NoError(t, err)
	require.NoError(t, auditInfo.FromBytes(audit2))
	require.NoError(t, auditInfo.Match(t.Context(), id2))
	require.Error(t, auditInfo.Match(t.Context(), id))
}

func TestKeyManager_DeserializeSigner(t *testing.T) {
	testKeyManager_DeserializeSigner(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testKeyManager_DeserializeSigner(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testKeyManager_DeserializeSigner(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	registry := view.NewServiceProvider()
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterService(kvs))
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// first key manager
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)

	// second key manager
	config, err = crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyManager2, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager2)

	// keyManager and keyManager2 use the same key store

	identityDescriptor, err := keyManager.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity

	identityDescriptor2, err := keyManager2.Identity(t.Context(), nil)
	require.NoError(t, err)
	id2 := identityDescriptor2.Identity

	// This must work
	signer, err := keyManager.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err := keyManager.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	msg := []byte("Hello World!!!")
	sigma, err := signer.Sign(msg)
	require.NoError(t, err)
	require.NoError(t, verifier.Verify(msg, sigma))

	// Try to deserialize id2 with provider for id, it should fail
	_, err = keyManager.DeserializeSigner(t.Context(), id2)
	require.Error(t, err)
	_, err = keyManager.DeserializeVerifier(t.Context(), id2)
	require.NoError(t, err)

	// this must work
	signer, err = keyManager.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err = keyManager.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	sigma, err = signer.Sign(msg)
	require.NoError(t, err)
	require.NoError(t, verifier.Verify(msg, sigma))
}

func TestIdentityFromFabricCA(t *testing.T) {
	// TODO: regenerate these keys with the gurvy curve
	registry := view.NewServiceProvider()

	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterService(kvs))
	ipkBytes, err := crypto.ReadFile(filepath.Join("./testdata/fp256bn_amcl/charlie.ExtraId2", idemixmsp.IdemixConfigFileIssuerPublicKey))
	require.NoError(t, err)
	config, err := crypto.NewConfigWithIPK(ipkBytes, "./testdata/fp256bn_amcl/charlie.ExtraId2", true)
	require.NoError(t, err)

	keyStore, err := crypto.NewKeyStore(math.BN254, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, math.BN254)
	require.NoError(t, err)
	p, err := NewKeyManager(config, types.Standard, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	identityDescriptor, err := p.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	audit := identityDescriptor.AuditInfo
	assert.NotNil(t, id)
	assert.Nil(t, audit)

	signer, err := p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err := p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err := signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))

	keyStore, err = crypto.NewKeyStore(math.BN254, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err = crypto.NewBCCSP(keyStore, math.BN254)
	require.NoError(t, err)
	p, err = NewKeyManager(config, types.Standard, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	_, err = p.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.NotNil(t, id)
	assert.Nil(t, audit)

	signer, err = p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err = p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err = signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))

	keyStore, err = crypto.NewKeyStore(math.BN254, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err = crypto.NewBCCSP(keyStore, math.BN254)
	require.NoError(t, err)
	p, err = NewKeyManager(config, Any, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	_, err = p.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.NotNil(t, id)
	assert.Nil(t, audit)

	signer, err = p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err = p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err = signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))
}

func TestIdentityFromFabricCAWithEidRhNymPolicy(t *testing.T) {
	// TODO: regenerate these keys with the gurvy curve
	registry := view.NewServiceProvider()

	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	require.NoError(t, registry.RegisterService(kvs))
	ipkBytes, err := crypto.ReadFile(filepath.Join("./testdata/fp256bn_amcl/charlie.ExtraId2", idemixmsp.IdemixConfigFileIssuerPublicKey))
	require.NoError(t, err)
	config, err := crypto.NewConfigWithIPK(ipkBytes, "./testdata/fp256bn_amcl/charlie.ExtraId2", true)
	require.NoError(t, err)

	keyStore, err := crypto.NewKeyStore(math.BN254, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, math.BN254)
	require.NoError(t, err)
	p, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	// get an identity with its own audit info from the provider
	// id is in its serialized form
	identityDescriptor, err := p.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity
	audit := identityDescriptor.AuditInfo
	assert.NotNil(t, id)
	assert.NotNil(t, audit)
	info, err := p.Info(t.Context(), id, audit)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: [charlie.ExtraId2]"))

	auditInfo, err := p.DeserializeAuditInfo(t.Context(), audit)
	require.NoError(t, err)
	require.NoError(t, auditInfo.Match(t.Context(), id))

	signer, err := p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err := p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err := signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))

	keyStore, err = crypto.NewKeyStore(math.BN254, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err = crypto.NewBCCSP(keyStore, math.BN254)
	require.NoError(t, err)
	p, err = NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, p)

	_, err = p.Identity(t.Context(), nil)
	require.NoError(t, err)
	assert.NotNil(t, id)
	assert.NotNil(t, audit)
	info, err = p.Info(t.Context(), id, audit)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(info, "Idemix: [charlie.ExtraId2]"))

	auditInfo, err = p.DeserializeAuditInfo(t.Context(), audit)
	require.NoError(t, err)
	require.NoError(t, auditInfo.Match(t.Context(), id))

	signer, err = p.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	verifier, err = p.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)

	sigma, err = signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))
}

func TestKeyManagerForRace(t *testing.T) {
	t.Run("BLS12_381_BBS_GURVY", func(t *testing.T) {
		keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
		defer cleanup()
		runIdentityConcurrently(t, t.Context(), keyManager)
	})

	t.Run("BLS12_381_BBS", func(t *testing.T) {
		keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS)
		defer cleanup()
		runIdentityConcurrently(t, t.Context(), keyManager)
	})

	t.Run("BLS12_381_BBS_GURVY", func(t *testing.T) {
		keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
		defer cleanup()
		runIdentityConcurrently(t, t.Context(), keyManager)
	})
}

func setupKeyManager(t require.TestingT, configPath string, curveID math.CurveID) (*KeyManager, func()) {
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	tracker := kvs2.NewTrackedMemoryFrom(kvs)
	keyStore, err := crypto.NewKeyStore(curveID, tracker)
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// check that version is enforced
	config.Version = 0
	_, err = NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.Error(t, err)
	require.EqualError(t, err, "unsupported protocol version [0]")
	config.Version = crypto.ProtobufProtocolVersionV1

	// new key manager loaded from file
	assert.Empty(t, config.Signer.Ski)
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.NotNil(t, keyManager)
	assert.False(t, keyManager.IsRemote())
	assert.True(t, keyManager.Anonymous())
	assert.Equal(t, "alice", keyManager.EnrollmentID())
	assert.Equal(t, IdentityType, keyManager.IdentityType())
	assert.Equal(t, fmt.Sprintf("Idemix KeyManager [%s]", utils.Hashable(keyManager.Ipk).String()), keyManager.String())
	assert.Equal(t, 1, tracker.PutCounter)
	assert.Equal(t, 0, tracker.GetCounter)

	return keyManager, func() {
		// cleanup
	}
}

func runIdentityConcurrently(t require.TestingT, ctx context.Context, keyManager *KeyManager) {
	numRoutines := 4
	var wg sync.WaitGroup
	wg.Add(numRoutines)
	for range numRoutines {
		go func(t require.TestingT) {
			defer wg.Done()

			for range 10 {
				id, err2 := keyManager.Identity(ctx, nil)
				assert.NoError(t, err2)
				assert.NotNil(t, id)
				assert.NotEmpty(t, id.Identity)
				assert.NotNil(t, id.Signer)
			}
		}(t)
	}
	wg.Wait()
}

// TestKeyManagerErrorPaths tests various error paths in km.go
func TestKeyManagerErrorPaths(t *testing.T) {
	testKeyManagerErrorPaths(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testKeyManagerErrorPaths(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testKeyManagerErrorPaths(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// Test NewKeyManagerWithSchema with an invalid schema
	_, err = NewKeyManagerWithSchema(
		config,
		types.EidNymRhNym,
		cryptoProvider,
		schema.NewDefaultManager(),
		"invalid-schema",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not obtain PublicKeyImportOpts")

	// Create a valid key manager
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// Test Identity descriptor construction with invalid raw audit info
	_, err = keyManager.Identity(context.Background(), []byte{0, 1, 2})
	require.Error(t, err)

	// Test Information printing about a given id with invalid audit info bytes
	_, err = keyManager.Info(context.Background(), []byte("test-id"), []byte{0, 1, 2})
	require.Error(t, err)

	// Create another valid key manager with a different config
	config2, err := crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyStore2, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider2, err := crypto.NewBCCSP(keyStore2, curveID)
	require.NoError(t, err)
	keyManager2, err := NewKeyManager(config2, types.EidNymRhNym, cryptoProvider2)
	require.NoError(t, err)

	// create a valid identity descriptor using keyManager2
	// then fail when trying to deserialize it using keyManager
	identityDescriptor2, err := keyManager2.Identity(context.Background(), nil)
	require.NoError(t, err)

	_, err = keyManager.DeserializeSigningIdentity(context.Background(), identityDescriptor2.Identity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed verifying verification signature")
}

// TestKeyManagerInfoErrorCases tests error cases in Info method
// that returns a string documenting the given identity and possibly the Enrollment ID (EID)
func TestKeyManagerInfoErrorCases(t *testing.T) {
	testKeyManagerInfoErrorCases(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testKeyManagerInfoErrorCases(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testKeyManagerInfoErrorCases(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// Get a valid identity
	identityDescriptor, err := keyManager.Identity(context.Background(), nil)
	require.NoError(t, err)

	// Test Info with the valid identity but with an empty audit info
	info, err := keyManager.Info(context.Background(), identityDescriptor.Identity, nil)
	require.NoError(t, err)
	assert.Contains(t, info, "Idemix:")

	// Test Info with the valid identity and with valid audit info (should make the
	// returned info string also include the Enrollment ID)
	info, err = keyManager.Info(context.Background(), identityDescriptor.Identity, identityDescriptor.AuditInfo)
	require.NoError(t, err)
	assert.Contains(t, info, "alice")

	// Test Info with mismatched identity and audit info (should fail on Match)
	config2, err := crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyStore2, err := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider2, err := crypto.NewBCCSP(keyStore2, curveID)
	require.NoError(t, err)
	keyManager2, err := NewKeyManager(config2, types.EidNymRhNym, cryptoProvider2)
	require.NoError(t, err)

	identityDescriptor3, err := keyManager2.Identity(context.Background(), nil)
	require.NoError(t, err)

	// Try to get info for identity from keyManager2 using audit info from another keyManager
	// (should fail on Match)
	_, err = keyManager.Info(context.Background(), identityDescriptor3.Identity, identityDescriptor.AuditInfo)
	require.Error(t, err)
}

// TestDeserializeSigningIdentityErrorPath tests error path in DeserializeSigningIdentity
// which tries to deserialize an invalid raw signing identity
func TestDeserializeSigningIdentityErrorPath(t *testing.T) {
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig("./testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// Test with invalid identity bytes
	_, err = keyManager.DeserializeSigningIdentity(context.Background(), []byte{0, 1, 2})
	require.Error(t, err)
}

// TestIdentityWithDifferentAuditInfo tests signing and verifying with
// identities returned by the Identity method using different (but equal) audit infos
func TestIdentityWithDifferentAuditInfo(t *testing.T) {
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig("./testdata/bls12_381_bbs_gurvy/idemix")
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(backend))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)

	// Get first identity
	id1, err := keyManager.Identity(context.Background(), nil)
	require.NoError(t, err)

	// Get second identity with the same audit info
	id2, err := keyManager.Identity(context.Background(), id1.AuditInfo)
	require.NoError(t, err)

	// Verify that both ids have the same audit info
	assert.Equal(t, id1.AuditInfo, id2.AuditInfo)

	// Verify that both identities can be used to sign
	signer1, err := keyManager.DeserializeSigner(context.Background(), id1.Identity)
	require.NoError(t, err)
	signer2, err := keyManager.DeserializeSigner(context.Background(), id2.Identity)
	require.NoError(t, err)

	msg := []byte("test message")
	sig1, err := signer1.Sign(msg)
	require.NoError(t, err)
	sig2, err := signer2.Sign(msg)
	require.NoError(t, err)

	// Verify that both identities can be used to verify
	verifier1, err := keyManager.DeserializeVerifier(context.Background(), id1.Identity)
	require.NoError(t, err)
	verifier2, err := keyManager.DeserializeVerifier(context.Background(), id2.Identity)
	require.NoError(t, err)

	require.NoError(t, verifier1.Verify(msg, sig1))
	require.NoError(t, verifier2.Verify(msg, sig2))
}
