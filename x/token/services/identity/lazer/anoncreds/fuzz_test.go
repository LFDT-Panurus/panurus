/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package anoncreds

import (
	"bytes"
	"testing"
)

// The fuzz targets below feed attacker-controlled bytes to the entry points
// that decode them. Under -fuzz every worker process builds its own (random)
// fixture, so the oracles must not assume that a seed belongs to the fixture
// of the process running it. Every input is placed right before an inaccessible page
// (see guard), so a decoder reading past its input crashes the target
// instead of passing silently. The seeds are a valid input, the empty input,
// truncated and bit-flipped variants and the crafted payloads of
// regression_test.go that used to read out of bounds.

// fuzzFixture is a credential with a presentation, built once per fuzz worker.
type fuzzFixture struct {
	*fixture
	idxCom []uint32
	nyms   [][]byte
	pres   []byte
	ctx    []byte
	masked []byte
	state  []byte
	com    *Commitment
	sok    []byte
	eqop   []byte
}

func newFuzzFixture(tb testing.TB) *fuzzFixture {
	tb.Helper()
	ff := &fuzzFixture{fixture: setup(tb), idxCom: []uint32{0}, ctx: []byte("fuzz")}
	var err error
	if ff.com, err = Commit(ff.attrs[0]); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(ff.com.Clear)
	ff.nyms = [][]byte{ff.com.Nym}
	if ff.pres, err = ff.user.PresGen(ff.cred, nil, []*Commitment{ff.com}, ff.idxCom, ff.ctx); err != nil {
		tb.Fatal(err)
	}
	if ff.sok, err = ff.com.SokProve(ff.ctx, ff.attrs[0]); err != nil {
		tb.Fatal(err)
	}
	if ff.eqop, err = ff.com.EqOpeningProve(ff.ctx, ff.attrs[0]); err != nil {
		tb.Fatal(err)
	}
	if ff.state, err = ff.user.Export(); err != nil {
		tb.Fatal(err)
	}
	// masked attributes for a second, independent issuance
	u, err := NewUser(ff.ipk)
	if err != nil {
		tb.Fatal(err)
	}
	defer u.Clear()
	if ff.masked, err = u.MaskAttrs(ff.attrs, ff.idxRev); err != nil {
		tb.Fatal(err)
	}

	return ff
}

// flip returns ctx with its first bit flipped, or a one-byte context.
func flip(ctx []byte) []byte {
	if len(ctx) == 0 {
		return []byte{1}
	}
	c := append([]byte{}, ctx...)
	c[0] ^= 1

	return c
}

// variants returns b, truncated and bit-flipped copies of b and the empty input.
func variants(b []byte) [][]byte {
	out := [][]byte{b, {}, b[:len(b)/2], b[:len(b)-1], append(append([]byte{}, b...), 0)}
	for _, i := range []int{0, len(b) / 3, len(b) / 2, len(b) - 1} {
		c := append([]byte{}, b...)
		c[i] ^= 0x01
		out = append(out, c)
	}

	return out
}

func FuzzSokVerify(f *testing.F) {
	ff := newFuzzFixture(f)
	for _, p := range variants(ff.sok) {
		f.Add(ff.com.Nym, ff.ctx, p)
	}
	f.Add(ff.com.Nym, ff.ctx, craftSok(false))
	f.Add(ff.com.Nym, ff.ctx, craftSok(true))
	f.Add(ff.com.Nym[:len(ff.com.Nym)-1], ff.ctx, ff.sok)
	f.Add([]byte{}, []byte{}, []byte{})
	f.Add([]byte("a"), []byte("b"), []byte("c"))
	g := newGuard(f, MaxLenSok())
	gn := newGuard(f, MaxLenNym())
	f.Fuzz(func(t *testing.T, nym, ctx, proof []byte) {
		nym, proof = gn.put(nym), g.put(proof)
		// a proof that verifies is bound to its context
		if SokVerify(nym, ctx, proof) == nil && SokVerify(nym, flip(ctx), proof) == nil {
			t.Fatal("proof accepted for another context")
		}
		_ = OpeningVerify(nym, make([]byte, AttrLen), proof)
	})
}

