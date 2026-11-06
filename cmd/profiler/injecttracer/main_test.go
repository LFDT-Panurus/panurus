/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectTracer(t *testing.T) {
	const src = `package bench

import "testing"

func BenchmarkTarget(b *testing.B) {
	_ = 1
}
`

	t.Run("injects into the named benchmark", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "bench_test.go")
		require.NoError(t, os.WriteFile(file, []byte(src), 0o600))

		require.NoError(t, injectTracer(file, "BenchmarkTarget"))

		out, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Contains(t, string(out), "tracer.Enable()")
		assert.Contains(t, string(out), "cmd/profiler/tracer")
	})

	t.Run("reports a missing benchmark without touching the file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "bench_test.go")
		require.NoError(t, os.WriteFile(file, []byte(src), 0o600))

		err := injectTracer(file, "BenchmarkAbsent")
		require.Error(t, err)
		require.EqualError(t, err, "benchmark function BenchmarkAbsent not found")

		out, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		assert.Equal(t, src, string(out))
	})

	t.Run("wraps the parse error", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "broken_test.go")
		require.NoError(t, os.WriteFile(file, []byte("this is not go"), 0o600))

		err := injectTracer(file, "BenchmarkTarget")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse file")
	})

	t.Run("wraps the error when the file is missing", func(t *testing.T) {
		err := injectTracer(filepath.Join(t.TempDir(), "absent_test.go"), "BenchmarkTarget")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse file")
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}
