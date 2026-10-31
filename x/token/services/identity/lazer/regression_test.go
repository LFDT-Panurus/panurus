/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests from the bug-hunting campaign against this module.

func randomAttr(t *testing.T) []byte {
	t.Helper()
	a := make([]byte, anoncreds.AttrLen)
	_, err := rand.Read(a)
	require.NoError(t, err)

	return a
}

// TestRegressionCommitmentSizesAreFixed checks that nyms and openings have a
// constant length: checkIdentity and DeserializeAuditInfo rely on it.
func TestRegressionCommitmentSizesAreFixed(t *testing.T) {
	for range 200 {
		c, err := anoncreds.Commit(randomAttr(t))
		require.NoError(t, err)
		require.Len(t, c.Nym, anoncreds.MaxLenNym())
		require.Len(t, mustOpening(t, c), anoncreds.MaxLenOpening())
		c.Clear()
	}
}

// TestRegressionIdentitySizeIsFixed checks that identities of different
// wallets have the same length, so the length does not identify a wallet.
func TestRegressionIdentitySizeIsFixed(t *testing.T) {
	w := newTestWallet(t, "alice")
	kms := []*KeyManager{w.km}
	w.addWallet(t, "bob-with-a-much-longer-enrollment-id")
	kms = append(kms, w.km)

	size := 0
	for _, km := range kms {
		for range 3 {
			d, err := km.Identity(t.Context(), nil)
			require.NoError(t, err)
			if size == 0 {
				size = len(d.Identity)
			}
			require.Len(t, d.Identity, size)
		}
	}
}

// TestRegressionOpeningNotMalleable checks that flipping any bit of a nym or of
// its opening makes the opening invalid.
func TestRegressionOpeningNotMalleable(t *testing.T) {
	attr := randomAttr(t)
	c, err := anoncreds.Commit(attr)
	require.NoError(t, err)
	defer c.Clear()
	opening := mustOpening(t, c)
	require.True(t, anoncreds.OpeningVerify(c.Nym, attr, opening))

	for i := range len(c.Nym) * 8 {
		nym := append([]byte(nil), c.Nym...)
		nym[i/8] ^= 1 << (i % 8)
		require.False(t, anoncreds.OpeningVerify(nym, attr, opening), "nym bit %d", i)
	}
	for i := range len(opening) * 8 {
		op := append([]byte(nil), opening...)
		op[i/8] ^= 1 << (i % 8)
		require.False(t, anoncreds.OpeningVerify(c.Nym, attr, op), "opening bit %d", i)
	}
}

// TestRegressionSignatureNotMalleable checks that tampered presentations and
// signatures of knowledge are rejected.
func TestRegressionSignatureNotMalleable(t *testing.T) {
	w := newTestWallet(t, "alice")
	d, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)
	msg := []byte("message")
	raw, err := d.Signer.Sign(msg)
	require.NoError(t, err)
	require.NoError(t, d.Verifier.Verify(msg, raw))
	sig, err := unmarshalSignature(raw)
	require.NoError(t, err)

	reject := func(name string, pres, sok []byte) {
		t.Helper()
		raw, err := asn1.Marshal(Signature{NymSK: sig.NymSK, NymRH: sig.NymRH, Pres: pres, Sok: sok})
		require.NoError(t, err)
		require.Error(t, d.Verifier.Verify(msg, raw), name)
	}
	rejectRaw := func(name string, raw []byte) {
		t.Helper()
		require.Error(t, d.Verifier.Verify(msg, raw), name)
	}
	flip := func(b []byte, pos int, bit uint) []byte {
		out := append([]byte(nil), b...)
		out[pos] ^= 1 << bit

		return out
	}

	for _, pos := range []int{0, 10, len(sig.Pres) / 2, len(sig.Pres) - 2} {
		reject("pres byte flipped", flip(sig.Pres, pos, 0), sig.Sok)
	}
	for _, pos := range []int{0, len(sig.Sok) / 2} {
		reject("sok byte flipped", sig.Pres, flip(sig.Sok, pos, 0))
	}
	// the last byte holds the end of the encoding and padding bits
	for bit := range uint(8) {
		reject("pres last byte flipped", flip(sig.Pres, len(sig.Pres)-1, bit), sig.Sok)
		reject("sok last byte flipped", sig.Pres, flip(sig.Sok, len(sig.Sok)-1, bit))
	}
	reject("pres trailing byte", append(append([]byte(nil), sig.Pres...), 0), sig.Sok)
	reject("sok trailing byte", sig.Pres, append(append([]byte(nil), sig.Sok...), 0))
	random := make([]byte, len(sig.Pres))
	_, err = rand.Read(random)
	require.NoError(t, err)
	reject("random pres", random, sig.Sok)

	// the nyms of the signature
	for _, pos := range []int{0, len(sig.NymSK) / 2, len(sig.NymSK) - 1} {
		for _, nymRH := range []bool{false, true} {
			s := *sig
			if nymRH {
				s.NymRH = flip(s.NymRH, pos, 0)
			} else {
				s.NymSK = flip(s.NymSK, pos, 0)
			}
			raw, err := asn1.Marshal(s)
			require.NoError(t, err)
			rejectRaw("nym byte flipped", raw)
		}
	}
	// encoding/asn1 alone accepts extra elements in a sequence and the nyms in any length
	type extra struct {
		NymSK, NymRH, Pres, Sok, Extra []byte
	}
	raw2, err := asn1.Marshal(extra{sig.NymSK, sig.NymRH, sig.Pres, sig.Sok, []byte{1}})
	require.NoError(t, err)
	rejectRaw("extra element", raw2)
	raw2, err = asn1.Marshal(Signature{NymSK: append(append([]byte(nil), sig.NymSK...), 0), NymRH: sig.NymRH, Pres: sig.Pres, Sok: sig.Sok})
	require.NoError(t, err)
	rejectRaw("long nym", raw2)
	rejectRaw("trailing data", append(append([]byte(nil), raw...), 0))
	// a signature of the former layout {pres, sok}
	type old struct{ Pres, Sok []byte }
	raw2, err = asn1.Marshal(old{sig.Pres, sig.Sok})
	require.NoError(t, err)
	rejectRaw("signature of the former layout", raw2)
}

