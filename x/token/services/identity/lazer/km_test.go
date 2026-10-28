/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityRoundTrip(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()

	desc, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	w.store.put(desc.Identity, desc.SignerInfo)
	assert.Less(t, len(desc.Identity), 1024, "the identity must stay small, proofs travel with the signature")

	msg := []byte("hello world")
	sigma, err := desc.Signer.Sign(msg)
	require.NoError(t, err)

	// verifier built by a party that only knows the issuer public key
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)
	v, err := des.DeserializeVerifier(ctx, desc.Identity)
	require.NoError(t, err)
	require.NoError(t, v.Verify(msg, sigma))
	require.NoError(t, desc.Verifier.Verify(msg, sigma))
	// every verification checks the presentation again
	require.NoError(t, v.Verify(msg, sigma))

	// wrong message
	require.Error(t, v.Verify([]byte("other"), sigma))
	// corrupted signature
	bad := append([]byte(nil), sigma...)
	bad[len(bad)-3] ^= 1
	require.Error(t, v.Verify(msg, bad))
	require.Error(t, v.Verify(msg, sigma[:len(sigma)-1]))
	require.Error(t, v.Verify(msg, nil))

	// signer rebuilt from the signer info stored under the identity
	signer, err := w.km.DeserializeSigner(ctx, desc.Identity)
	require.NoError(t, err)
	sigma2, err := signer.Sign(msg)
	require.NoError(t, err)
	require.NoError(t, v.Verify(msg, sigma2))
	signer, err = w.km.DeserializeSignerNoProbe(ctx, desc.Identity)
	require.NoError(t, err)
	_, err = signer.Sign(msg)
	require.NoError(t, err)
	si, err := w.km.DeserializeSigningIdentity(ctx, desc.Identity)
	require.NoError(t, err)
	raw, err := si.Serialize()
	require.NoError(t, err)
	assert.Equal(t, []byte(desc.Identity), raw)
}

func TestIdentitiesAreUnlinkable(t *testing.T) {
	w := newTestWallet(t, "alice")
	a, err := w.km.Identity(context.Background(), nil)
	require.NoError(t, err)
	b, err := w.km.Identity(context.Background(), nil)
	require.NoError(t, err)
	ia, err := UnmarshalIdentity(a.Identity)
	require.NoError(t, err)
	ib, err := UnmarshalIdentity(b.Identity)
	require.NoError(t, err)
	assert.NotEqual(t, ia.NymSK, ib.NymSK)
	assert.NotEqual(t, ia.NymEID, ib.NymEID)
	assert.NotEqual(t, ia.NymRH, ib.NymRH)
}

func TestSignatureUnderOtherIdentity(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	a, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	b, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)

	msg := []byte("m")
	sigmaA, err := a.Signer.Sign(msg)
	require.NoError(t, err)
	// a signature of A does not verify under B
	vB, err := des.DeserializeVerifier(ctx, b.Identity)
	require.NoError(t, err)
	require.Error(t, vB.Verify(msg, sigmaA))

	// swapping the nyms of an identity breaks the presentation binding
	ia, _ := UnmarshalIdentity(a.Identity)
	ib, _ := UnmarshalIdentity(b.Identity)
	mixed, err := (&SerializedIdentity{NymSK: ia.NymSK, NymEID: ib.NymEID, NymRH: ia.NymRH}).Bytes()
	require.NoError(t, err)
	vM, err := des.DeserializeVerifier(ctx, mixed)
	require.NoError(t, err)
	require.Error(t, vM.Verify(msg, sigmaA))

	// another issuer
	other := newTestWallet(t, "alice")
	des2, err := NewDeserializer(other.ipk)
	require.NoError(t, err)
	t.Cleanup(des2.Close)
	vA2, err := des2.DeserializeVerifier(ctx, a.Identity)
	require.NoError(t, err)
	require.Error(t, vA2.Verify(msg, sigmaA))
}

