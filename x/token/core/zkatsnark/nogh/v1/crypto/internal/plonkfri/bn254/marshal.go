// Copyright 2020-2026 Consensys Software Inc.
// Licensed under the Apache License, Version 2.0. See the LICENSE file for details.

package plonkfri

import (
	"bytes"
	"crypto/sha256"
	"io"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/internal/plonkfri/nativefri"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr/fft"
	"github.com/consensys/gnark-crypto/utils"
)

// MaxLogSize bounds log2 of the circuit domain size accepted when decoding a
// key. It protects decoders against allocation attacks: decoding a verifying
// key builds the FRI evaluation domain, 8·DegreeBound ≈ 16·Size points, from
// a header of a few bytes. 2^20 costs about 3 GB, 2^17 about 0.4 GB. Real
// circuits are far smaller (a 10k-constraint circuit has Size 2^14), so 2^17
// leaves 8x headroom; raise it only together with a matching limit upstream.
const MaxLogSize = 17

// readChunk is the number of elements read before growing a slice again, so
// that memory use tracks the amount of data actually present in the stream.
const readChunk = 1 << 14

var (
	// ErrInvalidKey is returned (wrapped) when a decoded key is inconsistent.
	ErrInvalidKey = errors.New("plonkfri: invalid key encoding")
	// ErrInvalidProof is returned (wrapped) when a decoded proof is malformed.
	ErrInvalidProof = errors.New("plonkfri: invalid proof encoding")
)

// readElements reads n field elements from r in bounded chunks.
func readElements(r *nativefri.Reader, n int) []fr.Element {
	res := make([]fr.Element, 0, min(n, readChunk))
	for len(res) < n && r.Err == nil {
		k := min(n-len(res), readChunk)
		res = append(res, make([]fr.Element, k)...)
		r.Elements(res[len(res)-k:])
	}

	return res
}

// ---------------------------------------------------------------------------
// VerifyingKey

// WriteTo writes the binary encoding of the verifying key to w.
func (vk *VerifyingKey) WriteTo(w io.Writer) (int64, error) {
	cw := &nativefri.Writer{W: w}
	vk.write(cw)

	return cw.N, cw.Err
}

func (vk *VerifyingKey) write(w *nativefri.Writer) {
	w.Uint64(vk.Size)
	w.Element(&vk.SizeInv)
	w.Element(&vk.Generator)
	w.Uint64(vk.NbPublicVariables)
	w.Element(&vk.CosetShift)
	w.Uint64(vk.DegreeBound)
	w.Uint64(vk.PieceSize)
	w.Bytes(vk.Pre.Root)
	w.Uint64(uint64(vk.Pre.NbPolys)) //nolint:gosec // NbPolys is the constant nbPre, never negative
}

// ReadFrom decodes a verifying key from r, overwriting *vk. The decoded key is
// checked for consistency (domain parameters, FRI sizing); the FRI scheme is
// rebuilt from the degree bound.
func (vk *VerifyingKey) ReadFrom(r io.Reader) (int64, error) {
	cr := &nativefri.Reader{R: r}
	err := vk.read(cr)

	return cr.N, err
}