// TestRegressionCloseConcurrentWithIdentity checks that Close may run while
// identities are generated, signers are rebuilt and signatures are verified.
// Run with -race: IsRemote, Identity, DeserializeSigner and Verify must not
// race with Close, which releases the user state; afterwards the key manager
// is still local, refuses to generate identities or rebuild signers, and
// pools no verifier state.
func TestRegressionCloseConcurrentWithIdentity(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := context.Background()
	d, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	w.store.put(d.Identity, d.SignerInfo)
	sigma, err := d.Signer.Sign([]byte("m"))
	require.NoError(t, err)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			<-start
			for range 2 {
				_ = w.km.IsRemote()
				_, _ = w.km.Identity(ctx, nil)
				_, _ = w.km.DeserializeSigner(ctx, d.Identity)
				_ = d.Verifier.Verify([]byte("m"), sigma)
			}
		})
	}
	close(start)
	w.km.Close()
	wg.Wait()
	w.km.Close() // idempotent

	assert.False(t, w.km.IsRemote(), "a closed local wallet is not a remote one")
	_, err = w.km.Identity(t.Context(), nil)
	require.Error(t, err, "identity generated after Close")
	_, err = w.km.DeserializeSigner(t.Context(), d.Identity)
	require.Error(t, err, "signer rebuilt after Close")
	// verifier states released after Close are not pooled again
	w.km.pv.poolMu.Lock()
	assert.Empty(t, w.km.pv.idle)
	w.km.pv.poolMu.Unlock()
}

// TestRegressionWrappedKeyManagerClose checks Close of the provider's key
// manager while its identity cache provisions identities in the background.
func TestRegressionWrappedKeyManagerClose(t *testing.T) {
	w := newTestWallet(t, "alice")
	raw, err := (&Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: w.signer}).Marshal()
	require.NoError(t, err)
	kmp := NewKeyManagerProvider(w.ipk, &memKeystore{}, w.store, mockConfig{}, 2, true, &disabled.Provider{})
	km, err := kmp.Get(t.Context(), &driver.IdentityConfiguration{ID: "alice", Raw: raw})
	require.NoError(t, err)
	wkm, ok := km.(*WrappedKeyManager)
	require.True(t, ok)
	// starts the provisioning goroutine
	_, err = wkm.Identity(t.Context(), nil)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { _, _ = wkm.Identity(context.Background(), nil) })
	}
	wkm.Close()
	wg.Wait()
	// the cache may still hold pseudonyms provisioned before Close
	for range 3 {
		_, err = wkm.Identity(t.Context(), nil)
		require.Error(t, err, "identity served after Close")
	}
	wkm.Close()
}

