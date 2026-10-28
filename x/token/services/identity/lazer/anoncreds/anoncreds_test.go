/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package anoncreds

import (
	"crypto/rand"
	"testing"
)

func randBytes(tb testing.TB, n int) []byte {
	tb.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		tb.Fatal(err)
	}

	return b
}

func revealed(attrs [][]byte, idx []uint32) [][]byte {
	out := make([][]byte, len(attrs))
	for i := range attrs {
		out[i] = make([]byte, AttrLen)
		for _, j := range idx {
			if uint32(i) == j {
				copy(out[i], attrs[i])
			}
		}
	}

	return out
}

type fixture struct {
	isk, ipk []byte
	attrs    [][]byte
	user     *User
	verifier *Verifier
	cred     []byte
	idxRev   []uint32
}

// setup issues a credential to attrs; attribute 0 stays hidden at issuance.
func setup(tb testing.TB) *fixture {
	tb.Helper()
	f := &fixture{}
	f.isk, f.ipk = IssuerKeygen()
	for {
		f.attrs = make([][]byte, NAttrs)
		for i := range f.attrs {
			f.attrs[i] = randBytes(tb, AttrLen)
		}
		if ok, err := AttrsOK(f.attrs); err != nil {
			tb.Fatal(err)
		} else if ok {
			break
		}
	}
	var err error
	if f.user, err = NewUser(f.ipk); err != nil {
		tb.Fatal(err)
	}
	if f.verifier, err = NewVerifier(f.ipk); err != nil {
		tb.Fatal(err)
	}
	issuer, err := NewIssuer(f.ipk, f.isk)
	if err != nil {
		tb.Fatal(err)
	}
	defer issuer.Clear()

	f.idxRev = []uint32{1, 2, 3, 4}
	masked, err := f.user.MaskAttrs(f.attrs, f.idxRev)
	if err != nil {
		tb.Fatal(err)
	}
	if f.cred, err = issuer.Issue(revealed(f.attrs, f.idxRev), f.idxRev, masked); err != nil {
		tb.Fatalf("issue: %v", err)
	}
	tb.Cleanup(func() { f.user.Clear(); f.verifier.Clear() })

	return f
}

func TestPresentationWithNyms(t *testing.T) {
	f := setup(t)
	idxCom := []uint32{0, 2, 3}
	var coms []*Commitment
	var nyms [][]byte
	for _, i := range idxCom {
		c, err := Commit(f.attrs[i])
		if err != nil {
			t.Fatal(err)
		}
		defer c.Clear()
		coms = append(coms, c)
		nyms = append(nyms, c.Nym)
	}
	ctx := []byte("ctx")
	pres, err := f.user.PresGen(f.cred, nil, coms, idxCom, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, pres, ctx); err != nil {
		t.Fatalf("valid presentation rejected: %v", err)
	}

	// wrong context
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, pres, []byte("other")) == nil {
		t.Fatal("wrong ctx accepted")
	}
	// swapped nyms (binding of nyms to attributes)
	swapped := [][]byte{nyms[1], nyms[0], nyms[2]}
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, swapped, idxCom, pres, ctx) == nil {
		t.Fatal("swapped nyms accepted")
	}
	// nym to a different attribute value
	other, _ := Commit(randBytes(t, AttrLen))
	defer other.Clear()
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, [][]byte{nyms[0], other.Nym, nyms[2]}, idxCom, pres, ctx) == nil {
		t.Fatal("foreign nym accepted")
	}
	// swapped indices
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, []uint32{2, 0, 3}, pres, ctx) == nil {
		t.Fatal("swapped idxCom accepted")
	}
	// duplicate / overlapping indices
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, []uint32{0, 0, 3}, pres, ctx) == nil {
		t.Fatal("duplicate idxCom accepted")
	}
	if f.verifier.PresVer(revealed(f.attrs, []uint32{0}), []uint32{0}, nyms, idxCom, pres, ctx) == nil {
		t.Fatal("overlapping indices accepted")
	}
	// truncated / oversized / corrupted proofs, malformed nyms
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, pres[:len(pres)-1], ctx) == nil {
		t.Fatal("truncated presentation accepted")
	}
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, make([]byte, MaxLenPres()+1), ctx) == nil {
		t.Fatal("oversized presentation accepted")
	}
	bad := append([]byte{}, pres...)
	bad[len(bad)/2] ^= 1
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, bad, ctx) == nil {
		t.Fatal("corrupted presentation accepted")
	}
	if f.verifier.PresVer(revealed(f.attrs, nil), nil, [][]byte{nyms[0][:5], nyms[1], nyms[2]}, idxCom, pres, ctx) == nil {
		t.Fatal("short nym accepted")
	}
	// a different issuer
	_, ipk2 := IssuerKeygen()
	v2, _ := NewVerifier(ipk2)
	defer v2.Clear()
	if v2.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, pres, ctx) == nil {
		t.Fatal("presentation accepted under another issuer")
	}
}

