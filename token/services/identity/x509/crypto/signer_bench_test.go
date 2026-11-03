/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/csp"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/require"
)

// BenchmarkSKIBasedSignerSign measures signing with a key in a real software keystore.
func BenchmarkSKIBasedSignerSign(b *testing.B) {
	backend, err := kvs.NewInMemory()
	require.NoError(b, err)
	cryptoProvider, err := GetDefaultBCCSP(csp.NewKVSStore(kvs.Keystore(backend)))
	require.NoError(b, err)
	key, err := cryptoProvider.KeyGen(&bccsp.ECDSAKeyGenOpts{})
	require.NoError(b, err)
	signer, err := NewSKIBasedSigner(cryptoProvider, key.SKI(), &ecdsa.PublicKey{})
	require.NoError(b, err)
	digest := sha256.Sum256([]byte("message"))

	b.ReportAllocs()
	for b.Loop() {
		if _, err := signer.Sign(rand.Reader, digest[:], nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkECDSAVerify measures ecdsaVerifier.Verify on a valid signature.
func BenchmarkECDSAVerify(b *testing.B) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(b, err)
	message := make([]byte, 1024)
	_, err = rand.Read(message)
	require.NoError(b, err)
	sigma, err := NewEcdsaSigner(priv).Sign(message)
	require.NoError(b, err)
	verifier := NewECDSAVerifier(&priv.PublicKey)

	b.ReportAllocs()
	for b.Loop() {
		if err := verifier.Verify(message, sigma); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeserializeVerifier measures turning a serialized x509 identity into a verifier.
func BenchmarkDeserializeVerifier(b *testing.B) {
	conf, err := LoadConfig("../testdata/msp", "")
	require.NoError(b, err)
	raw, err := SerializeIdentity(conf)
	require.NoError(b, err)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := DeserializeVerifier(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeserializeSigner measures deserializing an x509 signer plus one signature.
func BenchmarkDeserializeSigner(b *testing.B) {
	backend, err := kvs.NewInMemory()
	require.NoError(b, err)
	keyStore := csp.NewKVSStore(kvs.Keystore(backend))
	conf, err := LoadConfig("../testdata/msp", "")
	require.NoError(b, err)
	sID, err := GetSigningIdentity(conf, nil, keyStore)
	require.NoError(b, err)
	raw, err := sID.Serialize()
	require.NoError(b, err)
	message := []byte("message")

	b.ReportAllocs()
	for b.Loop() {
		signer, err := DeserializeIdentity(raw, nil, keyStore)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := signer.Sign(message); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFullIdentitySign measures signing with a cached x509 signing identity.
func BenchmarkFullIdentitySign(b *testing.B) {
	backend, err := kvs.NewInMemory()
	require.NoError(b, err)
	conf, err := LoadConfig("../testdata/msp", "")
	require.NoError(b, err)
	sID, err := GetSigningIdentity(conf, nil, csp.NewKVSStore(kvs.Keystore(backend)))
	require.NoError(b, err)
	message := []byte("message")

	b.ReportAllocs()
	for b.Loop() {
		if _, err := sID.Sign(message); err != nil {
			b.Fatal(err)
		}
	}
}