// TestRegressionIdentityWithoutCredentialIsRejected builds an identity whose
// nyms commit to the enrollment id and revocation handle of a victim, without
// any credential, and checks that it is not accepted as an identity of the
// victim. Otherwise an auditor accepts a transfer to it as a transfer to the
// victim (with Idemix the identity carries a proof bound to a credential).
func TestRegressionIdentityWithoutCredentialIsRejected(t *testing.T) {
	_, ipk := NewIssuerKey()
	d, err := NewDeserializer(ipk)
	require.NoError(t, err)
	defer d.Close()

	sk, err := anoncreds.Commit(randomAttr(t))
	require.NoError(t, err)
	defer sk.Clear()
	eid, err := anoncreds.Commit(hashAttr(IdxEID, "alice"))
	require.NoError(t, err)
	defer eid.Clear()
	rh, err := anoncreds.Commit(hashAttr(IdxRH, "alice-rh"))
	require.NoError(t, err)
	defer rh.Clear()
	id := eid.Nym
	eidProof, err := eid.EqOpeningProve(eidProofContext(ipk, id, hashAttr(IdxEID, "alice")), hashAttr(IdxEID, "alice"))
	require.NoError(t, err)
	encOpening := make([]byte, gcmNonceLen+anoncreds.MaxLenOpening()+gcmTagLen)
	// the presentation of a real identity of alice does not help
	for _, pres := range [][]byte{make([]byte, 1024), alicePres(t)} {
		ai, err := (&AuditInfo{
			EID: "alice", RH: "alice-rh", NymEID: id, NymSK: sk.Nym, NymRH: rh.Nym, OpeningRH: mustOpening(t, rh),
			EIDProof: eidProof, EncOpeningEID: encOpening, Pres: pres,
		}).Bytes()
		require.NoError(t, err)
		require.NoError(t, d.pv.verifyEID(id, "alice", eidProof), "the enrollment id proof alone is valid")

		_, errVerifier := d.DeserializeVerifier(t.Context(), id)
		errMatch := d.MatchIdentity(t.Context(), id, ai)
		require.True(t, errVerifier != nil || errMatch != nil, "credential-less identity accepted as alice's")
		matcher, err := d.GetAuditInfoMatcher(t.Context(), id, ai)
		require.NoError(t, err)
		require.Error(t, matcher.Match(t.Context(), id))
		_, err = d.DeserializeAuditInfo(t.Context(), id, ai)
		require.Error(t, err)
		_, err = d.Info(t.Context(), id, ai)
		require.Error(t, err)
	}
}

// alicePres returns the presentation of an identity of another wallet.
func alicePres(t *testing.T) []byte {
	t.Helper()
	w := newTestWallet(t, "alice")
	d, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)
	ai, err := DeserializeAuditInfo(d.AuditInfo)
	require.NoError(t, err)

	return ai.Pres
}

// TestRegressionMissingWalletDirIsAnError checks that a wrong wallet path is
// reported, instead of silently yielding a verify-only (remote) wallet.
func TestRegressionMissingWalletDirIsAnError(t *testing.T) {
	_, ipk := NewIssuerKey()
	_, err := NewConfigWithIPK(ipk, filepath.Join(t.TempDir(), "does-not-exist"), true)
	require.Error(t, err)
}

// TestRegressionMalformedSignerConfigIsAnError checks that a corrupt signer
// configuration is reported, instead of the loader falling back to dir/msp and
// silently yielding a remote wallet.
func TestRegressionMalformedSignerConfigIsAnError(t *testing.T) {
	_, ipk := NewIssuerKey()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ConfigDirUser), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigDirUser, ConfigFileSignerFull), []byte("{garbage"), filePerm))
	_, err := NewConfigWithIPK(ipk, dir, true)
	require.Error(t, err)
}

// TestRegressionAuditInfoIsCanonical checks that the strict audit info decoder
// rejects non-canonical encodings: trailing closing delimiters (json.Decoder.More
// does not report them) and duplicate or case-variant keys.
func TestRegressionAuditInfoIsCanonical(t *testing.T) {
	w := newTestWallet(t, "alice")
	d, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)
	_, err = DeserializeAuditInfo(d.AuditInfo)
	require.NoError(t, err)

	for _, suffix := range []string{"}", "]", " }}}"} {
		_, err := DeserializeAuditInfo(append(append([]byte(nil), d.AuditInfo...), suffix...))
		require.Error(t, err, "trailing %q accepted", suffix)
	}
	_, err = DeserializeAuditInfo([]byte(`{"EID":"mallory",` + string(d.AuditInfo[1:])))
	require.Error(t, err, "case-variant duplicate key accepted")
	_, err = DeserializeAuditInfo([]byte(`{"eid":"mallory",` + string(d.AuditInfo[1:])))
	require.Error(t, err, "duplicate key accepted")
	_, err = DeserializeAuditInfo(append([]byte(" "), d.AuditInfo...))
	require.Error(t, err, "non-canonical whitespace accepted")
}