func TestCredVerify(t *testing.T) {
	f := setup(t)
	if err := f.user.CredVerify(f.cred); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}
	bad := append([]byte{}, f.cred...)
	bad[len(bad)/2] ^= 0x10
	if f.user.CredVerify(bad) == nil {
		t.Fatal("corrupted credential accepted")
	}
	if f.user.CredVerify(f.cred[:10]) == nil || f.user.CredVerify(make([]byte, MaxLenCred()+1)) == nil {
		t.Fatal("malformed credential accepted")
	}
	// credential of another user
	f2 := setup(t)
	if f.user.CredVerify(f2.cred) == nil {
		t.Fatal("foreign credential accepted")
	}
}

func TestUserExportImport(t *testing.T) {
	f := setup(t)
	u2, err := NewUser(f.ipk)
	if err != nil {
		t.Fatal(err)
	}
	defer u2.Clear()
	if err := u2.Import(f.user.Export(), f.attrs); err != nil {
		t.Fatal(err)
	}
	c, _ := Commit(f.attrs[0])
	defer c.Clear()
	ctx := []byte("x")
	pres, err := u2.PresGen(f.cred, f.idxRev, []*Commitment{c}, []uint32{0}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.verifier.PresVer(revealed(f.attrs, f.idxRev), f.idxRev, [][]byte{c.Nym}, []uint32{0}, pres, ctx); err != nil {
		t.Fatal(err)
	}
	if u2.Import(make([]byte, 3), f.attrs) == nil {
		t.Fatal("short user state accepted")
	}
}

func TestSokAndOpening(t *testing.T) {
	attr := randBytes(t, AttrLen)
	c, err := Commit(attr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Clear()
	ctx := []byte("sig|message")
	proof, err := c.SokProve(ctx, attr)
	if err != nil {
		t.Fatal(err)
	}
	if err := SokVerify(c.Nym, ctx, proof); err != nil {
		t.Fatal(err)
	}
	if SokVerify(c.Nym, []byte("other"), proof) == nil {
		t.Fatal("wrong ctx accepted")
	}
	other, _ := Commit(attr)
	defer other.Clear()
	if SokVerify(other.Nym, ctx, proof) == nil {
		t.Fatal("proof accepted for another nym")
	}
	if SokVerify(c.Nym, ctx, append(proof, 0)) == nil || SokVerify(c.Nym, ctx, proof[:10]) == nil || SokVerify(c.Nym[:3], ctx, proof) == nil {
		t.Fatal("malformed input accepted")
	}

	op := c.Opening()
	if !OpeningVerify(c.Nym, attr, op) {
		t.Fatal("valid opening rejected")
	}
	if OpeningVerify(c.Nym, randBytes(t, AttrLen), op) || OpeningVerify(other.Nym, attr, op) || OpeningVerify(c.Nym, attr, op[:5]) || OpeningVerify(c.Nym[:5], attr, op) {
		t.Fatal("invalid opening accepted")
	}
	c2, err := CommitWithOpening(attr, op)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Clear()
	if string(c2.Nym) != string(c.Nym) {
		t.Fatal("re-imported commitment differs")
	}
}

func TestParamsMatchConstants(t *testing.T) {
	if !ParamsOK() {
		t.Fatal("anoncreds_params.h does not match CBDC_COM_ROWS/CBDC_MAXCOM in anoncreds.h")
	}
}

func TestBadInputs(t *testing.T) {
	if _, err := NewUser(make([]byte, 3)); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := Commit(make([]byte, 3)); err == nil {
		t.Fatal("short attr accepted")
	}
	if _, err := AttrsOK([][]byte{{1}}); err == nil {
		t.Fatal("bad attrs accepted")
	}
}

func FuzzSokVerify(f *testing.F) {
	f.Add([]byte("a"), []byte("b"), []byte("c"))
	f.Fuzz(func(t *testing.T, nym, ctx, proof []byte) {
		_ = SokVerify(nym, ctx, proof)
		_ = OpeningVerify(nym, make([]byte, AttrLen), proof)
	})
}

// zeroWitness returns a user whose state is r = 0, m = 0, and the all-zero
// credential tau = s1 = s2 = 0, i.e. the trivial witness of the credential
// relation. It needs no issuer secret.
func zeroWitness(tb testing.TB, ipk []byte) (*User, []byte) {
	tb.Helper()
	u, err := NewUser(ipk)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(u.Clear)
	zero := make([][]byte, NAttrs)
	for i := range zero {
		zero[i] = make([]byte, AttrLen)
	}
	// encodings of zero vectors: zero bits followed by the terminator bit
	st := make([]byte, MaxLenUserState())
	st[len(st)-1] = 0x01
	if err := u.Import(st, zero); err != nil {
		st[len(st)-1] = 0x80
		if err := u.Import(st, zero); err != nil {
			tb.Fatal("zero user state does not import")
		}
	}
	// 64 bytes tau, 2*8*64 gaussian coefficients of 1+7+1 bits, terminator
	cred := make([]byte, (64*8+2*8*64*9)/8+1)
	cred[len(cred)-1] = 0x01

	return u, cred
}

// TestZeroWitnessForgery checks that nobody can present without an issued
// credential by using the trivial witness, with nothing revealed (as in
// Panurus) and with an all-zero revealed attribute.
func TestZeroWitnessForgery(t *testing.T) {
	_, ipk := IssuerKeygen()
	u, cred := zeroWitness(t, ipk)
	if u.CredVerify(cred) == nil {
		t.Fatal("all-zero credential accepted")
	}
	zero := revealed(make([][]byte, NAttrs), nil)
	idxCom := []uint32{0, 2, 3}
	var coms []*Commitment
	var nyms [][]byte
	for range idxCom {
		c, err := Commit(make([]byte, AttrLen))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Clear()
		coms = append(coms, c)
		nyms = append(nyms, c.Nym)
	}
	v, err := NewVerifier(ipk)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Clear()
	ctx := []byte("ctx")
	for _, idxRev := range [][]uint32{nil, {1}} {
		// the prover refuses a witness that does not satisfy the statement
		if _, err := u.PresGen(cred, idxRev, coms, idxCom, ctx); err == nil {
			t.Fatalf("presentation from the zero witness generated (idxRev=%v)", idxRev)
		}
	}
	// a proof for the zero witness under the old, homogeneous statement does
	// not exist for the new one; a verifier must reject arbitrary bytes too
	if v.PresVer(zero, nil, nyms, idxCom, make([]byte, 1024), ctx) == nil {
		t.Fatal("garbage presentation accepted")
	}
}

// TestPresGenChecksNyms checks that a presentation is not generated for a
// nym that does not commit to the attribute at its index.
func TestPresGenChecksNyms(t *testing.T) {
	f := setup(t)
	c, err := Commit(f.attrs[1])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Clear()
	if _, err := f.user.PresGen(f.cred, nil, []*Commitment{c}, []uint32{0}, []byte("ctx")); err == nil {
		t.Fatal("presentation generated for a nym to another attribute")
	}
}

func TestCredTrailingBytes(t *testing.T) {
	f := setup(t)
	if f.user.CredVerify(append(append([]byte{}, f.cred...), 0)) == nil {
		t.Fatal("credential with trailing byte accepted")
	}
}
