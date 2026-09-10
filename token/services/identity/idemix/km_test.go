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
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
	identityProvider := identity.NewProvider(logging.MustGetLogger(), storage, deserializer.NewTypedSignerDeserializerMultiplex(), nil, nil, nil)
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
	// deserialize a valid signer — no key-store lookups happen in DeserializeSigningIdentity
	// now that the ephemeral sign-and-verify liveness check has been removed.
	signer, err := keyManager.DeserializeSigner(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, 3, tracker.GetCounter)

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

	// sign and verify — Sign fetches NymKey + UserKey (2 gets), Verify uses held Key objects (0 gets).
	sigma, err := signer.Sign([]byte("hello world!!!"))
	require.NoError(t, err)
	require.NoError(t, verifier.Verify([]byte("hello world!!!"), sigma))
	assert.Equal(t, 5, tracker.GetCounter)
	assert.Equal(t, hex.EncodeToString(keyManager.userKeySKI), tracker.GetHistory[4].Key)
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

	// DeserializeSigner for a same-issuer identity now succeeds: the issuer-proof check in
	// Deserialize passes, and ownership of the nym key is not verified here. Callers that
	// need to distinguish locally-owned identities must use the IsMe / signer-cache path.
	_, err = keyManager.DeserializeSigner(t.Context(), id2)
	require.NoError(t, err)
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