// TestRegressionAuditInfoLengths checks the fixed and bounded lengths of the
// fields of an audit info.
func TestRegressionAuditInfoLengths(t *testing.T) {
	w := newTestWallet(t, "alice")
	d, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)
	resize := func(b []byte, n int) []byte {
		out := make([]byte, n)
		copy(out, b)

		return out
	}
	for name, tamper := range map[string]func(*AuditInfo){
		"nym_eid short":         func(a *AuditInfo) { a.NymEID = a.NymEID[1:] },
		"nym_eid long":          func(a *AuditInfo) { a.NymEID = resize(a.NymEID, len(a.NymEID)+1) },
		"nym_sk short":          func(a *AuditInfo) { a.NymSK = a.NymSK[1:] },
		"nym_sk long":           func(a *AuditInfo) { a.NymSK = resize(a.NymSK, len(a.NymSK)+1) },
		"nym_rh short":          func(a *AuditInfo) { a.NymRH = a.NymRH[1:] },
		"nym_rh long":           func(a *AuditInfo) { a.NymRH = resize(a.NymRH, len(a.NymRH)+1) },
		"opening_rh short":      func(a *AuditInfo) { a.OpeningRH = a.OpeningRH[1:] },
		"opening_rh long":       func(a *AuditInfo) { a.OpeningRH = resize(a.OpeningRH, len(a.OpeningRH)+1) },
		"enc_opening_eid short": func(a *AuditInfo) { a.EncOpeningEID = a.EncOpeningEID[1:] },
		"enc_opening_eid long":  func(a *AuditInfo) { a.EncOpeningEID = resize(a.EncOpeningEID, len(a.EncOpeningEID)+1) },
		"eid_proof empty":       func(a *AuditInfo) { a.EIDProof = nil },
		"eid_proof too long":    func(a *AuditInfo) { a.EIDProof = resize(a.EIDProof, anoncreds.MaxLenSok()+1) },
		"pres empty":            func(a *AuditInfo) { a.Pres = nil },
		"pres too long":         func(a *AuditInfo) { a.Pres = resize(a.Pres, anoncreds.MaxLenPres()+1) },
	} {
		ai, err := DeserializeAuditInfo(d.AuditInfo)
		require.NoError(t, err)
		tamper(ai)
		raw, err := ai.Bytes()
		require.NoError(t, err)
		_, err = DeserializeAuditInfo(raw)
		require.Error(t, err, name)
	}
}

// TestRegressionStrictDecoders checks the signer info and configuration
// decoders against the same non-canonical encodings.
func TestRegressionStrictDecoders(t *testing.T) {
	w := newTestWallet(t, "alice")
	d, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)
	_, err = decodeSignerInfo(d.SignerInfo)
	require.NoError(t, err)
	conf, err := (&Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk}).Marshal()
	require.NoError(t, err)
	_, err = NewConfigFromRaw(w.ipk, conf)
	require.NoError(t, err)

	mutations := map[string]func([]byte) []byte{
		"trailing }":     func(b []byte) []byte { return append(append([]byte(nil), b...), '}') },
		"trailing ]":     func(b []byte) []byte { return append(append([]byte(nil), b...), ']') },
		"trailing value": func(b []byte) []byte { return append(append([]byte(nil), b...), " {}"...) },
		"case variant":   func(b []byte) []byte { return append([]byte(`{"VERSION":1,`), b[1:]...) },
		"duplicate":      func(b []byte) []byte { return append([]byte(`{"version":1,`), b[1:]...) },
	}
	for name, mutate := range mutations {
		_, err := decodeSignerInfo(mutate(d.SignerInfo))
		require.Error(t, err, "signer info: %s", name)
		_, err = NewConfigFromRaw(w.ipk, mutate(conf))
		require.Error(t, err, "config: %s", name)
	}
	// the signer config on disk is strict as well
	dir := t.TempDir()
	signer, err := json.Marshal(w.signer) //nolint:gosec // G117: test material
	require.NoError(t, err)
	for name, mutate := range mutations {
		if name == "case variant" || name == "duplicate" {
			// SignerConfig has no version field, use one of its own
			mutate = func(b []byte) []byte { return append([]byte(`{"OU":"x",`), b[1:]...) }
		}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ConfigDirUser), dirPerm))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigDirUser, ConfigFileSignerFull), mutate(signer), filePerm))
		_, err := NewConfigWithIPK(w.ipk, dir, true)
		require.Error(t, err, "signer config: %s", name)
	}
}

// TestRegressionWalletLayout checks the wallet directories NewConfigWithIPK
// accepts: dir/user and dir/msp/user, with or without signer configuration.
func TestRegressionWalletLayout(t *testing.T) {
	isk, ipk := NewIssuerKey()
	signer, err := IssueSignerConfig(isk, ipk, "org1", "member", "alice", "rh-alice")
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		setup      func(dir string)
		remote     bool
		wantErr    bool
		verifyOnly bool
	}{
		{name: "full in user", setup: func(dir string) { require.NoError(t, WriteSignerConfig(dir, signer, true)) }},
		{name: "full in msp/user", setup: func(dir string) {
			require.NoError(t, WriteSignerConfig(filepath.Join(dir, ExtraPathElement), signer, true))
		}},
		{name: "empty user dir is remote", setup: func(dir string) {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ConfigDirUser), dirPerm))
		}, remote: true},
		{name: "verify-only wallet", setup: func(dir string) {
			require.NoError(t, WriteSignerConfig(dir, signer, true))
		}, remote: true, verifyOnly: true},
		{name: "no user dir", setup: func(dir string) {
			require.NoError(t, WriteIssuerKeys(dir, nil, ipk))
		}, wantErr: true},
		{name: "user is a file", setup: func(dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigDirUser), []byte("x"), filePerm))
		}, wantErr: true},
		{name: "malformed SignerConfig", setup: func(dir string) {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ConfigDirUser), dirPerm))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigDirUser, ConfigFileSigner), []byte("{}}"), filePerm))
		}, wantErr: true, verifyOnly: true},
		{name: "malformed full in user does not fall back to msp", setup: func(dir string) {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ConfigDirUser), dirPerm))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigDirUser, ConfigFileSignerFull), []byte("{"), filePerm))
			require.NoError(t, WriteSignerConfig(filepath.Join(dir, ExtraPathElement), signer, true))
		}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			conf, err := NewConfigWithIPK(ipk, dir, !tc.verifyOnly)
			if tc.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.remote, conf.Signer == nil)
		})
	}
}

