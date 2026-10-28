// Copyright 2020-2026 Consensys Software Inc.
// Licensed under the Apache License, Version 2.0. See the LICENSE file for details.

package nativefri

import (
	"encoding/binary"
	"io"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
)

// Bounds enforced when decoding a BatchProof. They are generous upper bounds
// on what an honest prover produces (hash digests are 32 bytes, leaf rows hold
// at most a few dozen field elements, FRI has at most 64 layers), and exist so
// that a malicious length prefix cannot trigger a huge allocation.
const (
	maxBytesLen    = 1 << 10
	maxLayers      = 64
	maxCommitments = 16
	maxPath        = 64
)

// ErrDecode is returned (wrapped) when serialized data is malformed.
var ErrDecode = errors.New("nativefri: malformed serialized data")

// Writer accumulates the number of bytes written and the first error.
type Writer struct {
	W   io.Writer
	N   int64
	Err error
}

// Write implements io.Writer, remembering the first error.
func (w *Writer) Write(p []byte) (int, error) {
	if w.Err != nil {
		return 0, w.Err
	}
	n, err := w.W.Write(p)
	w.N += int64(n)
	w.Err = err

	return n, err
}

// Uint32 writes v big-endian.
func (w *Writer) Uint32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	_, _ = w.Write(b[:])
}

// Uint64 writes v big-endian.
func (w *Writer) Uint64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = w.Write(b[:])
}

// Bytes writes a length-prefixed byte slice.
func (w *Writer) Bytes(p []byte) {
	w.Uint32(uint32(len(p))) //nolint:gosec // encodes self-produced data: hashes and rows, far below 2^32 bytes
	_, _ = w.Write(p)
}

// Element writes a field element in its canonical 32-byte form.
func (w *Writer) Element(e *fr.Element) {
	b := e.Bytes()
	_, _ = w.Write(b[:])
}

// Elements writes the elements back to back, without length prefix.
func (w *Writer) Elements(v []fr.Element) {
	for i := range v {
		w.Element(&v[i])
	}
}

// Reader accumulates the number of bytes read and the first error.
type Reader struct {
	R   io.Reader
	N   int64
	Err error
}

func (r *Reader) full(p []byte) {
	if r.Err != nil {
		return
	}
	n, err := io.ReadFull(r.R, p)
	r.N += int64(n)
	r.Err = err
}

// Uint32 reads a big-endian uint32.
func (r *Reader) Uint32() uint32 {
	var b [4]byte
	r.full(b[:])

	return binary.BigEndian.Uint32(b[:])
}

// Uint64 reads a big-endian uint64.
func (r *Reader) Uint64() uint64 {
	var b [8]byte
	r.full(b[:])

	return binary.BigEndian.Uint64(b[:])
}

// Count reads a uint32 element count and checks it is at most max.
func (r *Reader) Count(max int) int {
	n := r.Uint32()
	if r.Err != nil {
		return 0
	}
	if max < 0 || uint64(n) > uint64(max) {
		r.Err = ErrDecode

		return 0
	}

	return int(n)
}

// Bytes reads a length-prefixed byte slice of at most maxBytesLen bytes.
func (r *Reader) Bytes() []byte {
	n := r.Count(maxBytesLen)
	if r.Err != nil {
		return nil
	}
	p := make([]byte, n)
	r.full(p)

	return p
}

// Element reads a canonical field element; non-canonical encodings are rejected.
func (r *Reader) Element(e *fr.Element) {
	var b [fr.Bytes]byte
	r.full(b[:])
	if r.Err != nil {
		return
	}
	if err := e.SetBytesCanonical(b[:]); err != nil {
		r.Err = ErrDecode
	}
}

// Elements reads exactly len(v) elements into v.
func (r *Reader) Elements(v []fr.Element) {
	for i := range v {
		r.Element(&v[i])
		if r.Err != nil {
			return
		}
	}
}

func (p *PairOpening) write(w *Writer) {
	w.Bytes(p.Rows[0])
	w.Bytes(p.Rows[1])
	w.Uint32(uint32(len(p.Path))) //nolint:gosec // encodes a self-produced Merkle path, at most maxPath hashes
	for _, h := range p.Path {
		w.Bytes(h)
	}
}

func (p *PairOpening) read(r *Reader) {
	p.Rows[0] = r.Bytes()
	p.Rows[1] = r.Bytes()
	n := r.Count(maxPath)
	if r.Err != nil {
		return
	}
	p.Path = make([][]byte, n)
	for i := range p.Path {
		p.Path[i] = r.Bytes()
	}
}

func writePairs(w *Writer, ps []PairOpening) {
	w.Uint32(uint32(len(ps))) //nolint:gosec // encodes a self-produced count, far below 2^32
	for i := range ps {
		ps[i].write(w)
	}
}

func readPairs(r *Reader, max int) []PairOpening {
	n := r.Count(max)
	if r.Err != nil {
		return nil
	}
	ps := make([]PairOpening, n)
	for i := range ps {
		ps[i].read(r)
		if r.Err != nil {
			return nil
		}
	}

	return ps
}

// WriteTo writes the binary encoding of the proof to w.
func (p *BatchProof) WriteTo(w io.Writer) (int64, error) {
	cw := &Writer{W: w}
	p.Write(cw)

	return cw.N, cw.Err
}

// Write writes the binary encoding of the proof to w.
func (p *BatchProof) Write(w *Writer) {
	w.Bytes(p.Mask)
	w.Uint32(uint32(len(p.Roots))) //nolint:gosec // encodes a self-produced count, far below 2^32
	for _, root := range p.Roots {
		w.Bytes(root)
	}
	w.Element(&p.Evaluation)
	w.Uint32(uint32(len(p.Queries))) //nolint:gosec // encodes a self-produced count, far below 2^32
	for i := range p.Queries {
		q := &p.Queries[i]
		q.Mask.write(w)
		writePairs(w, q.Commitments)
		writePairs(w, q.Layers)
	}
}

// ReadFrom decodes a proof from r, overwriting *p. Malformed or oversized
// input yields an error wrapping ErrDecode (or the underlying I/O error)
// without allocating more than the bounds above allow.
func (p *BatchProof) ReadFrom(r io.Reader) (int64, error) {
	cr := &Reader{R: r}
	p.Read(cr)

	return cr.N, cr.Err
}

// Read decodes a proof from r, overwriting *p.
func (p *BatchProof) Read(r *Reader) {
	p.Mask = r.Bytes()
	n := r.Count(maxLayers)
	if r.Err != nil {
		return
	}
	p.Roots = make([][]byte, n)
	for i := range p.Roots {
		p.Roots[i] = r.Bytes()
	}
	r.Element(&p.Evaluation)
	n = r.Count(nbQueries)
	if r.Err != nil {
		return
	}
	p.Queries = make([]BatchQuery, n)
	for i := range p.Queries {
		q := &p.Queries[i]
		q.Mask.read(r)
		q.Commitments = readPairs(r, maxCommitments)
		q.Layers = readPairs(r, maxLayers)
		if r.Err != nil {
			return
		}
	}
}