func FuzzEqOpeningVerify(f *testing.F) {
	ff := newFuzzFixture(f)
	for _, p := range variants(ff.eqop) {
		f.Add(ff.com.Nym, ff.attrs[0], ff.ctx, p)
	}
	f.Add(ff.com.Nym, ff.attrs[1], ff.ctx, ff.eqop)
	f.Add(ff.com.Nym, ff.attrs[0], ff.ctx, ff.sok)
	f.Add(ff.com.Nym, ff.attrs[0], ff.ctx, craftSok(false))
	f.Add(ff.com.Nym, ff.attrs[0], ff.ctx, craftSok(true))
	f.Add(ff.com.Nym[:len(ff.com.Nym)-1], ff.attrs[0], ff.ctx, ff.eqop)
	f.Add([]byte{}, []byte{}, []byte{}, []byte{})
	g := newGuard(f, MaxLenSok())
	gn := newGuard(f, MaxLenNym())
	f.Fuzz(func(t *testing.T, nym, attr, ctx, proof []byte) {
		nym, proof = gn.put(nym), g.put(proof)
		if EqOpeningVerify(nym, attr, ctx, proof) != nil {
			return
		}
		// a proof that verifies is bound to its context and to its attribute
		if EqOpeningVerify(nym, attr, flip(ctx), proof) == nil {
			t.Fatal("proof accepted for another context")
		}
		if EqOpeningVerify(nym, flip(attr), ctx, proof) == nil {
			t.Fatal("proof accepted for another attribute")
		}
	})
}

func FuzzPresVer(f *testing.F) {
	ff := newFuzzFixture(f)
	for _, p := range variants(ff.pres) {
		f.Add(p, ff.ctx, ff.nyms[0])
	}
	f.Add(craftPres(false), ff.ctx, ff.nyms[0])
	f.Add(craftPres(true), ff.ctx, ff.nyms[0])
	f.Add(ff.pres, ff.ctx, ff.nyms[0][:5])
	f.Add([]byte{}, []byte{}, []byte{})
	g := newGuard(f, MaxLenPres())
	gn := newGuard(f, MaxLenNym())
	zero := revealed(ff.attrs, nil)
	f.Fuzz(func(t *testing.T, pres, ctx, nym []byte) {
		nyms := [][]byte{gn.put(nym)}
		pres = g.put(pres)
		// a presentation that verifies is bound to its context
		if ff.verifier.PresVer(zero, nil, nyms, ff.idxCom, pres, ctx) == nil &&
			ff.verifier.PresVer(zero, nil, nyms, ff.idxCom, pres, flip(ctx)) == nil {
			t.Fatal("presentation accepted for another context")
		}
		// no nyms: exercises the statement without commitment blocks
		_ = ff.verifier.PresVer(zero, nil, nil, nil, g.put(pres), ctx)
	})
}

func FuzzIssue(f *testing.F) {
	ff := newFuzzFixture(f)
	for _, m := range variants(ff.masked) {
		f.Add(m, uint8(0x1e))
	}
	f.Add(craftMasked(false), uint8(0))
	f.Add(craftMasked(true), uint8(0))
	f.Add(maskedPrefix(), uint8(0))
	f.Add([]byte{}, uint8(0xff))
	issuer, err := NewIssuer(ff.ipk, ff.isk)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(issuer.Clear)
	g := newGuard(f, MaxLenMaskedAttrs())
	f.Fuzz(func(t *testing.T, masked []byte, rev uint8) {
		// rev selects the revealed attributes, bit i for attribute i
		var idxRev []uint32
		for i := range uint32(NAttrs) {
			if rev&(1<<i) != 0 {
				idxRev = append(idxRev, i)
			}
		}
		if rev&0x80 != 0 {
			idxRev = append(idxRev, NAttrs) // out of range
		}
		cred, err := issuer.Issue(revealed(ff.attrs, idxRev), idxRev, g.put(masked))
		if err == nil && len(cred) > MaxLenCred() {
			t.Fatalf("credential of %d bytes", len(cred))
		}
	})
}