// TestRegressionSignerInfoIsEncrypted checks that the signer info kept by the
// identity store, which is not a secret store, does not hold the opening of
// nymSK (with nymSK it reveals usk), and that a reader of the identity store
// without the wallet secret cannot rebuild a signer.
func TestRegressionSignerInfoIsEncrypted(t *testing.T) {
	w := newTestWallet(t, "alice")
	ctx := t.Context()
	d, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	w.store.put(d.Identity, d.SignerInfo)
	opening := d.Signer.(*Signer).opening
	require.Len(t, opening, anoncreds.MaxLenOpening())

	// neither the raw nor the base64 opening is in the stored signer info
	assert.False(t, bytes.Contains(d.SignerInfo, opening))
	assert.NotContains(t, string(d.SignerInfo), base64.StdEncoding.EncodeToString(opening))
	info, err := decodeSignerInfo(d.SignerInfo)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(info.EncOpeningSK, opening))
	for i := 0; i+16 <= len(opening); i += 16 {
		assert.False(t, bytes.Contains(info.EncOpeningSK, opening[i:i+16]), "opening block %d in the ciphertext", i/16)
	}

	// the identity store reader knows the identity and the issuer public key,
	// and may even hold the credential and the public attributes: not the secret
	readerConf := &Config{Version: ProtocolVersionV1, Name: "reader", IPK: w.ipk}
	reader, err := NewKeyManager(readerConf, &memKeystore{}, w.store)
	require.NoError(t, err)
	t.Cleanup(reader.Close)
	_, err = reader.DeserializeSigner(ctx, d.Identity)
	require.Error(t, err)
	// another wallet secret does not decrypt
	other := newTestWallet(t, "alice")
	ad := signerInfoAD(d.Identity, info.NymRH, info.Pres)
	_, err = openOpening(other.km.siKey, ad, info.EncOpeningSK)
	require.Error(t, err)
	// the key binds to the identity: the signer info of another identity is refused
	d2, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	w.store.put(d2.Identity, d.SignerInfo)
	_, err = w.km.DeserializeSigner(ctx, d2.Identity)
	require.Error(t, err)
	// tampering with the ciphertext is detected
	tampered := *info
	tampered.EncOpeningSK = append([]byte(nil), info.EncOpeningSK...)
	tampered.EncOpeningSK[len(tampered.EncOpeningSK)-1] ^= 1
	raw, err := json.Marshal(&tampered)
	require.NoError(t, err)
	w.store.put(d.Identity, raw)
	_, err = w.km.DeserializeSigner(ctx, d.Identity)
	require.Error(t, err)
	// the legitimate wallet rebuilds the signer, also after a restart
	w.store.put(d.Identity, d.SignerInfo)
	conf := &Config{Version: ProtocolVersionV1, Name: "alice", IPK: w.ipk, Signer: &SignerConfig{
		Cred: w.signer.Cred, OU: w.signer.OU, Role: w.signer.Role, EnrollmentID: w.signer.EnrollmentID,
		RevocationHandle: w.signer.RevocationHandle, Ski: w.signer.Ski,
	}}
	restarted, err := NewKeyManager(conf, w.keystore, w.store)
	require.NoError(t, err)
	t.Cleanup(restarted.Close)
	s, err := restarted.DeserializeSigner(ctx, d.Identity)
	require.NoError(t, err)
	sigma, err := s.Sign([]byte("m"))
	require.NoError(t, err)
	require.NoError(t, d.Verifier.Verify([]byte("m"), sigma))
	// a signer info of an older, unsupported version (plaintext opening) is refused
	legacy, err := json.Marshal(map[string][]byte{"opening_sk": opening, "pres": info.Pres})
	require.NoError(t, err)
	w.store.put(d.Identity, legacy)
	_, err = w.km.DeserializeSigner(ctx, d.Identity)
	require.Error(t, err)
	v1, err := json.Marshal(map[string]any{"version": 1, "enc_opening_sk": info.EncOpeningSK, "pres": info.Pres})
	require.NoError(t, err)
	w.store.put(d.Identity, v1)
	_, err = w.km.DeserializeSigner(ctx, d.Identity)
	require.Error(t, err)
	// the encrypted opening is bound to the nymRH and the presentation of the signer info
	d3, err := w.km.Identity(ctx, nil)
	require.NoError(t, err)
	info3, err := decodeSignerInfo(d3.SignerInfo)
	require.NoError(t, err)
	for name, tamper := range map[string]func(*SignerInfo){
		"nym_rh": func(i *SignerInfo) { i.NymRH = info3.NymRH },
		"pres":   func(i *SignerInfo) { i.Pres = info3.Pres },
	} {
		forged := *info
		tamper(&forged)
		raw, err := json.Marshal(&forged)
		require.NoError(t, err)
		w.store.put(d.Identity, raw)
		_, err = w.km.DeserializeSigner(ctx, d.Identity)
		require.Error(t, err, name)
	}
}

