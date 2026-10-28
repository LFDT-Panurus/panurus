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
	benchmarkDeserializeSigner(t, func(t *testing.T, km *KeyManager, raw []byte) error {
		t.Helper()
		_, err := km.DeserializeSigner(t.Context(), raw)

		return err
	})
}

// TestParallelBenchmarkLazerDeserializeSignerNoProbe benchmarks the probe-free signer deserialization
func TestParallelBenchmarkLazerDeserializeSignerNoProbe(t *testing.T) {
	benchmarkDeserializeSigner(t, func(t *testing.T, km *KeyManager, raw []byte) error {
		t.Helper()
		_, err := km.DeserializeSignerNoProbe(t.Context(), raw)

		return err
	})
}

func benchmarkDeserializeSigner(t *testing.T, fn func(*testing.T, *KeyManager, []byte) error) {
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
		func(s []byte) error { return fn(t, w.km, s) },
	)
	require.Zero(t, r.ErrorCount, "benchmark operations failed")
	r.Print()
}

// BenchmarkMatchIdentityCold benchmarks the audit matching of an identity against its audit
// info with a cold presentation cache: every iteration uses a fresh Deserializer, so the
// presentation carried by the audit info is fully verified. Creating the Deserializer is
// not timed.
func BenchmarkMatchIdentityCold(b *testing.B) {
	b.ReportAllocs()
	w := newTestWallet(b, "alice")
	id, err := w.km.Identity(b.Context(), nil)
	require.NoError(b, err)

	for range b.N {
		b.StopTimer()
		des, err := NewDeserializer(w.ipk)
		require.NoError(b, err)
		b.StartTimer()
		require.NoError(b, des.MatchIdentity(b.Context(), id.Identity, id.AuditInfo))
		b.StopTimer()
		des.Close()
	}
}

// TestParallelBenchmarkLazerMatchIdentityWarm benchmarks the audit matching of an identity
// against its audit info with a warm presentation cache: the samples are reused, so after
// the first pass the presentation check is a cache hit and only the openings check is
// measured. See BenchmarkMatchIdentityCold for the uncached cost.
func TestParallelBenchmarkLazerMatchIdentityWarm(t *testing.T) {
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
	type sample struct{ identity, auditInfo []byte }
	samples := make([]sample, 0, n)
	for range n {
		id, err := w.km.Identity(t.Context(), nil)
		require.NoError(t, err)
		samples = append(samples, sample{identity: id.Identity, auditInfo: id.AuditInfo})
	}

	r := benchmark2.RunBenchmark(
		benchmark2.NewConfig(workers[0], benchmark2.Duration(), 3*time.Second),
		func() sample { return samples[rand.Intn(len(samples))] },
		func(s sample) error { return des.MatchIdentity(t.Context(), s.identity, s.auditInfo) },
	)
	require.Zero(t, r.ErrorCount, "benchmark operations failed")
	r.Print()
}
