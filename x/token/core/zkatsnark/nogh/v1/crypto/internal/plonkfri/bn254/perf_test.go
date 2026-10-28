/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri

import (
	"os"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/internal/plonkfri/nativefri"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	cs "github.com/consensys/gnark/constraint/bn254"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/scs"
)

// refCircuit is the same chain of squarings as backend/plonk's refCircuit,
// so plonkfri numbers are comparable with the KZG backend and with the
// measurements recorded in PROJECT.md.
type refCircuit struct {
	nbConstraints int
	X             frontend.Variable
	Y             frontend.Variable `gnark:",public"`
}

func (c *refCircuit) Define(api frontend.API) error {
	for range c.nbConstraints {
		c.X = api.Mul(c.X, c.X)
	}
	api.AssertIsEqual(c.X, c.Y)

	return nil
}

// proofSize is the number of bytes of a proof's content (field elements and
// hashes), i.e. what a compact serialization would take.
func proofSize(p *Proof) int {
	n := len(p.LRO) + len(p.Z) + len(p.H) + 16*fr.Bytes // roots, 15 evaluations + z(ω·zeta)
	n += fr.Bytes                                       // final FRI evaluation
	n += len(p.Opening.Mask)
	for _, r := range p.Opening.Roots {
		n += len(r)
	}
	for _, q := range p.Opening.Queries {
		for _, op := range append(append([]nativefri.PairOpening{q.Mask}, q.Commitments...), q.Layers...) {
			n += len(op.Rows[0]) + len(op.Rows[1])
			for _, h := range op.Path {
				n += len(h)
			}
		}
	}

	return n
}

// TestPerfRef measures setup/prove/verify time and proof size on refCircuit.
// Opt-in: PLONKFRI_PERF=1 go test -run TestPerfRef -v ./nogh/v1/crypto/internal/plonkfri/bn254/
func TestPerfRef(t *testing.T) {
	if os.Getenv("PLONKFRI_PERF") == "" {
		t.Skip("set PLONKFRI_PERF=1 to run")
	}
	for _, nb := range []int{1, 1<<12 - 3, 1<<16 - 3} {
		ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &refCircuit{nbConstraints: nb})
		if err != nil {
			t.Fatal(err)
		}
		spr := ccs.(*cs.SparseR1CS)
		w, err := frontend.NewWitness(&refCircuit{nbConstraints: nb, X: 1, Y: 1}, ecc.BN254.ScalarField())
		if err != nil {
			t.Fatal(err)
		}
		pw, _ := w.Public()

		start := time.Now()
		pk, vk, err := Setup(spr)
		if err != nil {
			t.Fatal(err)
		}
		setup := time.Since(start)
		start = time.Now()
		proof, err := Prove(spr, pk, w)
		if err != nil {
			t.Fatal(err)
		}
		prove := time.Since(start)
		start = time.Now()
		if err := Verify(proof, vk, pw.Vector().(fr.Vector)); err != nil {
			t.Fatal(err)
		}
		verify := time.Since(start)
		t.Logf("constraints=%d domain=%d: setup=%v prove=%v verify=%v proof=%.2f MB",
			spr.GetNbConstraints(), vk.Size, setup.Round(time.Millisecond), prove.Round(time.Millisecond),
			verify.Round(100*time.Microsecond), float64(proofSize(proof))/1e6)
	}
}
