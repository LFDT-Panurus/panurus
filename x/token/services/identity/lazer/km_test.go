/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"context"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"

	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityRoundTrip(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()

	desc, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	w.store.put(desc.Identity, desc.SignerInfo)
	assert.Len(t, desc.Identity, anoncreds.MaxLenNym(), "the identity is nymEID only, proofs travel with the signature")

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
	assert.NotEqual(t, a.Identity, b.Identity)
	sa := mustSignature(t, a.Signer, []byte("m"))
	sb := mustSignature(t, b.Signer, []byte("m"))
	assert.NotEqual(t, sa.NymSK, sb.NymSK)
	assert.NotEqual(t, sa.NymRH, sb.NymRH)
	assert.NotEqual(t, sa.Pres, sb.Pres)
}

// mustSignature signs msg with s and parses the signature.
func mustSignature(t *testing.T, s interface{ Sign([]byte) ([]byte, error) }, msg []byte) *Signature {
	t.Helper()
	raw, err := s.Sign(msg)
	require.NoError(t, err)
	sig, err := unmarshalSignature(raw)
	require.NoError(t, err)

	return sig
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

	// swapping the nyms or the presentation of two signatures breaks the presentation binding
	vA, err := des.DeserializeVerifier(ctx, a.Identity)
	require.NoError(t, err)
	sa := mustSignature(t, a.Signer, msg)
	sb := mustSignature(t, b.Signer, msg)
	for name, mixed := range map[string]Signature{
		"nymRH": {NymSK: sa.NymSK, NymRH: sb.NymRH, Pres: sa.Pres, Sok: sa.Sok},
		"nymSK": {NymSK: sb.NymSK, NymRH: sa.NymRH, Pres: sa.Pres, Sok: sb.Sok},
		"pres":  {NymSK: sa.NymSK, NymRH: sa.NymRH, Pres: sb.Pres, Sok: sa.Sok},
		"all":   {NymSK: sb.NymSK, NymRH: sb.NymRH, Pres: sb.Pres, Sok: sb.Sok},
	} {
		raw, err := asn1.Marshal(mixed)
		require.NoError(t, err)
		require.Error(t, vA.Verify(msg, raw), name)
	}

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
	o, err := DeserializeAuditInfo(other.AuditInfo)
	require.NoError(t, err)
	for name, tamper := range map[string]func(*AuditInfo){
		"eid":        func(a *AuditInfo) { a.EID = "mallory" },
		"rh":         func(a *AuditInfo) { a.RH = "other" },
		"pres":       func(a *AuditInfo) { a.Pres = o.Pres },
		"nym_sk":     func(a *AuditInfo) { a.NymSK = o.NymSK },
		"nym_rh":     func(a *AuditInfo) { a.NymRH = o.NymRH },
		"opening_rh": func(a *AuditInfo) { a.OpeningRH = o.OpeningRH },
		"eid_proof":  func(a *AuditInfo) { a.EIDProof = o.EIDProof },
		"nym_eid":    func(a *AuditInfo) { a.NymEID = o.NymEID },
		"rh_set":     func(a *AuditInfo) { a.NymRH, a.OpeningRH = o.NymRH, o.OpeningRH },
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

	// the audit info does not reveal the opening of nymEID
	pai, err := DeserializeAuditInfo(desc.AuditInfo)
	require.NoError(t, err)
	op, err := openOpening(w.km.aiKey, pai.auditOpeningAD(), pai.EncOpeningEID)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(desc.AuditInfo, op))
	assert.NotContains(t, string(desc.AuditInfo), base64.StdEncoding.EncodeToString(op))
	assert.False(t, bytes.Contains(pai.EncOpeningEID, op))
	assert.True(t, anoncreds.OpeningVerify(desc.Identity, hashAttr(IdxEID, "alice"), op))

	_, err = DeserializeAuditInfo(append(append([]byte(nil), desc.AuditInfo...), ' ', '{'))
	require.Error(t, err)
}

