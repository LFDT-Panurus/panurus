/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package anoncreds

import (
	"bytes"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Crafted inputs. Each targets a decoder that, without the bounds of lazer
// patch 0001, reads past the end of its input (src/coder.c: the unary codes
// of the hints and of the gaussian elements). Proofs start with a fixed-width part (tB, h, tA1, c),
// which zero bytes decode, followed by the hints.

// craftHintRun returns n zero bytes except for "11" at the first hint of a
// proof starting at byte off: the hint decoder then scans the zero bytes up to
// the end for the closing 1 of a unary run.
func craftHintRun(n, off, system int) []byte {
	b := make([]byte, n)
	hb := off*8 + proofHintOffset(system)
	b[hb/8] |= 1 << (hb % 8)
	hb++
	b[hb/8] |= 1 << (hb % 8)

	return b
}

// craftGaussRun returns zero bytes for the fixed-width part of a proof
// starting at byte off and 0xff after it: the first gaussian element is a
// unary run of ones up to the end of the input.
func craftGaussRun(n, off, system int) []byte {
	b := make([]byte, n)
	for i := off + proofHintOffset(system)/8 + 1; i < n; i++ {
		b[i] = 0xff
	}

	return b
}

// craftCred returns a credential of maximal length with a zero tau and a
// unary run of ones in s1 up to the end.
func craftCred() []byte {
	b := bytes.Repeat([]byte{0xff}, MaxLenCred())
	copy(b, make([]byte, 64))

	return b
}

// maskedPrefix is the encoding of t = 0 that precedes P1 in masked attributes.
func maskedPrefix() []byte {
	t := make([]byte, 8*64*14/8+1)
	t[len(t)-1] = 0x01

	return t
}

// craftMasked returns masked attributes with a valid t and a crafted P1.
func craftMasked(gauss bool) []byte {
	t := maskedPrefix()
	n := MaxLenMaskedAttrs()
	var b []byte
	if gauss {
		b = craftGaussRun(n, len(t), proofP1)
	} else {
		b = craftHintRun(n, len(t), proofP1)
	}
	copy(b, t)

	return b
}

func craftSok(gauss bool) []byte {
	if gauss {
		return craftGaussRun(MaxLenSok(), 0, proofSok)
	}

	return craftHintRun(MaxLenSok(), 0, proofSok)
}

func craftPres(gauss bool) []byte {
	if gauss {
		return craftGaussRun(MaxLenPres(), 0, proofP2)
	}

	return craftHintRun(MaxLenPres(), 0, proofP2)
}

func requireInvalid(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted", what)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("%s: error %v is not ErrInvalid", what, err)
	}
}

// badPubKeys are malformed Falcon-512 public keys of the right length.
func badPubKeys(valid []byte) map[string][]byte {
	ff := bytes.Repeat([]byte{0xff}, PubKeyLen)
	header := append([]byte{}, valid...)
	header[0] = 0x0a
	coeff := append([]byte{}, valid...)
	coeff[1], coeff[2] = 0xff, 0xff // first coefficient >= q

	return map[string][]byte{"0xff": ff, "header": header, "coefficient": coeff}
}