// TestDeserialize_RejectsDifferentIssuerIdentity verifies that p.Deserialize (and therefore
// DeserializeSigningIdentity) rejects an identity issued by a different idemix issuer.
// The sameissuer/ testdata directory contains a separate CA with a distinct IssuerPublicKey,
// so identities it issues will fail the ZK association-proof check against the local issuer key.
func TestDeserialize_RejectsDifferentIssuerIdentity(t *testing.T) {
	backend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.FP256BN_AMCL, kvs2.Keystore(backend))
	require.NoError(t, err)
	csp, err := crypto.NewBCCSP(keyStore, math.FP256BN_AMCL)
	require.NoError(t, err)

	config, err := crypto.NewConfig("./testdata/fp256bn_amcl/idemix")
	require.NoError(t, err)
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, csp)
	require.NoError(t, err)

	// Build a key manager under a genuinely different issuer (separate key store so no shared state).
	foreignBackend, err := kvs2.NewInMemory()
	require.NoError(t, err)
	foreignKeyStore, err := crypto.NewKeyStore(math.FP256BN_AMCL, kvs2.Keystore(foreignBackend))
	require.NoError(t, err)
	foreignCSP, err := crypto.NewBCCSP(foreignKeyStore, math.FP256BN_AMCL)
	require.NoError(t, err)
	foreignConfig, err := crypto.NewConfig("./testdata/fp256bn_amcl/sameissuer/idemix")
	require.NoError(t, err)
	foreignKM, err := NewKeyManager(foreignConfig, types.EidNymRhNym, foreignCSP)
	require.NoError(t, err)

	foreignDesc, err := foreignKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	// p.Deserialize verifies the ZK association proof; a different issuer's proof is invalid here.
	_, err = keyManager.Deserialize(t.Context(), foreignDesc.Identity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot deserialize, invalid identity")

	// DeserializeSigningIdentity must also fail — it delegates to Deserialize first.
	_, err = keyManager.DeserializeSigningIdentity(t.Context(), foreignDesc.Identity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot deserialize, invalid identity")
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

	// Create a key manager backed by a genuinely different issuer key.
	// fp256bn_amcl sameissuer/ testdata uses a distinct IssuerPublicKey from the main idemix/ fixtures.
	// For curves without a sameissuer fixture this block is skipped.
	foreignConfigPath := filepath.Join(filepath.Dir(configPath), "sameissuer", filepath.Base(configPath))
	if foreignConfig, ferr := crypto.NewConfig(foreignConfigPath); ferr == nil {
		foreignStore, ferr := crypto.NewKeyStore(curveID, kvs2.Keystore(backend))
		require.NoError(t, ferr)
		foreignCSP, ferr := crypto.NewBCCSP(foreignStore, curveID)
		require.NoError(t, ferr)
		foreignKM, ferr := NewKeyManager(foreignConfig, types.EidNymRhNym, foreignCSP)
		require.NoError(t, ferr)

		foreignDesc, ferr := foreignKM.Identity(context.Background(), nil)
		require.NoError(t, ferr)

		// p.Deserialize verifies the ZK association proof against the local issuer public key;
		// an identity from a different issuer must be rejected before the signing identity is built.
		_, err = keyManager.DeserializeSigningIdentity(context.Background(), foreignDesc.Identity)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot deserialize, invalid identity")
	}
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

// countingCSP decorates a real bccsp.BCCSP and counts calls to Sign, so tests can prove that a
// code path never generates a signature (the sign+verify probe DeserializeSigningIdentity relies
// on, and DeserializeSigningIdentityNoProbe must not run).
type countingCSP struct {
	types.BCCSP
	signCount int
}

func (c *countingCSP) Sign(k types.Key, digest []byte, opts types.SignerOpts) ([]byte, error) {
	c.signCount++

	return c.BCCSP.Sign(k, digest, opts)
}

// TestKeyManager_DeserializeSigningIdentityNoProbe proves that DeserializeSigningIdentityNoProbe
// reconstructs a valid, usable signing identity without generating the extra probe signature that
// DeserializeSigningIdentity relies on to distinguish the right key manager from a wrong one.
func TestKeyManager_DeserializeSigningIdentityNoProbe(t *testing.T) {
	testKeyManager_DeserializeSigningIdentityNoProbe(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testKeyManager_DeserializeSigningIdentityNoProbe(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testKeyManager_DeserializeSigningIdentityNoProbe(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	realCsp, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)
	csp := &countingCSP{BCCSP: realCsp}

	keyManager, err := NewKeyManager(config, types.EidNymRhNym, csp)
	require.NoError(t, err)

	identityDescriptor, err := keyManager.Identity(t.Context(), nil)
	require.NoError(t, err)
	id := identityDescriptor.Identity

	signCountBeforeReconstruction := csp.signCount

	si, err := keyManager.DeserializeSigningIdentityNoProbe(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, si)
	assert.Equal(t, signCountBeforeReconstruction, csp.signCount, "no-probe reconstruction must not call Sign")

	// the reconstructed signing identity is fully usable: it can sign and its own signature verifies
	verifier, err := keyManager.DeserializeVerifier(t.Context(), id)
	require.NoError(t, err)
	msg := []byte("hello world!!!")
	sigma, err := si.Sign(msg)
	require.NoError(t, err)
	require.NoError(t, verifier.Verify(msg, sigma))

	// contrast: the probing variant does call Sign at least once
	signCountBeforeProbe := csp.signCount
	_, err = keyManager.DeserializeSigningIdentity(t.Context(), id)
	require.NoError(t, err)
	assert.Greater(t, csp.signCount, signCountBeforeProbe, "probing reconstruction must call Sign")
}

// TestKeyManager_DeserializeSigningIdentityNoProbeRemote proves that
// DeserializeSigningIdentityNoProbe fails closed for a remote (verify-only) key manager instead of
// returning a broken signer.
func TestKeyManager_DeserializeSigningIdentityNoProbeRemote(t *testing.T) {
	testKeyManager_DeserializeSigningIdentityNoProbeRemote(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
}

func testKeyManager_DeserializeSigningIdentityNoProbeRemote(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// build a local key manager first to obtain a valid serialized identity to try to reconstruct
	localKM, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	identityDescriptor, err := localKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	// now strip the secret key material to simulate a remote/verify-only wallet
	config.Signer.Sk = nil
	config.Signer.Cred = nil
	config.Signer.Ski = nil
	remoteKM, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	assert.True(t, remoteKM.IsRemote())

	_, err = remoteKM.DeserializeSigningIdentityNoProbe(t.Context(), identityDescriptor.Identity)
	require.Error(t, err)
}

// verifyOverridingCSP decorates a real bccsp.BCCSP and forces the outcome of credential
// verification, so tests can exercise both ways the BCCSP reports a bad credential: a plain
// `false` with no error, and a `false` accompanied by an error.
type verifyOverridingCSP struct {
	types.BCCSP
	valid     bool
	verifyErr error
}

func (c *verifyOverridingCSP) Verify(k types.Key, signature, digest []byte, opts types.SignerOpts) (bool, error) {
	if _, ok := opts.(*types.IdemixCredentialSignerOpts); ok {
		return c.valid, c.verifyErr
	}

	return c.BCCSP.Verify(k, signature, digest, opts)
}

// TestNewKeyManagerCredentialVerificationFailure is a regression test for the case where the BCCSP
// reports a cryptographically invalid credential through its boolean return value alone
// (valid == false, err == nil). The constructor used to funnel both failure modes through
// errors.WithMessagef(err, ...), which returns nil when the wrapped error is nil, so it returned
// (nil, nil): callers checking only err != nil went on to use a nil *KeyManager and panicked
// later, away from the real cause.
func TestNewKeyManagerCredentialVerificationFailure(t *testing.T) {
	for _, curve := range []struct {
		configPath string
		curveID    math.CurveID
	}{
		{"./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY},
		{"./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY},
	} {
		for _, tc := range []struct {
			name      string
			valid     bool
			verifyErr error
			// contains lists substrings the returned error must mention
			contains []string
		}{
			{
				// the bug: failure signalled by the boolean only
				name:     "invalid credential, no error from the BCCSP",
				valid:    false,
				contains: []string{"credential is not cryptographically valid"},
			},
			{
				name:      "invalid credential, error from the BCCSP",
				valid:     false,
				verifyErr: errors.New("verification exploded"),
				contains:  []string{"credential is not cryptographically valid", "verification exploded"},
			},
			{
				// an error alongside valid == true is still a failure
				name:      "error from the BCCSP with valid set",
				valid:     true,
				verifyErr: errors.New("verification exploded"),
				contains:  []string{"credential is not cryptographically valid", "verification exploded"},
			},
		} {
			t.Run(tc.name+" ["+curve.configPath+"]", func(t *testing.T) {
				kvs, err := kvs2.NewInMemory()
				require.NoError(t, err)
				config, err := crypto.NewConfig(curve.configPath)
				require.NoError(t, err)
				keyStore, err := crypto.NewKeyStore(curve.curveID, kvs2.Keystore(kvs))
				require.NoError(t, err)
				realCSP, err := crypto.NewBCCSP(keyStore, curve.curveID)
				require.NoError(t, err)

				keyManager, err := NewKeyManager(
					config,
					types.EidNymRhNym,
					&verifyOverridingCSP{BCCSP: realCSP, valid: tc.valid, verifyErr: tc.verifyErr},
				)
				// the invariant callers rely on: a failure is never reported as (nil, nil)
				require.Error(t, err, "credential verification failure must return a non-nil error")
				require.Nil(t, keyManager)
				for _, substring := range tc.contains {
					require.ErrorContains(t, err, substring)
				}
			})
		}
	}
}

// TestNewKeyManagerCredentialVerificationSuccess pins the happy path through the same decorator,
// proving the failure cases above are caused by the verification outcome and not by the decorator
// itself.
func TestNewKeyManagerCredentialVerificationSuccess(t *testing.T) {
	const configPath = "./testdata/bls12_381_bbs_gurvy/idemix"
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(math.BLS12_381_BBS_GURVY, kvs2.Keystore(kvs))
	require.NoError(t, err)
	realCSP, err := crypto.NewBCCSP(keyStore, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)

	keyManager, err := NewKeyManager(
		config,
		types.EidNymRhNym,
		&verifyOverridingCSP{BCCSP: realCSP, valid: true},
	)
	require.NoError(t, err)
	require.NotNil(t, keyManager)
}

// TestNewKeyManagerTamperedCredential drives the same failure end to end, with no test double: a
// byte of the real credential is flipped so the real BCCSP rejects it. Whichever way the BCCSP
// signals the rejection, construction must fail loudly rather than hand back a nil key manager.
func TestNewKeyManagerTamperedCredential(t *testing.T) {
	testNewKeyManagerTamperedCredential(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testNewKeyManagerTamperedCredential(t, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testNewKeyManagerTamperedCredential(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	kvs, err := kvs2.NewInMemory()
	require.NoError(t, err)
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)
	keyStore, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider, err := crypto.NewBCCSP(keyStore, curveID)
	require.NoError(t, err)

	// sanity check: the untouched credential is accepted
	keyManager, err := NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	require.NotNil(t, keyManager)

	// tamper with the credential's signature material
	config, err = crypto.NewConfig(configPath)
	require.NoError(t, err)
	require.NotEmpty(t, config.Signer.Cred)
	config.Signer.Cred[len(config.Signer.Cred)-1] ^= 0xFF

	keyManager, err = NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.Error(t, err, "a tampered credential must return a non-nil error")
	require.Nil(t, keyManager)
	require.ErrorContains(t, err, "credential is not cryptographically valid")
}