// TestRegressionOpeningRevealsAttribute documents why openings must not
// leak: nym = Acom*(enc(attr), r) is linear, so whoever has a nym and its
// opening solves for attr. The column of Acom that multiplies the attribute is
// public and obtained here with CommitWithOpening. Applied to nymSK this
// yields usk (whence the encryption of the signer info), applied to the audit
// info it yields the RH hash (by design, for the auditor). The opening of
// nymEID, the identity, is never in the clear (see TestAuditorCannotSign).
func TestRegressionOpeningRevealsAttribute(t *testing.T) {
	w := newTestWallet(t, "alice")
	d, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)
	ai, err := DeserializeAuditInfo(d.AuditInfo)
	require.NoError(t, err)
	openingEID, err := openOpening(w.km.aiKey, ai.auditOpeningAD(), ai.EncOpeningEID)
	require.NoError(t, err)

	for _, tc := range []struct {
		name         string
		nym, opening []byte
		want         []byte
	}{
		{"usk from nymSK", ai.NymSK, d.Signer.(*Signer).opening, w.km.usk},
		{"EID from nymEID (wallet only)", d.Identity, openingEID, hashAttr(IdxEID, "alice")},
		{"RH from nymRH", ai.NymRH, ai.OpeningRH, hashAttr(IdxRH, "rh-alice")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, solveAttribute(t, tc.nym, tc.opening))
		})
	}
}

// nymCoeffs decodes a nym: CBDC_COM_ROWS polynomials of degree 64 over
// Z_12289, 14 bits per coefficient, least significant bit first.
func nymCoeffs(t *testing.T, nym []byte) []int64 {
	t.Helper()
	const bits = 14
	n := (len(nym) - 1) * 8 / bits
	out := make([]int64, n)
	for i := range n {
		for b := range bits {
			pos := i*bits + b
			out[i] |= int64(nym[pos/8]>>(pos%8)&1) << b
		}
	}

	return out
}

// solveAttribute recovers the attribute committed to by nym from its opening.
func solveAttribute(t *testing.T, nym, opening []byte) []byte {
	t.Helper()
	m := solveEncoding(t, nym, opening)
	// m_i = redc16(nibble): the nibble is m_i mod 16
	attr := make([]byte, anoncreds.AttrLen)
	for j := range attr {
		hi, lo := uint8(((m[2*j]%16)+16)%16), uint8(((m[2*j+1]%16)+16)%16)
		attr[j] = hi<<4 | lo
	}

	return attr
}

// solveEncoding recovers the encoding m (64 centered coefficients) of the
// attribute committed to by nym from its opening: nym = a0*m + Ar*r is linear
// in m.
func solveEncoding(t *testing.T, nym, opening []byte) []int64 {
	t.Helper()
	const q, deg = 12289, 64
	commit := func(attr []byte) []int64 {
		c, err := anoncreds.CommitWithOpening(attr, opening)
		require.NoError(t, err)
		defer c.Clear()

		return nymCoeffs(t, c.Nym)
	}
	zero := make([]byte, anoncreds.AttrLen)
	// Ar*r, and Ar*r + a0*1: the attribute byte 0x10 encodes the polynomial 1
	base := commit(zero)
	one := append([]byte{0x10}, zero[1:]...)
	a0 := commit(one)
	target := nymCoeffs(t, nym)
	rows := len(base) / deg
	// rows*deg linear equations a0_k * m = target_k - base_k mod q in the 64
	// coefficients of m, negacyclic convolution
	mat := make([][]int64, 0, rows*deg)
	for k := range rows {
		a := make([]int64, deg)
		for i := range deg {
			a[i] = (a0[k*deg+i] - base[k*deg+i] + q) % q
		}
		for i := range deg {
			row := make([]int64, deg+1)
			for j := range deg {
				if i >= j {
					row[j] = a[i-j]
				} else {
					row[j] = (q - a[i-j+deg]) % q
				}
			}
			row[deg] = (target[k*deg+i] - base[k*deg+i] + q) % q
			mat = append(mat, row)
		}
	}

	return gaussMod(t, mat, deg, q)
}