func TestAudit(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	desc, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)

	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)

	ai, err := des.DeserializeAuditInfo(ctx, desc.Identity, desc.AuditInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", ai.EnrollmentID())
	assert.Equal(t, "rh-alice", ai.RevocationHandle())
	require.NoError(t, des.MatchIdentity(ctx, desc.Identity, desc.AuditInfo))
	matcher, err := des.GetAuditInfoMatcher(ctx, desc.Identity, desc.AuditInfo)
	require.NoError(t, err)
	require.NoError(t, matcher.Match(ctx, desc.Identity))
	info, err := des.Info(ctx, desc.Identity, desc.AuditInfo)
	require.NoError(t, err)
	assert.Contains(t, info, "alice")

	// another identity of the same wallet does not match
	other, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	require.Error(t, des.MatchIdentity(ctx, other.Identity, desc.AuditInfo))
	// another user's audit info does not match
	w2 := newTestWallet(t, "bob")
	d2, err := w2.km.Identity(ctx, nil)
	require.NoError(t, err)
	require.Error(t, des.MatchIdentity(ctx, desc.Identity, d2.AuditInfo))
	// a lying EID or RH, or the presentation of another identity, does not match
	for name, tamper := range map[string]func(*AuditInfo){
		"eid":  func(a *AuditInfo) { a.EID = "mallory" },
		"rh":   func(a *AuditInfo) { a.RH = "other" },
		"pres": func(a *AuditInfo) { o, _ := DeserializeAuditInfo(other.AuditInfo); a.Pres = o.Pres },
	} {
		forged, err := DeserializeAuditInfo(desc.AuditInfo)
		require.NoError(t, err)
		tamper(forged)
		raw, err := forged.Bytes()
		require.NoError(t, err)
		require.Error(t, des.MatchIdentity(ctx, desc.Identity, raw), name)
		_, err = des.DeserializeAuditInfo(ctx, desc.Identity, raw)
		require.Error(t, err, name)
	}
	// the deserializer checks the audit info against the identity, which is required
	_, err = des.DeserializeAuditInfo(ctx, other.Identity, desc.AuditInfo)
	require.Error(t, err)
	_, err = des.DeserializeAuditInfo(ctx, nil, desc.AuditInfo)
	require.Error(t, err)
	// the audit info deserializer without ipk only reads EID and RH
	plain, err := (&AuditInfoDeserializer{}).DeserializeAuditInfo(ctx, desc.Identity, desc.AuditInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", plain.EnrollmentID())
	_, isMatcher := plain.(interface {
		Match(context.Context, []byte) error
	})
	assert.False(t, isMatcher, "an audit info without issuer key must not be usable as a matcher")

	// the auditor cannot sign: signatures need the nymSK opening
	_, err = DeserializeAuditInfo(append(append([]byte(nil), desc.AuditInfo...), ' ', '{'))
	require.Error(t, err)
}

func TestIdentityWithReferenceAuditInfo(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	first, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	second, err := w.km.Identity(ctx, first.AuditInfo)
	require.NoError(t, err)

	i1, _ := UnmarshalIdentity(first.Identity)
	i2, _ := UnmarshalIdentity(second.Identity)
	assert.Equal(t, i1.NymEID, i2.NymEID, "nymEID is reused")
	assert.Equal(t, i1.NymRH, i2.NymRH, "nymRH is reused")
	assert.NotEqual(t, i1.NymSK, i2.NymSK, "nymSK is fresh")

	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)
	require.NoError(t, des.MatchIdentity(ctx, second.Identity, second.AuditInfo))
	// the audit info of an identity carries the presentation of that identity:
	// the reference audit info has the same openings, but not the presentation
	ref, err := DeserializeAuditInfo(first.AuditInfo)
	require.NoError(t, err)
	require.NoError(t, ref.matchOpenings(i2))
	require.Error(t, des.MatchIdentity(ctx, second.Identity, first.AuditInfo))
	sigma, err := second.Signer.Sign([]byte("m"))
	require.NoError(t, err)
	v, _ := des.DeserializeVerifier(ctx, second.Identity)
	require.NoError(t, v.Verify([]byte("m"), sigma))

	// audit info of another wallet is refused
	w2 := newTestWallet(t, "bob")
	d2, err := w2.km.Identity(ctx, nil)
	require.NoError(t, err)
	_, err = w.km.Identity(ctx, d2.AuditInfo)
	require.Error(t, err)
}

