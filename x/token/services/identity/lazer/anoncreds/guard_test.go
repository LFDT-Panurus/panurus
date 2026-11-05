/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package anoncreds

import (
	"os"
	"syscall"
	"testing"
)

// guard is a buffer that ends at an inaccessible page: an input placed at its
// end by put makes any read past the input fault, so the tests and fuzz
// targets detect out-of-bounds reads of the C decoders without a sanitizer.
type guard struct {
	mem  []byte // the whole mapping, the last page is PROT_NONE
	size int    // usable bytes before the guard page
}

// newGuard maps a buffer of at least n usable bytes followed by a guard page.
func newGuard(tb testing.TB, n int) *guard {
	tb.Helper()
	pg := os.Getpagesize()
	np := (n + pg - 1) / pg
	if np == 0 {
		np = 1
	}
	mem, err := syscall.Mmap(-1, 0, (np+1)*pg, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		tb.Fatalf("mmap: %v", err)
	}
	if err := syscall.Mprotect(mem[np*pg:], syscall.PROT_NONE); err != nil {
		tb.Fatalf("mprotect: %v", err)
	}
	tb.Cleanup(func() { _ = syscall.Munmap(mem) })

	return &guard{mem: mem, size: np * pg}
}

// put copies b to the end of the usable part of the buffer and returns that
// copy, which is immediately followed by the guard page. Inputs longer than
// the buffer are returned unchanged (the C code rejects them by length).
func (g *guard) put(b []byte) []byte {
	if len(b) > g.size {
		return b
	}
	dst := g.mem[g.size-len(b) : g.size : g.size]
	copy(dst, b)

	return dst
}