// gaussMod solves the overdetermined system mat (n unknowns, last column the
// constants) modulo the prime q and returns centered solutions.
func gaussMod(t *testing.T, mat [][]int64, n int, q int64) []int64 {
	t.Helper()
	inv := func(a int64) int64 {
		r, e := int64(1), q-2
		for b := a % q; e > 0; e >>= 1 {
			if e&1 == 1 {
				r = r * b % q
			}
			b = b * b % q
		}

		return r
	}
	row := 0
	for col := range n {
		p := -1
		for r := row; r < len(mat); r++ {
			if mat[r][col] != 0 {
				p = r

				break
			}
		}
		require.NotEqual(t, -1, p, "singular system")
		mat[row], mat[p] = mat[p], mat[row]
		f := inv(mat[row][col])
		for c := col; c <= n; c++ {
			mat[row][c] = mat[row][c] * f % q
		}
		for r := range mat {
			if r == row || mat[r][col] == 0 {
				continue
			}
			g := mat[r][col]
			for c := col; c <= n; c++ {
				mat[r][c] = ((mat[r][c]-g*mat[row][c])%q + q) % q
			}
		}
		row++
	}
	out := make([]int64, n)
	for i := range n {
		v := mat[i][n]
		if v > q/2 {
			v -= q
		}
		out[i] = v
	}
	// the remaining equations hold: the solution is the committed value
	for r := n; r < len(mat); r++ {
		require.Zero(t, mat[r][n], "inconsistent system")
	}

	return out
}

// TestRegressionIssueSignerConfigIsBounded checks that IssueSignerConfig
// gives up when the public attributes alone exceed the norm bound of the
// issuance proof, instead of resampling the user secret forever.
func TestRegressionIssueSignerConfigIsBounded(t *testing.T) {
	isk, ipk := NewIssuerKey()
	// values found by search whose encodings have a large norm: together they
	// exceed the bound (91^2) whatever the user secret
	_, err := IssueSignerConfig(isk, ipk, "v13070", "v54243", "v193246", "v49325")
	require.ErrorContains(t, err, "norm bound")

	// the cap logic
	calls := 0
	never := func([][]byte) (bool, error) {
		calls++

		return false, nil
	}
	_, err = sampleAttributes("ou", "role", "eid", "rh", 7, never)
	require.Error(t, err)
	assert.Equal(t, 7, calls)
	third := 0
	attrs, err := sampleAttributes("ou", "role", "eid", "rh", 7, func([][]byte) (bool, error) {
		third++

		return third == 3, nil
	})
	require.NoError(t, err)
	assert.Len(t, attrs, anoncreds.NAttrs)
	assert.Equal(t, 3, third)
}

// mustOpening returns the opening of c.
func mustOpening(t *testing.T, c *anoncreds.Commitment) []byte {
	t.Helper()
	opening, err := c.Opening()
	require.NoError(t, err)

	return opening
}

// TestSizesWithinStatedBounds checks the sizes stated in the documentation:
// identity and nym 225 B, opening 1121 B, user state 1793 B, credential at most
// 3000 B, signature about 53.4 KB, audit info about 75 KB and signer info about
// 51 KB. The fixed sizes are exact; for the others the bounds are the maxima
// the decoders accept and a band around the observed size, which varies with
// the coins of the proofs by some tens of bytes.
func TestSizesWithinStatedBounds(t *testing.T) {
	// the encodings of fixed length, and the maxima
	assert.Equal(t, 225, anoncreds.MaxLenNym())
	assert.Equal(t, 1121, anoncreds.MaxLenOpening())
	assert.Equal(t, 1793, anoncreds.MaxLenUserState())
	assert.Equal(t, 3000, anoncreds.MaxLenCred())
	assert.Equal(t, anoncreds.MaxLenPres()+anoncreds.MaxLenSok()+2*anoncreds.MaxLenNym()+64, maxSignatureLen)

	w := newTestWallet(t, "alice")
	for range 3 {
		w.addWallet(t, "alice")
		assert.LessOrEqual(t, len(w.signer.Cred), anoncreds.MaxLenCred(), "credential")
		assert.Len(t, w.signer.Sk, anoncreds.AttrLen+anoncreds.MaxLenUserState(), "wallet secret: usk and user state")

		d, err := w.km.Identity(t.Context(), nil)
		require.NoError(t, err)
		assert.Len(t, d.Identity, 225, "identity")
		sigma, err := d.Signer.Sign([]byte("m"))
		require.NoError(t, err)
		sig, err := unmarshalSignature(sigma)
		require.NoError(t, err)
		assert.Len(t, sig.NymSK, 225, "nymSK")
		assert.Len(t, sig.NymRH, 225, "nymRH")
		ai, err := DeserializeAuditInfo(d.AuditInfo)
		require.NoError(t, err)
		assert.Len(t, ai.OpeningRH, 1121, "opening of nymRH")

		for _, tc := range []struct {
			name     string
			n        int
			min, max int
		}{
			// "about 53.4 KB", at most what unmarshalSignature accepts
			{"signature", len(sigma), 53_000, min(54_000, maxSignatureLen)},
			// "about 75 KB", at most what DeserializeAuditInfo accepts
			{"audit info", len(d.AuditInfo), 74_000, min(76_000, maxAuditInfoLen)},
			// "about 51 KB", at most what decodeSignerInfo accepts
			{"signer info", len(d.SignerInfo), 50_000, min(52_000, maxSignerInfoLen)},
			{"presentation", len(sig.Pres), 1, anoncreds.MaxLenPres()},
			{"signature of knowledge", len(sig.Sok), 1, anoncreds.MaxLenSok()},
		} {
			assert.GreaterOrEqual(t, tc.n, tc.min, tc.name)
			assert.LessOrEqual(t, tc.n, tc.max, tc.name)
		}
	}
}

