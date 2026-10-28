/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package anoncreds

import (
	"math"
	"slices"
	"sync"
	"testing"
)

// TestGaussianSamplerLargeSigma checks the sampler for log2o >= 24, which
// the presentation prover uses for its mask y4 (log2stdev4 = 30). lazer used
// to reseed the rng with the same seed for every element of a vector, so a
// vector of 64 elements took about 2 distinct values (lazer patch 0004).
func TestGaussianSamplerLargeSigma(t *testing.T) {
	for _, log2o := range []uint{12, 20, 23, 24, 30} {
		const n = 4096
		v, err := gaussianSamples(n, log2o, [32]byte{1}, 7)
		if err != nil {
			t.Fatal(err)
		}
		distinct := len(slices.Compact(slices.Sorted(slices.Values(v[:64]))))
		if distinct < 60 {
			t.Errorf("log2o=%d: %d distinct values in 64 samples", log2o, distinct)
		}
		var sum, sum2 float64
		for _, x := range v {
			sum += float64(x)
			sum2 += float64(x) * float64(x)
		}
		sigma := 1.55 * math.Exp2(float64(log2o))
		mean, sd := sum/n, math.Sqrt(sum2/n)
		// 4096 samples: the standard error of the mean is sigma/64, of sd about sigma/90
		if math.Abs(mean) > 5*sigma/64 || math.Abs(sd/sigma-1) > 0.08 {
			t.Errorf("log2o=%d: mean %.3g sd %.3g, expected 0 and %.3g", log2o, mean, sd, sigma)
		}
	}
}

// TestGaussianSamplerIsAFunctionOfItsSeed checks that a sample depends only
// on its seed: lazer kept the sign bits of the sampler in a process-global
// buffer, so a sample depended on what was sampled before, by any thread
// (lazer patch 0004). Not parallel: before the fix, concurrent proving in
// other tests perturbs the buffer.
func TestGaussianSamplerIsAFunctionOfItsSeed(t *testing.T) {
	for _, log2o := range []uint{12, 30} {
		a1, err := gaussianSamples(64, log2o, [32]byte{1}, 3)
		if err != nil {
			t.Fatal(err)
		}
		// an odd count leaves a partially used sign-bit buffer behind
		if _, err := gaussianSamples(37, log2o, [32]byte{2}, 5); err != nil {
			t.Fatal(err)
		}
		a2, err := gaussianSamples(64, log2o, [32]byte{1}, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(a1, a2) {
			t.Errorf("log2o=%d: same seed, different samples", log2o)
		}
	}
}

func templateOf(tb testing.TB, f *fixture) *UserTemplate {
	tb.Helper()
	state, err := f.user.Export()
	if err != nil {
		tb.Fatal(err)
	}
	tmpl, err := NewUserTemplate(f.ipk, state, f.attrs)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(tmpl.Clear)

	return tmpl
}

// exportClone returns the state of a fresh clone of tmpl, that is of tmpl.
func exportClone(tb testing.TB, tmpl *UserTemplate) []byte {
	tb.Helper()
	u, err := tmpl.Clone()
	if err != nil {
		tb.Fatal(err)
	}
	defer u.Clear()
	state, err := u.Export()
	if err != nil {
		tb.Fatal(err)
	}

	return state
}

func TestUserTemplate(t *testing.T) {
	f := setup(t)
	tmpl := templateOf(t, f)

	// the template itself never runs arithmetic on its state
	if err := (&User{c: tmpl.c}).CredVerify(f.cred); err == nil {
		t.Fatal("credverify on a template")
	}
	if _, err := (&User{c: tmpl.c}).PresGen(f.cred, nil, nil, nil, nil); err == nil {
		t.Fatal("presgen on a template")
	}
	if _, err := (&User{c: tmpl.c}).Export(); err == nil {
		t.Fatal("export of a template")
	}
	if _, err := (&User{c: tmpl.c}).MaskAttrs(f.attrs, f.idxRev); err == nil {
		t.Fatal("maskattrs on a template")
	}

	u, err := tmpl.Clone()
	if err != nil {
		t.Fatal(err)
	}
	defer u.Clear()
	if err := u.CredVerify(f.cred); err != nil {
		t.Fatalf("clone rejects the credential: %v", err)
	}
	// a clone presents, but has no issuance prover
	if _, err := u.MaskAttrs(f.attrs, f.idxRev); err == nil {
		t.Fatal("maskattrs on a clone")
	}
	pres, err := u.PresGen(f.cred, f.idxRev, nil, nil, []byte("ctx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.verifier.PresVer(revealed(f.attrs, f.idxRev), f.idxRev, nil, nil, pres, []byte("ctx")); err != nil {
		t.Fatalf("presentation of a clone rejected: %v", err)
	}

	// the prover keeps no witness after a presentation (lazer patch 0005),
	// whether it succeeds or not
	if wiped, err := p2WitnessWiped(u); err != nil || !wiped {
		t.Fatalf("witness kept after a presentation (%v)", err)
	}
	bad := append([]byte(nil), f.cred...)
	bad[len(bad)/2] ^= 1
	if _, err := u.PresGen(bad, f.idxRev, nil, nil, []byte("ctx")); err == nil {
		t.Fatal("presentation with a corrupted credential")
	}
	if wiped, err := p2WitnessWiped(u); err != nil || !wiped {
		t.Fatalf("witness kept after a failed presentation (%v)", err)
	}

	// malformed inputs
	if _, err := NewUserTemplate(f.ipk[:10], nil, f.attrs); err == nil {
		t.Fatal("short public key accepted")
	}
	if _, err := NewUserTemplate(f.ipk, []byte("short"), f.attrs); err == nil {
		t.Fatal("malformed state accepted")
	}
	if _, err := NewUserTemplate(f.ipk, nil, f.attrs[:2]); err == nil {
		t.Fatal("missing attributes accepted")
	}
	// cleared objects
	c := templateOf(t, f)
	c.Clear()
	c.Clear()
	if _, err := c.Clone(); err == nil {
		t.Fatal("clone of a cleared template")
	}
}

// TestUserTemplateConcurrentClones clones one template from several
// goroutines and proves with every clone at the same time. The template is
// only read by the clones; every presentation must verify.
func TestUserTemplateConcurrentClones(t *testing.T) {
	f := setup(t)
	tmpl := templateOf(t, f)
	before := exportClone(t, tmpl)

	const workers = 4
	press := make([][]byte, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			u, err := tmpl.Clone()
			if err != nil {
				errs[i] = err

				return
			}
			defer u.Clear()
			press[i], errs[i] = u.PresGen(f.cred, f.idxRev, nil, nil, []byte("ctx"))
		})
	}
	wg.Wait()
	for i := range workers {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if err := f.verifier.PresVer(revealed(f.attrs, f.idxRev), f.idxRev, nil, nil, press[i], []byte("ctx")); err != nil {
			t.Fatalf("presentation %d rejected: %v", i, err)
		}
	}
	// the race detector does not see C: check that the template was only read
	if after := exportClone(t, tmpl); !slices.Equal(before, after) {
		t.Fatal("cloning changed the template")
	}
	u, err := tmpl.Clone()
	if err != nil {
		t.Fatal(err)
	}
	defer u.Clear()
	if err := u.CredVerify(f.cred); err != nil {
		t.Fatalf("clone rejects the credential: %v", err)
	}
}
