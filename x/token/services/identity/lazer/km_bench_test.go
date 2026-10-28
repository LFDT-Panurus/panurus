/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/stretchr/testify/require"
)

// BenchmarkKmIdentity benchmarks the identity creation
func BenchmarkKmIdentity(b *testing.B) {
	b.ReportAllocs()
	w := newTestWallet(b, "alice")
	for b.Loop() {
		_, _ = w.km.Identity(b.Context(), nil)
	}
}

// TestParallelBenchmarkLazerKMIdentity benchmarks the identity creation in parallel
func TestParallelBenchmarkLazerKMIdentity(t *testing.T) {
	w := newTestWallet(t, "alice")

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(workers[0], benchmark2.Duration(), 3*time.Second),
		func() *KeyManager { return w.km },
		func(km *KeyManager) error {
			_, err := km.Identity(t.Context(), nil)

			return err
		},
	)
	r.Print()
}

// TestParallelBenchmarkLazerSign benchmarks the signing process
func TestParallelBenchmarkLazerSign(t *testing.T) {
	w := newTestWallet(t, "alice")
	id, err := w.km.Identity(t.Context(), nil)
	require.NoError(t, err)

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(workers[0], benchmark2.Duration(), 3*time.Second),
		func() driver.Signer { return id.Signer },
		func(s driver.Signer) error {
			_, err := s.Sign([]byte("hello world"))

			return err
		},
	)
	r.Print()
}

// TestParallelBenchmarkLazerVerify benchmarks the verification process
func TestParallelBenchmarkLazerVerify(t *testing.T) {
	w := newTestWallet(t, "alice")
	des, err := NewDeserializer(w.ipk)
	require.NoError(t, err)
	t.Cleanup(des.Close)

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	n := benchmark2.SetupSamples()
	if n == 0 {
		n = 128
	}
	type sample struct{ identity, sigma []byte }
	samples := make([]sample, 0, n)
	for range n {
		id, err := w.km.Identity(t.Context(), nil)
		require.NoError(t, err)
		sigma, err := id.Signer.Sign([]byte("hello world"))
		require.NoError(t, err)
		samples = append(samples, sample{identity: id.Identity, sigma: sigma})
	}

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(workers[0], benchmark2.Duration(), 3*time.Second),
		func() sample { return samples[rand.Intn(len(samples))] },
		func(s sample) error {
			v, err := des.DeserializeVerifier(t.Context(), s.identity)
			if err != nil {
				return err
			}

			return v.Verify([]byte("hello world"), s.sigma)
		},
	)
	r.Print()
}

// TestParallelBenchmarkLazerDeserializeSigner benchmarks the signer deserialization
func TestParallelBenchmarkLazerDeserializeSigner(t *testing.T) {
	benchmarkDeserializeSigner(t, func(km *KeyManager, t *testing.T, raw []byte) error {
		_, err := km.DeserializeSigner(t.Context(), raw)

		return err
	})
}

// TestParallelBenchmarkLazerDeserializeSignerNoProbe benchmarks the probe-free signer deserialization
func TestParallelBenchmarkLazerDeserializeSignerNoProbe(t *testing.T) {
	benchmarkDeserializeSigner(t, func(km *KeyManager, t *testing.T, raw []byte) error {
		_, err := km.DeserializeSignerNoProbe(t.Context(), raw)

		return err
	})
}

func benchmarkDeserializeSigner(t *testing.T, fn func(*KeyManager, *testing.T, []byte) error) {
	t.Helper()
	w := newTestWallet(t, "alice")

	workers, err := benchmark2.Workers(runtime.NumCPU())
	require.NoError(t, err)

	n := benchmark2.SetupSamples()
	if n == 0 {
		n = 128
	}
	ids := make([][]byte, 0, n)
	for range n {
		id, err := w.km.Identity(t.Context(), nil)
		require.NoError(t, err)
		// the wrapped key manager stores the signer info, the bare one does not
		w.store.put(id.Identity, id.SignerInfo)
		ids = append(ids, id.Identity)
	}

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(workers[0], benchmark2.Duration(), 3*time.Second),
		func() []byte { return ids[rand.Intn(len(ids))] },
		func(s []byte) error { return fn(w.km, t, s) },
	)
	require.Zero(t, r.ErrorCount, "benchmark operations failed")
	r.Print()
}
