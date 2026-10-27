/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"slices"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("rand.Read failed")
	}

	return b
}

func contains(idx []uint32, i uint32) bool {
	return slices.Contains(idx, i)
}

// revealed returns attrs with all attributes not in idx zeroed out.
func revealed(attrs [][]byte, idx []uint32) [][]byte {
	out := make([][]byte, len(attrs))
	for i := range attrs {
		out[i] = make([]byte, anoncreds.AttrLen)
		if contains(idx, uint32(i)) {
			copy(out[i], attrs[i])
		}
	}

	return out
}

// failure is raised by fail and turned into a non-zero exit by main.
type failure string

func fail(msg string) {
	panic(failure(msg))
}

func must[T any](v T, err error) T {
	if err != nil {
		fail(err.Error())
	}

	return v
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(failure)
			if !ok {
				panic(r)
			}
			fmt.Println(string(f))
			os.Exit(1)
		}
	}()
	run()
}

func run() {
	fmt.Print("lazer CBDC demo\n")
	fmt.Print("---------------\n\n")

	isk, ipk := anoncreds.IssuerKeygen()

	// 5 random 32-byte attributes whose encoding satisfies the issuance norm bound
	var attrs [][]byte
	for {
		attrs = make([][]byte, anoncreds.NAttrs)
		for i := range attrs {
			attrs[i] = randBytes(anoncreds.AttrLen)
		}
		if ok := must(anoncreds.AttrsOK(attrs)); ok {
			break
		}
	}
	// the context (e.g. a hash of a message) the proofs are bound to
	ctx := randBytes(32)

	// attributes revealed to the issuer
	idxRev := []uint32{1}
	attrsRev := revealed(attrs, idxRev)

	user := must(anoncreds.NewUser(ipk))
	defer user.Clear()
	issuer := must(anoncreds.NewIssuer(ipk, isk))
	defer issuer.Clear()
	verifier := must(anoncreds.NewVerifier(ipk))
	defer verifier.Clear()
	fmt.Print("Initialized user, issuer and verifier ... [OK]\n\n")

	maskedAttrs := must(user.MaskAttrs(attrs, idxRev))
	fmt.Printf("masked attributes (t,P1): %d bytes\n\n", len(maskedAttrs))

	fmt.Print("Issuer checks the masked attributes and outputs credentials ... ")
	cred, err := issuer.Issue(attrsRev, idxRev, maskedAttrs)
	if err != nil {
		fail("masked attributes are invalid.")
	}
	if _, err := issuer.Issue(attrsRev, []uint32{0, 1, 2, 3, 4}, maskedAttrs); err == nil {
		fail("issuing for invalid indices should fail, but succeeded.")
	}
	attrsRevInv := revealed(attrs, idxRev)
	attrsRevInv[idxRev[0]] = make([]byte, anoncreds.AttrLen)
	if _, err := issuer.Issue(attrsRevInv, idxRev, maskedAttrs); err == nil {
		fail("issuing for invalid attributes should fail, but succeeded.")
	}
	fmt.Print("[OK]\n\n")
	fmt.Printf("credentials (tau,s1,s2): %d bytes\n\n", len(cred))

	// attributes revealed to the verifier, and attributes committed to
	idxRev = []uint32{0}
	idxCom := []uint32{1, 2}
	for _, i := range idxCom {
		if contains(idxRev, i) {
			fail("revealed and committed attributes must be disjoint.")
		}
	}
	attrsRev = revealed(attrs, idxRev)

	fmt.Print("User creates nyms ... ")
	coms := make([]*anoncreds.Commitment, len(idxCom))
	nyms := make([][]byte, len(idxCom))
	for k, i := range idxCom {
		com := must(anoncreds.Commit(attrs[i]))
		defer com.Clear()
		coms[k], nyms[k] = com, com.Nym
		// standalone signature of knowledge and opening
		proof := must(com.SokProve(ctx, attrs[i]))
		if err := anoncreds.SokVerify(com.Nym, ctx, proof); err != nil {
			fail("nym proof invalid.")
		}
		if !anoncreds.OpeningVerify(com.Nym, attrs[i], com.Opening()) {
			fail("nym opening invalid.")
		}
	}
	fmt.Print("[OK]\n\n")

	fmt.Print("User generates a presentation ... ")
	pres, err := user.PresGen(cred, idxRev, coms, idxCom, ctx)
	if err != nil {
		fail("presentation failed.")
	}
	fmt.Print("[OK]\n\n")
	fmt.Printf("presentation (P2): %d bytes\n\n", len(pres))

	fmt.Print("Verifier verifies the presentation ... ")
	if err := verifier.PresVer(attrsRev, idxRev, nyms, idxCom, pres, ctx); err != nil {
		fail("presentation invalid.")
	}
	if verifier.PresVer(attrsRev, []uint32{0, 1, 2, 3, 4}, nyms, idxCom, pres, ctx) == nil {
		fail("verifying for invalid indices should fail, but succeeded.")
	}
	attrsRevInv = revealed(attrs, idxRev)
	attrsRevInv[idxRev[0]] = make([]byte, anoncreds.AttrLen)
	if verifier.PresVer(attrsRevInv, idxRev, nyms, idxCom, pres, ctx) == nil {
		fail("verifying for invalid attributes should fail, but succeeded.")
	}
	if verifier.PresVer(attrsRev, idxRev, []([]byte){nyms[1], nyms[0]}, idxCom, pres, ctx) == nil {
		fail("verifying for swapped nyms should fail, but succeeded.")
	}
	fmt.Print("[OK]\n\n")
}