func (vk *VerifyingKey) read(r *nativefri.Reader) error {
	var v VerifyingKey
	v.Size = r.Uint64()
	r.Element(&v.SizeInv)
	r.Element(&v.Generator)
	v.NbPublicVariables = r.Uint64()
	r.Element(&v.CosetShift)
	v.DegreeBound = r.Uint64()
	v.PieceSize = r.Uint64()
	v.Pre.Root = r.Bytes()
	nbPolys := r.Uint64()
	if r.Err != nil {
		return r.Err
	}

	// consistency checks, cheapest first and before anything is allocated
	if v.Size < minDomainSize || v.Size > 1<<MaxLogSize || v.Size&(v.Size-1) != 0 {
		return errors.Join(ErrInvalidKey, errors.New("bad domain size"))
	}
	if nbPolys != nbPre {
		return errors.Join(ErrInvalidKey, errors.New("bad number of preprocessed polynomials"))
	}
	v.Pre.NbPolys = int(nbPolys)
	if v.NbPublicVariables >= v.Size {
		return errors.Join(ErrInvalidKey, errors.New("bad number of public variables"))
	}
	pieceSize, degreeBound := sizes(v.Size)
	if v.PieceSize != pieceSize || v.DegreeBound != degreeBound {
		return errors.Join(ErrInvalidKey, errors.New("FRI sizes do not match the domain size"))
	}
	gen, err := fft.Generator(v.Size)
	if err != nil {
		return errors.Join(ErrInvalidKey, err)
	}
	var sizeInv, one fr.Element
	one.SetOne()
	sizeInv.SetUint64(v.Size).Inverse(&sizeInv)
	if !v.Generator.Equal(&gen) || !v.SizeInv.Equal(&sizeInv) {
		return errors.Join(ErrInvalidKey, errors.New("domain parameters do not match the domain size"))
	}
	if !v.CosetShift.Equal(&fft.NewDomain(2, fft.WithoutPrecompute()).FrMultiplicativeGen) {
		return errors.Join(ErrInvalidKey, errors.New("bad coset shift"))
	}

	if v.Fri, err = nativefri.NewScheme(v.DegreeBound, sha256.New); err != nil {
		return errors.Join(ErrInvalidKey, err)
	}
	*vk = v

	return nil
}

// ---------------------------------------------------------------------------
// ProvingKey

// WriteTo writes the binary encoding of the proving key to w. Only the
// non-derivable data is written: the verifying key, the selector polynomials
// and the copy-constraint permutation. ReadFrom recomputes everything else
// (FFT evaluations, sigma polynomials, the FRI commitment to the preprocessed
// polynomials) and checks the result against the verifying key.
func (pk *ProvingKey) WriteTo(w io.Writer) (int64, error) {
	if pk.Vk == nil {
		return 0, errors.Join(ErrInvalidKey, errors.New("proving key has no verifying key"))
	}
	cw := &nativefri.Writer{W: w}
	pk.Vk.write(cw)
	cw.Elements(pk.CQl)
	cw.Elements(pk.CQr)
	cw.Elements(pk.CQm)
	cw.Elements(pk.CQo)
	cw.Elements(pk.LQkIncompleteDomainSmall)
	for _, p := range pk.Permutation {
		cw.Uint64(uint64(p)) //nolint:gosec // permutation entries are indices in [0, 3n), never negative
	}

	return cw.N, cw.Err
}

// ReadFrom decodes a proving key from r, overwriting *pk. It rebuilds the
// derived data and rejects keys whose recomputed FRI commitment differs from
// the one in the embedded verifying key.
func (pk *ProvingKey) ReadFrom(r io.Reader) (int64, error) {
	cr := &nativefri.Reader{R: r}
	err := pk.read(cr)

	return cr.N, err
}

