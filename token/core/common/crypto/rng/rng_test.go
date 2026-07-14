/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rng_test

import (
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/common/crypto/rng"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureRNG_Read(t *testing.T) {
	rng := rng.NewSecureRNG()

	// 1. Basic Read
	buf := make([]byte, 32)
	n, err := rng.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 32, n)
	assert.NotEqual(t, make([]byte, 32), buf, "Buffer should not be zero")

	// 2. Randomness check (sanity only)
	buf2 := make([]byte, 32)
	_, err = rng.Read(buf2)
	require.NoError(t, err)
	assert.NotEqual(t, buf, buf2, "Subsequent reads should differ")
}

func TestSecureRNG_ReseedInterval(t *testing.T) {
	// Set a tiny reseed interval
	rng := rng.NewSecureRNGWith(10*time.Millisecond, 1024*1024)

	buf := make([]byte, 16)
	_, err := rng.Read(buf)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// This read should trigger a reseed internally.
	// We can't easily observe the internal pointer change without mocks,
	// but we can verify it still works and doesn't panic or error.
	_, err = rng.Read(buf)
	require.NoError(t, err)
}

func TestSecureRNG_ReseedVolume(t *testing.T) {
	// Set a tiny volume limit (e.g., 10 bytes)
	rng := rng.NewSecureRNGWith(1*time.Hour, 10)

	// Read 1: 8 bytes (Counter = 8)
	buf1 := make([]byte, 8)
	_, err := rng.Read(buf1)
	require.NoError(t, err)

	// Read 2: 8 bytes (Counter = 16, > 10).
	// Next read *after* this wrapper is reused should trigger reseed,
	// OR if the check happens before read.
	// Our logic checks: if w.bytesRead > limit.
	// Current logic:
	// 1. Get w (bytes=0). Read 8. Put w (bytes=8).
	// 2. Get w (bytes=8). Read 8. Put w (bytes=16).
	// 3. Get w (bytes=16). Check (16 > 10) -> Reseed.

	buf2 := make([]byte, 8)
	_, err = rng.Read(buf2)
	require.NoError(t, err)

	// This third read guarantees we hit the reseed path if we get the same wrapper
	// (which is likely in a single-thread test).
	buf3 := make([]byte, 8)
	_, err = rng.Read(buf3)
	require.NoError(t, err)
}

func TestSecureRNG_Concurrency(t *testing.T) {
	rng := rng.NewSecureRNG()
	concurrency := 100

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for range concurrency {
		go func(t *testing.T) {
			t.Helper()
			defer wg.Done()
			<-start

			buf := make([]byte, 128)
			_, err := rng.Read(buf)
			assert.NoError(t, err)

			// Basic check that we didn't get all zeros
			assert.NotEqual(t, make([]byte, 128), buf)
		}(t)
	}

	close(start)
	wg.Wait()
}

func TestSecureRNG_LargeRead(t *testing.T) {
	// Test reading a buffer larger than standard checks
	rngInst := rng.NewSecureRNG()
	buf := make([]byte, 1024*1024) // 1MB
	n, err := rngInst.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 1024*1024, n)
}

func TestSecureRNG_Read_Error(t *testing.T) {
	// 1. Test failure in initial seeding (NewSecureRNG)
	oldReader := rand.Reader
	defer func() { rand.Reader = oldReader }()

	rand.Reader = &errorReader{}
	rngInst := rng.NewSecureRNG()
	buf := make([]byte, 32)
	_, err := rngInst.Read(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RNG seed failed")

	// 2. Test failure in reseed (Time based)
	rand.Reader = oldReader
	rngInst = rng.NewSecureRNGWith(10*time.Millisecond, 1024*1024)
	_, err = rngInst.Read(buf)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	rand.Reader = &errorReader{}
	_, err = rngInst.Read(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RNG seed failed")

	// 3. Test failure in reseed (Volume based)
	rand.Reader = oldReader
	rngInst = rng.NewSecureRNGWith(1*time.Hour, 10)
	_, err = rngInst.Read(make([]byte, 16)) // volume = 16 > 10
	require.NoError(t, err)

	rand.Reader = &errorReader{}
	_, err = rngInst.Read(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RNG seed failed")
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, assert.AnError
}

// standard sizes for benchmarking
var sizes = []struct {
	name string
	len  int
}{
	{"32B", 32},      // Key/Nonce generation
	{"4KB", 4096},    // Page size / small file
	{"1MB", 1048576}, // High throughput data
}

// --- Serial Benchmarks ---

func BenchmarkCryptoRand_Read(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			buf := make([]byte, size.len)
			b.SetBytes(int64(size.len))
			b.ResetTimer()

			for b.Loop() {
				// crypto/rand typically involves a syscall (getrandom/urandom)
				// or a locked internal generator depending on OS/Go version.
				_, err := rand.Read(buf)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSecureRNG_Read(b *testing.B) {
	// Initialize once outside the loop
	rng := rng.NewSecureRNG()

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			buf := make([]byte, size.len)
			b.SetBytes(int64(size.len))
			b.ResetTimer()

			for b.Loop() {
				// Should be faster due to user-space buffering and sync.Pool
				_, err := rng.Read(buf)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- Parallel Benchmarks ---

func BenchmarkCryptoRand_Read_Parallel(b *testing.B) {
	// Only testing 4KB for parallel to save time/noise,
	// but you can expand this if needed.
	size := 4096
	b.SetBytes(int64(size))

	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine needs its own buffer in a real app,
		// but here we can reuse strictly for throughput testing
		// (though writing to same slice concurrently is unsafe,
		// crypto/rand is safe, the data race is on 'buf' contents
		// which we don't read. For strict correctness, allocate per loop).
		localBuf := make([]byte, size)
		for pb.Next() {
			_, err := rand.Read(localBuf)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkSecureRNG_Read_Parallel(b *testing.B) {
	rng := rng.NewSecureRNG()
	size := 4096
	b.SetBytes(int64(size))

	b.RunParallel(func(pb *testing.PB) {
		localBuf := make([]byte, size)
		for pb.Next() {
			// This tests the contention on sync.Pool
			_, err := rng.Read(localBuf)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