func FuzzCredVerify(f *testing.F) {
	ff := newFuzzFixture(f)
	for _, c := range variants(ff.cred) {
		f.Add(c)
	}
	f.Add(craftCred())
	_, zcred := zeroWitness(f, ff.ipk)
	f.Add(zcred)
	g := newGuard(f, MaxLenCred())
	f.Fuzz(func(t *testing.T, cred []byte) {
		err := ff.user.CredVerify(g.put(cred))
		if err == nil && !bytes.Equal(cred, ff.cred) {
			// a credential is s1,s2 for a fixed tau: another valid one
			// would be a second preimage of a Falcon target
			t.Fatal("a different credential verifies")
		}
	})
}

// FuzzImport also drives NewUserTemplate, which imports the same encoding.
func FuzzImport(f *testing.F) {
	ff := newFuzzFixture(f)
	flat := bytes.Join(ff.attrs, nil)
	for _, s := range variants(ff.state) {
		f.Add(s, flat)
	}
	zst := make([]byte, MaxLenUserState())
	zst[len(zst)-1] = 0x01
	f.Add(zst, make([]byte, NAttrs*AttrLen))
	f.Add(bytes.Repeat([]byte{0xff}, MaxLenUserState()), flat)
	f.Add([]byte{}, []byte{})
	u, err := NewUser(ff.ipk)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(u.Clear)
	g := newGuard(f, MaxLenUserState())
	f.Fuzz(func(t *testing.T, state, attrs []byte) {
		as := make([][]byte, NAttrs)
		for i := range as {
			as[i] = make([]byte, AttrLen)
			if len(attrs) >= (i+1)*AttrLen {
				copy(as[i], attrs[i*AttrLen:])
			}
		}
		importErr := u.Import(g.put(state), as)
		// the template imports the same encoding, it accepts exactly what Import accepts
		tmpl, tmplErr := NewUserTemplate(ff.ipk, g.put(state), as)
		if (importErr == nil) != (tmplErr == nil) {
			t.Fatalf("Import: %v, NewUserTemplate: %v", importErr, tmplErr)
		}
		if tmplErr != nil {
			return
		}
		defer tmpl.Clear()
		out, err := u.Export()
		if err != nil {
			t.Fatalf("Export after Import: %v", err)
		}
		if !bytes.Equal(out, state) {
			t.Fatal("Export does not return the imported state")
		}
		_ = u.CredVerify(ff.cred)

		c, err := tmpl.Clone()
		if err != nil {
			t.Fatalf("Clone: %v", err)
		}
		defer c.Clear()
		_ = c.CredVerify(ff.cred)
	})
}

func FuzzCommitWithOpening(f *testing.F) {
	ff := newFuzzFixture(f)
	op, err := ff.com.Opening()
	if err != nil {
		f.Fatal(err)
	}
	for _, o := range variants(op) {
		f.Add(ff.attrs[0], o)
	}
	f.Add(make([]byte, AttrLen), bytes.Repeat([]byte{0xff}, MaxLenOpening()))
	f.Add([]byte{}, []byte{})
	g := newGuard(f, MaxLenOpening())
	f.Fuzz(func(t *testing.T, attr, opening []byte) {
		c, err := CommitWithOpening(attr, g.put(opening))
		if err != nil {
			return
		}
		defer c.Clear()
		if !OpeningVerify(c.Nym, attr, opening) {
			t.Fatal("opening does not verify against its own commitment")
		}
		op, err := c.Opening()
		if err != nil || !bytes.Equal(op, opening) {
			t.Fatal("Opening does not return the imported opening")
		}
		if _, err := c.SokProve(nil, attr); err != nil {
			t.Fatalf("SokProve: %v", err)
		}
	})
}

func FuzzNewVerifier(f *testing.F) {
	_, ipk := IssuerKeygen()
	for _, k := range variants(ipk) {
		f.Add(k)
	}
	for _, k := range badPubKeys(ipk) {
		f.Add(k)
	}
	g := newGuard(f, PubKeyLen)
	f.Fuzz(func(t *testing.T, pk []byte) {
		pk = g.put(pk)
		ok := PubKeyOK(pk)
		v, err := NewVerifier(pk)
		if (err == nil) != ok {
			t.Fatalf("NewVerifier error %v, PubKeyOK %v", err, ok)
		}
		if err == nil {
			v.Clear()
		}
		u, err := NewUser(pk)
		if (err == nil) != ok {
			t.Fatalf("NewUser error %v, PubKeyOK %v", err, ok)
		}
		if err == nil {
			u.Clear()
		}
	})
}
