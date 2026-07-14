/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"context"
	"encoding/asn1"
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

func TestSignerSign(t *testing.T) {
	testSignerSign(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSignerSign(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSignerSign(t *testing.T, configPath string, curveID math.CurveID) {
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
	backendAuditInfo, err := crypto.DeserializeAuditInfo(backendID.AuditInfo)
	require.NoError(t, err)

	// create the nym (commitment to EID)
	nymEID := backendAuditInfo.EidNymAuditData.Nym.Bytes()

	// create nym signer
	signer := &Signer{
		Creator: nymEID,
		Signer:  backendID.Signer,
	}

	// sign a message
	msg := []byte("test message")
	sigma, err := signer.Sign(msg)
	require.NoError(t, err)
	assert.NotNil(t, sigma)

	// verify the signature structure
	sig := &Signature{}
	_, err = asn1.Unmarshal(sigma, sig)
	require.NoError(t, err)
	assert.Equal(t, nymEID, sig.Creator)
	assert.NotNil(t, sig.Signature)
}

func TestVerifierVerify(t *testing.T) {
	testVerifierVerify(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testVerifierVerify(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testVerifierVerify(t *testing.T, configPath string, curveID math.CurveID) {
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

	// create backend key manager and deserializer
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)

	// get an identity from backend
	backendID, err := backendKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	// deserialize the backend audit info
	backendAuditInfo, err := backendKM.DeserializeAuditInfo(t.Context(), backendID.AuditInfo)
	require.NoError(t, err)

	// create the nym (commitment to EID)
	nymEID := backendAuditInfo.EidNymAuditData.Nym.Bytes()

	// create nym signer
	signer := &Signer{
		Creator: backendID.Identity,
		Signer:  backendID.Signer,
	}

	// create nym verifier
	verifier := &Verifier{
		NymEID: nymEID,
		Backed: backendDeserializer,
	}

	// sign and verify
	msg := []byte("test message")
	sigma, err := signer.Sign(msg)
	require.NoError(t, err)

	err = verifier.Verify(msg, sigma)
	require.NoError(t, err)

	// verify with wrong message should fail
	err = verifier.Verify([]byte("wrong message"), sigma)
	require.Error(t, err)
}

func TestVerifierVerifyErrorPaths(t *testing.T) {
	testVerifierVerifyErrorPaths(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testVerifierVerifyErrorPaths(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testVerifierVerifyErrorPaths(t *testing.T, configPath string, curveID math.CurveID) {
	t.Helper()
	// prepare
	config, err := crypto.NewConfig(configPath)
	require.NoError(t, err)

	// create backend deserializer
	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)

	// create nym verifier
	verifier := &Verifier{
		NymEID: []byte("test-nym"),
		Backed: backendDeserializer,
	}

	// test with invalid signature (not ASN1)
	msg := []byte("test message")
	err = verifier.Verify(msg, []byte("invalid signature"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to verify idemix-plus signature")

	// test with valid ASN1 but invalid creator
	invalidSig := Signature{
		Creator:   []byte("invalid-creator"),
		Signature: []byte("signature"),
	}
	invalidSigRaw, err := asn1.Marshal(invalidSig)
	require.NoError(t, err)
	err = verifier.Verify(msg, invalidSigRaw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get idemix deserializer")
}

func TestSignerProviderImpl(t *testing.T) {
	testSignerProviderImpl(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSignerProviderImpl(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSignerProviderImpl(t *testing.T, configPath string, curveID math.CurveID) {
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

	// create signer provider
	signerProvider := NewSignerProviderImpl(backendKM, backendID.AuditInfo)
	require.NotNil(t, signerProvider)

	// get a new signer
	id, signer, err := signerProvider.NewSigner(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, id)
	assert.NotNil(t, signer)

	// verify the signer works
	msg := []byte("test message")
	sigma, err := signer.Sign(msg)
	require.NoError(t, err)
	assert.NotNil(t, sigma)
}

func TestSignerEntryBytes(t *testing.T) {
	entry := &SignerEntry{
		Identity:  []byte("identity"),
		AuditInfo: []byte("audit-info"),
		Label:     "test-label",
	}

	raw, err := entry.Bytes()
	require.NoError(t, err)
	assert.NotNil(t, raw)

	// verify it's valid JSON
	var decoded SignerEntry
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)
	assert.Equal(t, entry.Identity, decoded.Identity)
	assert.Equal(t, entry.AuditInfo, decoded.AuditInfo)
	assert.Equal(t, entry.Label, decoded.Label)
}

func TestSignerSignVerifyIntegration(t *testing.T) {
	testSignerSignVerifyIntegration(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testSignerSignVerifyIntegration(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testSignerSignVerifyIntegration(t *testing.T, configPath string, curveID math.CurveID) {
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

	// create backend key manager and deserializer
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)

	// get an identity from backend
	backendID, err := backendKM.Identity(t.Context(), nil)
	require.NoError(t, err)

	// deserialize the backend audit info
	backendAuditInfo, err := crypto.DeserializeAuditInfo(backendID.AuditInfo)
	require.NoError(t, err)

	// create the nym (commitment to EID)
	nymEID := backendAuditInfo.EidNymAuditData.Nym.Bytes()

	// create nym signer
	signer := &Signer{
		Creator: backendID.Identity,
		Signer:  backendID.Signer,
	}

	// create nym verifier
	verifier := &Verifier{
		NymEID: nymEID,
		Backed: backendDeserializer,
	}

	// test multiple sign/verify cycles
	for i := range 5 {
		msg := []byte("test message " + string(rune(i)))
		sigma, err := signer.Sign(msg)
		require.NoError(t, err)

		err = verifier.Verify(msg, sigma)
		require.NoError(t, err)
	}
}

func TestVerifierWithDifferentNyms(t *testing.T) {
	testVerifierWithDifferentNyms(t, "../../idemix/testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	testVerifierWithDifferentNyms(t, "../../idemix/testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS_GURVY)
}

func testVerifierWithDifferentNyms(t *testing.T, configPath string, curveID math.CurveID) {
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

	// create backend key manager and deserializer
	backendKM, err := idemix.NewKeyManager(config, types.EidNymRhNym, cryptoProvider)
	require.NoError(t, err)
	backendDeserializer, err := idemix.NewDeserializer(config.Ipk, curveID)
	require.NoError(t, err)

	// get first identity
	backendID1, err := backendKM.Identity(t.Context(), nil)
	require.NoError(t, err)
	backendAuditInfo1, err := backendKM.DeserializeAuditInfo(t.Context(), backendID1.AuditInfo)
	require.NoError(t, err)
	nymEID1 := backendAuditInfo1.EidNymAuditData.Nym.Bytes()

	// create second key manager with different config
	config2, err := crypto.NewConfig(configPath + "2")
	require.NoError(t, err)
	keyStore2, err := crypto.NewKeyStore(curveID, kvs2.Keystore(kvs))
	require.NoError(t, err)
	cryptoProvider2, err := crypto.NewBCCSP(keyStore2, curveID)
	require.NoError(t, err)
	backendKM2, err := idemix.NewKeyManager(config2, types.EidNymRhNym, cryptoProvider2)
	require.NoError(t, err)

	// get second identity
	backendID2, err := backendKM2.Identity(t.Context(), nil)
	require.NoError(t, err)
	backendAuditInfo2, err := backendKM2.DeserializeAuditInfo(t.Context(), backendID2.AuditInfo)
	require.NoError(t, err)
	nymEID2 := backendAuditInfo2.EidNymAuditData.Nym.Bytes()

	// create signers
	signer1 := &Signer{Creator: backendID1.Identity, Signer: backendID1.Signer}
	signer2 := &Signer{Creator: backendID2.Identity, Signer: backendID2.Signer}

	// create verifiers
	verifier1 := &Verifier{NymEID: nymEID1, Backed: backendDeserializer}
	verifier2 := &Verifier{NymEID: nymEID2, Backed: backendDeserializer}

	// sign with signer1
	msg := []byte("test message")
	sigma1, err := signer1.Sign(msg)
	require.NoError(t, err)

	// verify with verifier1 should succeed
	err = verifier1.Verify(msg, sigma1)
	require.NoError(t, err)

	// verify with verifier2 should fail (different nym)
	err = verifier2.Verify(msg, sigma1)
	require.Error(t, err)

	// sign with signer2
	sigma2, err := signer2.Sign(msg)
	require.NoError(t, err)

	// verify with verifier2 should succeed
	err = verifier2.Verify(msg, sigma2)
	require.NoError(t, err)

	// verify with verifier1 should fail (different nym)
	err = verifier1.Verify(msg, sigma2)
	require.Error(t, err)
}