// TestIssueSignerConfigAttemptCap checks the cap on the user secrets that
// IssueSignerConfig (and so lazergen) samples: 1000.
func TestIssueSignerConfigAttemptCap(t *testing.T) {
	assert.Equal(t, 1000, maxAttrSamples)
	_, err := sampleAttributes("ou", "role", "eid", "rh", maxAttrSamples, func([][]byte) (bool, error) { return false, nil })
	require.ErrorContains(t, err, "after 1000 attempts")
}

// TestAttributeEncodingBoundAndInjective checks, on random attributes, the
// properties of the attribute encoding the proofs rely on: each coefficient
// of the encoding m of a 32-byte attribute is in [-7, 8], so ‖m‖₂ ≤ 64, and
// distinct attributes have distinct encodings. The encoding is recovered from
// a commitment and its opening (solveEncoding); AttrsOK accepts any five
// attributes that fit the P1 bound ‖(m_0..m_4)‖₂ ≤ 91.
func TestAttributeEncodingBoundAndInjective(t *testing.T) {
	// fixed openings, so the solved system is the same for every attribute
	c0, err := anoncreds.Commit(make([]byte, anoncreds.AttrLen))
	require.NoError(t, err)
	opening := mustOpening(t, c0)
	c0.Clear()
	encode := func(attr []byte) []int64 {
		c, err := anoncreds.CommitWithOpening(attr, opening)
		require.NoError(t, err)
		defer c.Clear()

		return solveEncoding(t, c.Nym, opening)
	}

	extreme := func(b byte) []byte { return bytes.Repeat([]byte{b}, anoncreds.AttrLen) }
	samples := [][]byte{
		make([]byte, anoncreds.AttrLen), // all zero
		extreme(0x88),                   // every coefficient 8, the largest norm: 64
		extreme(0x99),                   // every coefficient -7
		extreme(0xff),                   // every coefficient -1
	}
	for range 60 {
		samples = append(samples, randomAttr(t))
	}
	seen := map[string]int{}
	for i, attr := range samples {
		m := encode(attr)
		require.Len(t, m, 2*anoncreds.AttrLen)
		var l2sqr int64
		for _, x := range m {
			require.True(t, -7 <= x && x <= 8, "coefficient %d out of [-7, 8]", x)
			l2sqr += x * x
		}
		assert.LessOrEqual(t, l2sqr, int64(64*64), "‖m‖₂ > 64 for sample %d", i)
		// the encoding determines the attribute
		require.Equal(t, attr, solveAttribute(t, mustCommitNym(t, attr, opening), opening))
		key := string(encodingKey(m))
		j, dup := seen[key]
		require.False(t, dup && !bytes.Equal(samples[j], attr), "samples %d and %d have the same encoding", j, i)
		seen[key] = i
	}
	assert.Equal(t, int64(64*64), func() int64 {
		var s int64
		for _, x := range encode(extreme(0x88)) {
			s += x * x
		}

		return s
	}(), "the bound ‖m‖₂ ≤ 64 is tight")

	// AttrsOK, the P1 bound on all five attributes: all zero passes, all 0x88 does not
	zero := make([][]byte, anoncreds.NAttrs)
	big := make([][]byte, anoncreds.NAttrs)
	for i := range zero {
		zero[i], big[i] = make([]byte, anoncreds.AttrLen), extreme(0x88)
	}
	ok, err := anoncreds.AttrsOK(zero)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = anoncreds.AttrsOK(big)
	require.NoError(t, err)
	assert.False(t, ok, "5 x 64^2 exceeds 91^2")
}

// mustCommitNym returns the nym of the commitment to attr with opening.
func mustCommitNym(t *testing.T, attr, opening []byte) []byte {
	t.Helper()
	c, err := anoncreds.CommitWithOpening(attr, opening)
	require.NoError(t, err)
	defer c.Clear()

	return bytes.Clone(c.Nym)
}

// encodingKey returns v as a map key.
func encodingKey(v []int64) []byte {
	out := make([]byte, 0, 8*len(v))
	for _, x := range v {
		out = binary.LittleEndian.AppendUint64(out, uint64(x)) //nolint:gosec // a bijection, not an arithmetic conversion
	}

	return out
}