func TestOOBSokVerify(t *testing.T) {
	c, err := Commit(make([]byte, AttrLen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Clear()
	g := newGuard(t, MaxLenSok())
	for _, gauss := range []bool{false, true} {
		requireInvalid(t, SokVerify(c.Nym, nil, g.put(craftSok(gauss))), "crafted sok")
	}
}

func TestOOBPresVer(t *testing.T) {
	_, ipk := IssuerKeygen()
	v, err := NewVerifier(ipk)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Clear()
	zero := revealed(make([][]byte, NAttrs), nil)
	g := newGuard(t, MaxLenPres())
	for _, gauss := range []bool{false, true} {
		requireInvalid(t, v.PresVer(zero, nil, nil, nil, g.put(craftPres(gauss)), nil), "crafted presentation")
	}
}

func TestOOBCredential(t *testing.T) {
	f := setup(t)
	g := newGuard(t, MaxLenCred())
	requireInvalid(t, f.user.CredVerify(g.put(craftCred())), "crafted credential")
	_, err := f.user.PresGen(g.put(craftCred()), nil, nil, nil, nil)
	requireInvalid(t, err, "presentation from crafted credential")
	// the valid credential, right before the guard page
	if err := f.user.CredVerify(g.put(f.cred)); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}
}

func TestOOBIssue(t *testing.T) {
	isk, ipk := IssuerKeygen()
	issuer, err := NewIssuer(ipk, isk)
	if err != nil {
		t.Fatal(err)
	}
	defer issuer.Clear()
	zero := revealed(make([][]byte, NAttrs), nil)
	g := newGuard(t, MaxLenMaskedAttrs())
	for _, gauss := range []bool{false, true} {
		_, err := issuer.Issue(zero, nil, g.put(craftMasked(gauss)))
		requireInvalid(t, err, "crafted masked attributes")
	}
	// t alone, without a proof
	_, err = issuer.Issue(zero, nil, g.put(maskedPrefix()))
	requireInvalid(t, err, "masked attributes without proof")
}

func TestBadPubKey(t *testing.T) {
	isk, ipk := IssuerKeygen()
	if !PubKeyOK(ipk) {
		t.Fatal("valid public key rejected")
	}
	for name, pk := range badPubKeys(ipk) {
		if PubKeyOK(pk) {
			t.Fatalf("%s: malformed public key accepted", name)
		}
		_, err := NewUser(pk)
		requireInvalid(t, err, name+": NewUser")
		_, err = NewVerifier(pk)
		requireInvalid(t, err, name+": NewVerifier")
		_, err = NewIssuer(pk, isk)
		requireInvalid(t, err, name+": NewIssuer")
	}
}

func TestBadIssuerKey(t *testing.T) {
	isk, ipk := IssuerKeygen()
	isk2, ipk2 := IssuerKeygen()
	_, err := NewIssuer(ipk, isk2)
	requireInvalid(t, err, "mismatched key pair")
	_, err = NewIssuer(ipk2, isk)
	requireInvalid(t, err, "mismatched key pair")
	ff := bytes.Repeat([]byte{0xff}, PrivKeyLen)
	_, err = NewIssuer(ipk, ff)
	requireInvalid(t, err, "0xff private key")
	header := append([]byte{}, isk...)
	header[0] = 0
	_, err = NewIssuer(ipk, header)
	requireInvalid(t, err, "private key header")
	flipped := append([]byte{}, isk...)
	flipped[100] ^= 0x10
	_, err = NewIssuer(ipk, flipped)
	requireInvalid(t, err, "corrupted private key")
	s, err := NewIssuer(ipk, isk)
	if err != nil {
		t.Fatal(err)
	}
	s.Clear()
}

func TestSokProveMismatchedAttr(t *testing.T) {
	a := randBytes(t, AttrLen)
	c, err := Commit(a)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Clear()
	_, err = c.SokProve([]byte("x"), randBytes(t, AttrLen))
	requireInvalid(t, err, "SokProve with another attribute")
	if _, err := c.SokProve([]byte("x"), a); err != nil {
		t.Fatalf("SokProve with the committed attribute: %v", err)
	}
}

func TestExportBeforeMask(t *testing.T) {
	_, ipk := IssuerKeygen()
	u, err := NewUser(ipk)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Clear()
	_, err = u.Export()
	requireInvalid(t, err, "Export before MaskAttrs")
	requireInvalid(t, u.CredVerify(make([]byte, 100)), "CredVerify before MaskAttrs")
	_, err = u.PresGen(make([]byte, 100), nil, nil, nil, nil)
	requireInvalid(t, err, "PresGen before MaskAttrs")
}

func TestMaskAttrsBadAttrs(t *testing.T) {
	_, ipk := IssuerKeygen()
	u, err := NewUser(ipk)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Clear()
	big := make([][]byte, NAttrs)
	for i := range big {
		big[i] = bytes.Repeat([]byte{0x88}, AttrLen) // every coefficient is 8
	}
	if ok, _ := AttrsOK(big); ok {
		t.Fatal("AttrsOK accepts attributes beyond the norm bound")
	}
	_, err = u.MaskAttrs(big, nil)
	requireInvalid(t, err, "MaskAttrs with attributes beyond the norm bound")
	_, err = u.Export()
	requireInvalid(t, err, "Export after failed MaskAttrs")

	zero := revealed(make([][]byte, NAttrs), nil)
	_, err = u.MaskAttrs(zero, []uint32{NAttrs})
	requireInvalid(t, err, "MaskAttrs with an index out of range")
	_, err = u.MaskAttrs([][]byte{{1}}, nil)
	requireInvalid(t, err, "MaskAttrs with malformed attributes")

	// a failed masking keeps the previous one
	if _, err := u.MaskAttrs(zero, nil); err != nil {
		t.Fatal(err)
	}
	st, err := u.Export()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.MaskAttrs(big, nil); err == nil {
		t.Fatal("MaskAttrs with attributes beyond the norm bound accepted")
	}
	st2, err := u.Export()
	if err != nil || !bytes.Equal(st, st2) {
		t.Fatal("failed MaskAttrs changed the state")
	}
}

func TestUseAfterClear(t *testing.T) {
	f := setup(t)
	issuer, err := NewIssuer(f.ipk, f.isk)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Commit(f.attrs[0])
	if err != nil {
		t.Fatal(err)
	}
	u, err := NewUser(f.ipk)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(f.ipk)
	if err != nil {
		t.Fatal(err)
	}
	u.Clear()
	v.Clear()
	c.Clear()
	issuer.Clear()
	// a second Clear is a no-op
	u.Clear()
	v.Clear()
	c.Clear()
	issuer.Clear()

	_, err = u.MaskAttrs(f.attrs, nil)
	requireInvalid(t, err, "User.MaskAttrs")
	_, err = u.Export()
	requireInvalid(t, err, "User.Export")
	requireInvalid(t, u.Import(make([]byte, MaxLenUserState()), f.attrs), "User.Import")
	requireInvalid(t, u.CredVerify(f.cred), "User.CredVerify")
	_, err = u.PresGen(f.cred, nil, nil, nil, nil)
	requireInvalid(t, err, "User.PresGen")
	requireInvalid(t, v.PresVer(revealed(f.attrs, nil), nil, nil, nil, []byte{1}, nil), "Verifier.PresVer")
	_, err = issuer.Issue(revealed(f.attrs, nil), nil, []byte{1})
	requireInvalid(t, err, "Issuer.Issue")
	_, err = c.Opening()
	requireInvalid(t, err, "Commitment.Opening")
	_, err = c.SokProve(nil, f.attrs[0])
	requireInvalid(t, err, "Commitment.SokProve")
	_, err = f.user.PresGen(f.cred, nil, []*Commitment{c}, []uint32{0}, nil)
	requireInvalid(t, err, "PresGen with a cleared commitment")
	_, err = f.user.PresGen(f.cred, nil, []*Commitment{nil}, []uint32{0}, nil)
	requireInvalid(t, err, "PresGen with a nil commitment")

	// nil and zero objects
	var nu *User
	_, err = nu.Export()
	requireInvalid(t, err, "nil User")
	nu.Clear()
	var zu User
	requireInvalid(t, zu.CredVerify(f.cred), "zero User")
	var zv Verifier
	requireInvalid(t, zv.PresVer(revealed(f.attrs, nil), nil, nil, nil, []byte{1}, nil), "zero Verifier")
	var zc Commitment
	_, err = zc.Opening()
	requireInvalid(t, err, "zero Commitment")
}

func TestParamsCheck(t *testing.T) {
	if rc := paramsCheck(); rc != 0 {
		t.Fatalf("anoncreds_params.h does not match anoncreds.h/anoncreds.c, check %d failed", rc)
	}
}