func TestSignerOfAnotherWalletIsRefused(t *testing.T) {
	w := newTestWallet(t, "alice")
	w2 := newTestWallet(t, "bob")
	ctx := context.Background()
	d, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	// bob's key manager is given alice's signer info
	w2.store.put(d.Identity, d.SignerInfo)
	_, err = w2.km.DeserializeSigner(ctx, d.Identity)
	require.Error(t, err)
	// no signer info at all
	_, err = w.km.DeserializeSigner(ctx, d.Identity)
	require.Error(t, err)
}

func TestRemoteKeyManager(t *testing.T) {
	w := newTestWallet(t, "alice")
	conf := &Config{Version: ProtocolVersionV1, Name: "remote", IPK: w.ipk}
	km, err := NewKeyManager(conf, &memKeystore{}, &memIdentityStore{})
	require.NoError(t, err)
	t.Cleanup(km.Close)
	assert.True(t, km.IsRemote())
	assert.True(t, km.Anonymous())
	assert.Equal(t, IdentityType, km.IdentityType())
	_, err = km.Identity(context.Background(), nil)
	require.Error(t, err)
	_, err = km.DeserializeSigner(context.Background(), []byte("x"))
	require.Error(t, err)

	// it still verifies
	d, err := w.km.Identity(context.Background(), nil)
	require.NoError(t, err)
	sigma, err := d.Signer.Sign([]byte("m"))
	require.NoError(t, err)
	v, err := km.DeserializeVerifier(context.Background(), d.Identity)
	require.NoError(t, err)
	require.NoError(t, v.Verify([]byte("m"), sigma))
}

func TestInvalidCredentialIsRejected(t *testing.T) {
	w := newTestWallet(t, "alice")
	other := newTestWallet(t, "bob")
	signer := *w.signer
	signer.Cred = other.signer.Cred
	conf := &Config{Version: ProtocolVersionV1, Name: "x", IPK: w.ipk, Signer: &signer}
	_, err := NewKeyManager(conf, &memKeystore{}, &memIdentityStore{})
	require.Error(t, err)

	// attributes that were not issued (wrong EID)
	signer = *w.signer
	signer.EnrollmentID = "mallory"
	conf = &Config{Version: ProtocolVersionV1, Name: "x", IPK: w.ipk, Signer: &signer}
	_, err = NewKeyManager(conf, &memKeystore{}, &memIdentityStore{})
	require.Error(t, err)
}

func TestConcurrentUse(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 4 {
		wg.Go(func() {
			d, err := w.km.Identity(ctx, nil)
			if err != nil {
				errs <- err

				return
			}
			sigma, err := d.Signer.Sign([]byte("m"))
			if err != nil {
				errs <- err

				return
			}
			v, err := des.DeserializeVerifier(ctx, d.Identity)
			if err != nil {
				errs <- err

				return
			}
			errs <- v.Verify([]byte("m"), sigma)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestMalformedInputs(t *testing.T) {
	w := newTestWallet(t, "alice")
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)
	ctx := context.Background()
	for _, raw := range [][]byte{nil, {}, {1, 2, 3}, make([]byte, 100000)} {
		_, err := des.DeserializeVerifier(ctx, raw)
		require.Error(t, err)
		_, err = DeserializeAuditInfo(raw)
		require.Error(t, err)
		_, err = unmarshalSignature(raw)
		require.Error(t, err)
	}
	_, err = NewDeserializer(nil)
	require.Error(t, err)
	_, err = NewDeserializer([]byte("short"))
	require.Error(t, err)

	// unknown field in audit info
	var m map[string]any
	d, _ := w.km.Identity(ctx, nil)
	require.NoError(t, json.Unmarshal(d.AuditInfo, &m))
	m["extra"] = 1
	raw, _ := json.Marshal(m)
	_, err = DeserializeAuditInfo(raw)
	require.Error(t, err)
}