// TestAuditorCannotSign checks that the holder of an audit info cannot sign
// under its identity, even with a credential for the same enrollment id (which
// the issuer can always make): the opening of nymEID is never revealed.
func TestAuditorCannotSign(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	victim, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)
	v, err := des.DeserializeVerifier(ctx, victim.Identity)
	require.NoError(t, err)

	// a second wallet, same issuer, same enrollment id and revocation handle
	w.addWallet(t, "alice")
	attacker, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	msg := []byte("transfer everything")
	sig := mustSignature(t, attacker.Signer, msg)
	ai, err := DeserializeAuditInfo(victim.AuditInfo)
	require.NoError(t, err)
	for name, forged := range map[string]Signature{
		"attacker signature":         *sig,
		"victim nyms, attacker pres": {NymSK: ai.NymSK, NymRH: ai.NymRH, Pres: sig.Pres, Sok: sig.Sok},
		"victim pres, attacker sok":  {NymSK: ai.NymSK, NymRH: ai.NymRH, Pres: ai.Pres, Sok: sig.Sok},
	} {
		raw, err := asn1.Marshal(forged)
		require.NoError(t, err)
		require.Error(t, v.Verify(msg, raw), name)
	}

	// nor can it make an audit info of its own match the victim's identity,
	// although the victim's enrollment id proof is valid for its enrollment id
	aai, err := DeserializeAuditInfo(attacker.AuditInfo)
	require.NoError(t, err)
	aai.NymEID, aai.EIDProof = ai.NymEID, ai.EIDProof
	raw, err := aai.Bytes()
	require.NoError(t, err)
	require.Error(t, des.MatchIdentity(ctx, victim.Identity, raw))
	require.Error(t, des.MatchIdentity(ctx, victim.Identity, attacker.AuditInfo))
}

func TestIdentityWithReferenceAuditInfo(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	first, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	second, err := w.km.Identity(ctx, first.AuditInfo)
	require.NoError(t, err)

	assert.Equal(t, first.Identity, second.Identity, "nymEID, the identity, is reused")
	s1 := mustSignature(t, first.Signer, []byte("m"))
	s2 := mustSignature(t, second.Signer, []byte("m"))
	assert.Equal(t, s1.NymRH, s2.NymRH, "nymRH is reused")
	assert.NotEqual(t, s1.NymSK, s2.NymSK, "nymSK is fresh")
	assert.NotEqual(t, s1.Pres, s2.Pres, "the presentation is fresh")

	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)
	// the identity is the same: both audit infos match it, whichever the stores keep,
	// and the signatures of both descriptors verify under it
	require.NoError(t, des.MatchIdentity(ctx, second.Identity, second.AuditInfo))
	require.NoError(t, des.MatchIdentity(ctx, second.Identity, first.AuditInfo))
	v, err := des.DeserializeVerifier(ctx, second.Identity)
	require.NoError(t, err)
	for _, d := range []*idriver.IdentityDescriptor{first, second} {
		sigma, err := d.Signer.Sign([]byte("m"))
		require.NoError(t, err)
		require.NoError(t, v.Verify([]byte("m"), sigma))
	}
	// the signer info of either descriptor rebuilds a signer
	for _, d := range []*idriver.IdentityDescriptor{first, second} {
		w.store.put(d.Identity, d.SignerInfo)
		s, err := w.km.DeserializeSigner(ctx, d.Identity)
		require.NoError(t, err)
		sigma, err := s.Sign([]byte("m"))
		require.NoError(t, err)
		require.NoError(t, v.Verify([]byte("m"), sigma))
	}
	// a third generation from the second audit info is the same identity again
	third, err := w.km.Identity(ctx, second.AuditInfo)
	require.NoError(t, err)
	assert.Equal(t, first.Identity, third.Identity)

	// audit info of another wallet is refused
	w2 := newTestWallet(t, "bob")
	d2, err := w2.km.Identity(ctx, nil)
	require.NoError(t, err)
	_, err = w.km.Identity(ctx, d2.AuditInfo)
	require.Error(t, err)
	// an audit info of this wallet whose encrypted opening, or RH opening, comes from another identity
	fresh, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	fai, err := DeserializeAuditInfo(fresh.AuditInfo)
	require.NoError(t, err)
	for name, tamper := range map[string]func(*AuditInfo){
		"enc_opening_eid": func(a *AuditInfo) { a.EncOpeningEID = fai.EncOpeningEID },
		"nym_rh":          func(a *AuditInfo) { a.NymRH = fai.NymRH },
		"opening_rh":      func(a *AuditInfo) { a.OpeningRH = fai.OpeningRH },
	} {
		ai, err := DeserializeAuditInfo(first.AuditInfo)
		require.NoError(t, err)
		tamper(ai)
		raw, err := ai.Bytes()
		require.NoError(t, err)
		_, err = w.km.Identity(ctx, raw)
		require.Error(t, err, name)
	}
	// also of another wallet with the same enrollment id and revocation handle:
	// the opening of nymEID is encrypted for the wallet that made it
	w.addWallet(t, "alice")
	_, err = w.km.Identity(ctx, first.AuditInfo)
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

