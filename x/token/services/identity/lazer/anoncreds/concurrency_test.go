/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package anoncreds

import (
	"math"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TestGaussianSamplerLargeSigma checks the sampler for log2o >= 24, which
// the presentation prover uses for its mask y4 (log2stdev4 = 30): the
// elements of a vector are drawn independently, so 64 samples take (almost)
// 64 distinct values, and the samples have mean 0 and standard deviation
// 1.55*2^log2o (lazer patch 0004).
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
// on its seed and not on what was sampled before, by any thread: the same
// seed gives the same samples with another sampling in between, including one
// of an odd count (lazer patch 0004). Not parallel: the sampler must not see
// concurrent proving of other tests between the two samplings.
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

// importedUser returns a presentation User for f's wallet, imported from the
// exported state as the key manager builds it.
func importedUser(tb testing.TB, f *fixture) *User {
	tb.Helper()
	state, err := f.user.Export()
	if err != nil {
		tb.Fatal(err)
	}
	u, err := NewUser(f.ipk)
	if err != nil {
		tb.Fatal(err)
	}
	if err := u.Import(state, f.attrs); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(u.Clear)

	return u
}

// fixedCoinCommitments returns commitments to the attributes idxCom of f with
// fixed openings, so that two provers see the same statement.
func fixedCoinCommitments(tb testing.TB, f *fixture, idxCom []uint32) []*Commitment {
	tb.Helper()
	var coms []*Commitment
	for _, i := range idxCom {
		c0, err := Commit(f.attrs[i])
		if err != nil {
			tb.Fatal(err)
		}
		op, err := c0.Opening()
		c0.Clear()
		if err != nil {
			tb.Fatal(err)
		}
		c, err := CommitWithOpening(f.attrs[i], op)
		if err != nil {
			tb.Fatal(err)
		}
		tb.Cleanup(c.Clear)
		coms = append(coms, c)
	}

	return coms
}

// TestSharedPublicParamsSameProofs checks that the public matrices shared by
// the process (lazer patch 0011) give the same proofs as matrices expanded
// for the call: with the same coins, presentations and signatures of
// knowledge are byte-identical, and they verify.
func TestSharedPublicParamsSameProofs(t *testing.T) {
	f := setup(t)
	u := importedUser(t, f)
	idxCom := []uint32{0, 2, 3}
	coms := fixedCoinCommitments(t, f, idxCom)
	var nyms [][]byte
	for _, c := range coms {
		nyms = append(nyms, c.Nym)
	}
	ctx := []byte("ctx")
	for _, coins := range [][32]byte{{1}, {2, 3}} {
		private, err := u.presGenCoins(f.cred, coms, idxCom, ctx, coins, false)
		if err != nil {
			t.Fatal(err)
		}
		shared, err := u.presGenCoins(f.cred, coms, idxCom, ctx, coins, true)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(private, shared) {
			t.Fatal("presentations differ with shared public matrices")
		}
		if err := f.verifier.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, shared, ctx); err != nil {
			t.Fatalf("presentation rejected: %v", err)
		}

		private, err = coms[0].sokProveCoins(ctx, f.attrs[0], coins, false)
		if err != nil {
			t.Fatal(err)
		}
		shared, err = coms[0].sokProveCoins(ctx, f.attrs[0], coins, true)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(private, shared) {
			t.Fatal("signatures of knowledge differ with shared public matrices")
		}
		if err := SokVerify(coms[0].Nym, ctx, shared); err != nil {
			t.Fatalf("signature of knowledge rejected: %v", err)
		}
	}
}

// TestUserConcurrentUse runs presentations, credential checks, signatures of
// knowledge, equality-of-opening proofs and their verifications on one User
// and the shared public matrices at the same time. Every proof must verify and
// the shared matrices must be unchanged afterwards (the race detector does not
// see the C code, the digest does).
func TestUserConcurrentUse(t *testing.T) {
	f := setup(t)
	u := importedUser(t, f)
	before := publicParamsDigest()

	const workers = 6
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			idxCom := []uint32{0, 2, 3}
			var coms []*Commitment
			var nyms [][]byte
			for _, j := range idxCom {
				c, err := Commit(f.attrs[j])
				if err != nil {
					errs[i] = err

					return
				}
				defer c.Clear()
				coms = append(coms, c)
				nyms = append(nyms, c.Nym)
			}
			// a Verifier is not for concurrent use: one per worker
			v, err := NewVerifier(f.ipk)
			if err != nil {
				errs[i] = err

				return
			}
			defer v.Clear()
			ctx := []byte("ctx" + strconv.Itoa(i))
			pres, err := u.PresGen(f.cred, nil, coms, idxCom, ctx)
			if err == nil {
				err = v.PresVer(revealed(f.attrs, nil), nil, nyms, idxCom, pres, ctx)
			}
			if err == nil {
				err = u.CredVerify(f.cred)
			}
			var sok, eq []byte
			if err == nil {
				sok, err = coms[0].SokProve(ctx, f.attrs[0])
			}
			if err == nil {
				err = SokVerify(coms[0].Nym, ctx, sok)
			}
			if err == nil {
				eq, err = coms[1].EqOpeningProve(ctx, f.attrs[2])
			}
			if err == nil {
				err = EqOpeningVerify(coms[1].Nym, f.attrs[2], ctx, eq)
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	if publicParamsDigest() != before {
		t.Fatal("the shared public matrices changed")
	}
}

// TestUserClearWaitsForPresentations clears a User while several
// presentations are in progress: they complete, and calls after Clear fail.
func TestUserClearWaitsForPresentations(t *testing.T) {
	f := setup(t)
	u := importedUser(t, f)

	const workers = 4
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			<-start
			_, errs[i] = u.PresGen(f.cred, f.idxRev, nil, nil, []byte("ctx"))
		})
	}
	close(start)
	u.Clear()
	wg.Wait()
	for _, err := range errs {
		// a presentation either ran before Clear or saw the cleared state
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := u.PresGen(f.cred, f.idxRev, nil, nil, []byte("ctx")); err == nil {
		t.Fatal("presentation after Clear")
	}
	if err := u.CredVerify(f.cred); err == nil {
		t.Fatal("credverify after Clear")
	}
	if _, err := u.Export(); err == nil {
		t.Fatal("export after Clear")
	}
	u.Clear()
}
