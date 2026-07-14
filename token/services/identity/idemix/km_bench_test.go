/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemix

import (
	"math/rand"
	"runtime"
	"testing"
	"time"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/driver"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/stretchr/testify/require"
)

// BenchmarkKmIdentity benchmarks the identity creation
func BenchmarkKmIdentity(b *testing.B) {
	b.Run("FP256BN_AMCL", func(b *testing.B) {
		b.ReportAllocs()

		keyManager, cleanup := setupKeyManager(b, "./testdata/fp256bn_amcl/idemix", math.FP256BN_AMCL)
		defer cleanup()
		for b.Loop() {
			_, _ = keyManager.Identity(b.Context(), nil)
		}
	})

	b.Run("BLS12_381_BBS", func(b *testing.B) {
		b.ReportAllocs()

		// in this case, the backed uses GURVY directly
		keyManager, cleanup := setupKeyManager(b, "./testdata/bls12_381_bbs/idemix", math.BLS12_381_BBS)
		defer cleanup()
		for b.Loop() {
			_, _ = keyManager.Identity(b.Context(), nil)
		}
	})

	b.Run("BLS12_381_BBS_GURVY", func(b *testing.B) {
		b.ReportAllocs()

		keyManager, cleanup := setupKeyManager(b, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
		defer cleanup()
		for b.Loop() {
			_, _ = keyManager.Identity(b.Context(), nil)
		}
	})
}

// TestParallelBenchmarkIdemixKMIdentity benchmarks the identity creation in parallel
func TestParallelBenchmarkIdemixKMIdentity(t *testing.T) {
	keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	defer cleanup()

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(workers[0],
			benchmark2.Duration(),
			3*time.Second),
		func() *KeyManager {
			return keyManager
		},
		func(km *KeyManager) error {
			_, err := keyManager.Identity(t.Context(), nil)

			return err
		},
	)
	r.Print()
}

// TestParallelBenchmarkIdemixSign benchmarks the signing process
func TestParallelBenchmarkIdemixSign(t *testing.T) {
	keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	defer cleanup()
	id, err := keyManager.Identity(t.Context(), nil)
	require.NoError(t, err)

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(
			workers[0],
			benchmark2.Duration(),
			3*time.Second,
		),
		func() driver.Signer {
			return id.Signer
		},
		func(s driver.Signer) error {
			_, err := s.Sign([]byte("hello world"))

			return err
		},
	)
	r.Print()
}

// TestParallelBenchmarkIdemixVerify benchmarks the verification process
func TestParallelBenchmarkIdemixVerify(t *testing.T) {
	keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	defer cleanup()
	id, err := keyManager.Identity(t.Context(), nil)
	require.NoError(t, err)

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	n := benchmark2.SetupSamples()
	if n == 0 {
		n = 128
	}
	signatures := make([][]byte, 0, n)
	for range n {
		sigma, err := id.Signer.Sign([]byte("hello world"))
		require.NoError(t, err)
		signatures = append(signatures, sigma)
	}

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(
			workers[0],
			benchmark2.Duration(),
			3*time.Second,
		),
		func() []byte {
			return signatures[rand.Intn(len(signatures))]
		},
		func(s []byte) error {
			return id.Verifier.Verify([]byte("hello world"), s)
		},
	)
	r.Print()
}

// TestParallelBenchmarkIdemixDeserializeSigner benchmarks the signer deserialization
func TestParallelBenchmarkIdemixDeserializeSigner(t *testing.T) {
	keyManager, cleanup := setupKeyManager(t, "./testdata/bls12_381_bbs_gurvy/idemix", math.BLS12_381_BBS_GURVY)
	defer cleanup()

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	n := benchmark2.SetupSamples()
	if n == 0 {
		n = 128
	}
	ids := make([][]byte, 0, n)
	for range n {
		id, err := keyManager.Identity(t.Context(), nil)
		require.NoError(t, err)
		ids = append(ids, id.Identity)
	}

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(
			workers[0],
			benchmark2.Duration(),
			3*time.Second,
		),
		func() []byte {
			return ids[rand.Intn(len(ids))]
		},
		func(s []byte) error {
			_, err := keyManager.DeserializeSigner(t.Context(), s)

			return err
		},
	)
	r.Print()
}