// TestRevocationHandleIsBoundPerSignature checks that the revocation handle
// travels with each signature and is attested by that signature's own
// presentation, while an audit info attests the RH of the presentation it was
// made with only.
//
// All the proofs are honest. The last part is a user who legitimately holds
// two credentials of the issuer with the same enrollment id and different
// revocation handles, and the opening of a nymEID made with the first one: it
// signs under that nymEID with the second RH, and the signature verifies.
// Only comparing nymRH of the signature with nym_rh of the audit info tells.
func TestRevocationHandleIsBoundPerSignature(t *testing.T) {
	ctx := t.Context()
	w := newTestWallet(t, "alice")
	km1 := w.km
	d, err := km1.Identity(ctx, nil)
	require.NoError(t, err)
	ai, err := DeserializeAuditInfo(d.AuditInfo)
	require.NoError(t, err)
	rh1 := hashAttr(IdxRH, "rh-alice")

	// the audit info attests the RH of its own presentation
	require.NoError(t, km1.pv.verifyPres(ai.NymSK, d.Identity, ai.NymRH, ai.Pres))
	assert.True(t, anoncreds.OpeningVerify(ai.NymRH, rh1, ai.OpeningRH))

	// each signature carries nymRH, verified with its own presentation
	for _, msg := range []string{"m1", "m2"} {
		sigma, err := d.Signer.Sign([]byte(msg))
		require.NoError(t, err)
		require.NoError(t, d.Verifier.Verify([]byte(msg), sigma))
		sig, err := unmarshalSignature(sigma)
		require.NoError(t, err)
		require.NoError(t, km1.pv.verifyPres(sig.NymSK, d.Identity, sig.NymRH, sig.Pres), msg)
		// signed with the pseudonym of the audit info: same presentation, same RH
		assert.Equal(t, ai.NymRH, sig.NymRH, msg)
		assert.Equal(t, ai.Pres, sig.Pres, msg)
	}

	// a second credential with the same enrollment id and another RH
	signer2, err := IssueSignerConfig(w.isk, w.ipk, "org1", "member", "alice", "rh-alice-2")
	require.NoError(t, err)
	km2, err := NewKeyManager(&Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: signer2}, &memKeystore{}, &memIdentityStore{})
	require.NoError(t, err)
	t.Cleanup(km2.Close)
	rh2 := km2.attrs[IdxRH]
	require.NotEqual(t, rh1, rh2)

	// the holder of the opening of nymEID and of the second credential
	openingEID, err := openOpening(km1.aiKey, ai.auditOpeningAD(), ai.EncOpeningEID)
	require.NoError(t, err)
	comEID, err := anoncreds.CommitWithOpening(km2.attrs[IdxEID], openingEID)
	require.NoError(t, err)
	defer comEID.Clear()
	require.Equal(t, []byte(d.Identity), comEID.Nym, "same enrollment id, same opening: the same nymEID")
	comSK, err := anoncreds.Commit(km2.usk)
	require.NoError(t, err)
	defer comSK.Clear()
	comRH, err := anoncreds.Commit(rh2)
	require.NoError(t, err)
	defer comRH.Clear()
	pres, err := km2.present(ctx, []*anoncreds.Commitment{comSK, comEID, comRH})
	require.NoError(t, err)
	msg := []byte("m3")
	sok, err := comSK.SokProve(signatureContext(msg), km2.usk)
	require.NoError(t, err)
	sigma, err := asn1.Marshal(Signature{NymSK: comSK.Nym, NymRH: comRH.Nym, Pres: pres, Sok: sok})
	require.NoError(t, err)

	// it verifies under the identity of the audit info, with the other RH
	require.NoError(t, d.Verifier.Verify(msg, sigma))
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)
	v, err := des.DeserializeVerifier(ctx, d.Identity)
	require.NoError(t, err)
	require.NoError(t, v.Verify(msg, sigma))
	// the audit info still matches the identity, and says nothing about this RH
	require.NoError(t, des.MatchIdentity(ctx, d.Identity, d.AuditInfo))
	sig, err := unmarshalSignature(sigma)
	require.NoError(t, err)
	assert.NotEqual(t, ai.NymRH, sig.NymRH, "the auditor's check: nymRH of the signature against nym_rh of the audit info")
	assert.False(t, anoncreds.OpeningVerify(sig.NymRH, rh1, ai.OpeningRH))
}