func (pk *ProvingKey) read(r *nativefri.Reader) error {
	var vk VerifyingKey
	if err := vk.read(r); err != nil {
		return err
	}
	n := int(vk.Size) //nolint:gosec // vk.read bounds Size by 1<<MaxLogSize

	var p ProvingKey
	p.Vk = &vk
	p.CQl = readElements(r, n)
	p.CQr = readElements(r, n)
	p.CQm = readElements(r, n)
	p.CQo = readElements(r, n)
	p.LQkIncompleteDomainSmall = readElements(r, n)
	if r.Err != nil {
		return r.Err
	}
	for _, s := range [][]fr.Element{p.CQl, p.CQr, p.CQm, p.CQo, p.LQkIncompleteDomainSmall} {
		if len(s) != n {
			return errors.Join(ErrInvalidKey, errors.New("truncated polynomial"))
		}
	}
	p.Permutation = make([]int64, 0, min(3*n, readChunk))
	for len(p.Permutation) < 3*n {
		v := r.Uint64()
		if r.Err != nil {
			return r.Err
		}
		if v >= uint64(3*n) {
			return errors.Join(ErrInvalidKey, errors.New("permutation index out of range"))
		}
		p.Permutation = append(p.Permutation, int64(v)) //nolint:gosec // v < 3n, checked above
	}
	// the copy-constraint permutation must be a bijection of [0, 3n)
	seen := make([]bool, 3*n)
	for _, v := range p.Permutation {
		if seen[v] {
			return errors.Join(ErrInvalidKey, errors.New("permutation is not a bijection"))
		}
		seen[v] = true
	}

	// derived data, exactly as in Setup
	p.Domain[0] = *fft.NewDomain(vk.Size)
	p.Domain[1] = *fft.NewDomain(3 * vk.PieceSize)
	p.CQkIncomplete = make([]fr.Element, n)
	copy(p.CQkIncomplete, p.LQkIncompleteDomainSmall)
	p.Domain[0].FFTInverse(p.CQkIncomplete, fft.DIF)
	utils.BitReverse(p.CQkIncomplete)
	for i, c := range [][]fr.Element{p.CQl, p.CQr, p.CQm, p.CQo} {
		e := make([]fr.Element, p.Domain[1].Cardinality)
		copy(e, c)
		p.Domain[1].FFT(e, fft.DIF, fft.OnCoset())
		switch i {
		case 0:
			p.EvaluationQlDomainBigBitReversed = e
		case 1:
			p.EvaluationQrDomainBigBitReversed = e
		case 2:
			p.EvaluationQmDomainBigBitReversed = e
		case 3:
			p.EvaluationQoDomainBigBitReversed = e
		}
	}
	computePermutationPolynomials(&p)

	var err error
	p.Pre, err = vk.Fri.Commit(p.CQl, p.CQr, p.CQm, p.CQo, p.CQkIncomplete, p.CS[0], p.CS[1], p.CS[2])
	if err != nil {
		return errors.Join(ErrInvalidKey, err)
	}
	if p.Pre.NbPolys != vk.Pre.NbPolys || !bytes.Equal(p.Pre.Root, vk.Pre.Root) {
		return errors.Join(ErrInvalidKey, errors.New("preprocessed polynomials do not match the verifying key"))
	}

	*pk = p

	return nil
}

// ---------------------------------------------------------------------------
// Proof

// WriteTo writes the binary encoding of the proof to w.
func (proof *Proof) WriteTo(w io.Writer) (int64, error) {
	if proof.Opening == nil {
		return 0, errors.Join(ErrInvalidProof, errors.New("proof has no opening"))
	}
	cw := &nativefri.Writer{W: w}
	cw.Bytes(proof.LRO)
	cw.Bytes(proof.Z)
	cw.Bytes(proof.H)
	e := &proof.Evals
	for _, x := range []*fr.Element{
		&e.Ql, &e.Qr, &e.Qm, &e.Qo, &e.Qk, &e.S1, &e.S2, &e.S3,
		&e.L, &e.R, &e.O, &e.Z, &e.H1, &e.H2, &e.H3, &proof.ZShifted,
	} {
		cw.Element(x)
	}
	proof.Opening.Write(cw)

	return cw.N, cw.Err
}

// ReadFrom decodes a proof from r, overwriting *proof. Lengths are bounded, so
// malformed input cannot trigger large allocations.
func (proof *Proof) ReadFrom(r io.Reader) (int64, error) {
	cr := &nativefri.Reader{R: r}
	var p Proof
	p.LRO = cr.Bytes()
	p.Z = cr.Bytes()
	p.H = cr.Bytes()
	e := &p.Evals
	for _, x := range []*fr.Element{
		&e.Ql, &e.Qr, &e.Qm, &e.Qo, &e.Qk, &e.S1, &e.S2, &e.S3,
		&e.L, &e.R, &e.O, &e.Z, &e.H1, &e.H2, &e.H3, &p.ZShifted,
	} {
		cr.Element(x)
	}
	p.Opening = new(nativefri.BatchProof)
	p.Opening.Read(cr)
	if cr.Err != nil {
		return cr.N, errors.Join(ErrInvalidProof, cr.Err)
	}
	*proof = p

	return cr.N, nil
}
