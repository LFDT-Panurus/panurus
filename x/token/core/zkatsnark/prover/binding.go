/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"math/big"
	"sort"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
)

// domainSeparator prevents a binding signature produced by this scheme from
// being valid in, or confused with, any other protocol context.
const domainSeparator = "zkatsnark-action-binding-v1"

// ProofResult holds everything one input or output token contributes to an
// action's binding signature. All fields are raw bytes to avoid locking
// the type to a single curve.
type ProofResult struct {
	Commitment       []byte // 32 bytes
	ValueCommitmentX []byte // 32 bytes
	ValueCommitmentY []byte // 32 bytes
	RCV              []byte // 32 bytes
}

// sortedByCommitment returns results sorted lexicographically by
// Commitment bytes. This makes ComputeActionHash canonical regardless of
// which order the Orchestrator's goroutines happen to complete in — two
// runs proving the same logical action, in any goroutine completion order,
// must produce the same hash.
func sortedByCommitment(results []ProofResult) []ProofResult {
	sorted := make([]ProofResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].Commitment, sorted[j].Commitment) < 0
	})

	return sorted
}

// writeLengthPrefixed writes an 8-byte big-endian length followed by the
// bytes. Used for actionType and tokenType, which are variable-length
// strings — without a length prefix, concatenating two variable-length
// strings back-to-back is ambiguous (e.g. "a"+"bc" and "ab"+"c" would hash
// identically under naive concatenation). Fixed-length fields (commitments,
// coordinates) don't need this.
func writeLengthPrefixed(h hash.Hash, b []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	_, _ = h.Write(lenBuf[:])
	_, _ = h.Write(b)
}

// ComputeActionHash computes the canonical, deterministic message the
// binding signature is produced over. Must be computed byte-identically by
// the prover (when signing) and eventually the validator (when verifying) —
// any divergence in sorting, encoding, or field ordering causes every
// legitimate binding signature to fail verification with no useful
// diagnostic.
func ComputeActionHash(actionType string, typeCommitment []byte, inputs, outputs []ProofResult) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte(domainSeparator))
	writeLengthPrefixed(h, []byte(actionType))

	// TypeCommitment is a fixed-length field element (32 bytes), so no
	// length prefix is needed — there is no concatenation ambiguity.
	_, _ = h.Write(typeCommitment)

	for _, r := range sortedByCommitment(inputs) {
		_, _ = h.Write(r.Commitment)
		_, _ = h.Write(r.ValueCommitmentX)
		_, _ = h.Write(r.ValueCommitmentY)
	}

	for _, r := range sortedByCommitment(outputs) {
		_, _ = h.Write(r.Commitment)
		_, _ = h.Write(r.ValueCommitmentX)
		_, _ = h.Write(r.ValueCommitmentY)
	}

	return h.Sum(nil)
}

// ComputeBVK computes the binding verification key:
//
//	bvk = Σcv_in − Σcv_out + valueBalance·V
//
// valueBalance corrects for non-conserved value flows, following the
// ZCash Sapling binding signature convention:
//
//   - Transfer (conserved values): valueBalance = 0
//   - Issuance (no inputs):        valueBalance = Σv_out
//
// When values are conserved the V-terms in Σcv_in and Σcv_out cancel,
// leaving bvk = bsk·R. For issuance the V-terms do NOT cancel;
// adding valueBalance·V restores the equality bvk = bsk·R so the
// Schnorr verification succeeds.
//
// NOTE: Currently BLS12-381/Jubjub only. BN254 support for binding
// signatures requires a parallel implementation using babyjubjub.
func ComputeBVK(inputs, outputs []ProofResult, valueBalance uint64) (twistededwards.PointAffine, error) {
	points := make([]twistededwards.PointAffine, 0, len(inputs)+len(outputs)+1)

	for _, r := range inputs {
		pt, err := ValueCommitPointBLS12381(r.ValueCommitmentX, r.ValueCommitmentY)
		if err != nil {
			return twistededwards.PointAffine{}, err
		}
		points = append(points, pt)
	}

	for _, r := range outputs {
		pt, err := ValueCommitPointBLS12381(r.ValueCommitmentX, r.ValueCommitmentY)
		if err != nil {
			return twistededwards.PointAffine{}, err
		}
		points = append(points, jubjub.NegatePoint(pt))
	}

	if valueBalance != 0 {
		var vbScalar fr.Element
		vbScalar.SetUint64(valueBalance)
		vbBig := vbScalar.BigInt(new(big.Int))

		var vbPoint twistededwards.PointAffine
		vbPoint.ScalarMultiplication(&jubjub.V, vbBig)
		points = append(points, vbPoint)
	}

	return jubjub.ValueCommitSum(points), nil
}

// ComputeBindingSignature computes the per-action binding signature proving
// value conservation: Σvalue_in == Σvalue_out.
//
// bsk = Σrcv_in − Σrcv_out is the private signing key, computable only
// because the caller collected every RCV used across this action's proofs.
// Signing uses jubjub.R as the group base point, matching the ZCash
// Sapling convention: bvk = Σcv_in − Σcv_out reduces to exactly bsk·R when
// values are conserved, and to something else entirely (unreachable by the
// prover, since nobody knows log_R(V)) when they are not
//
// For issuance (empty inputs), bsk becomes −Σrcv_out. There is no
// conservation requirement for issuance, the signature instead attests
// that the published output value commitments are correctly formed.
func ComputeBindingSignature(actionType string, typeCommitment []byte, inputs, outputs []ProofResult) (jubjub.Signature, error) {
	// bsk must be computed modulo the Jubjub subgroup order, NOT the
	// BLS12-381 scalar field modulus. fr.Element.Add/Sub reduce mod r
	// (the BLS12-381 scalar field), but the Schnorr signature scheme
	// operates over the Jubjub curve whose subgroup order is different.
	params := twistededwards.GetEdwardsCurve()
	order := &params.Order

	bskBig := new(big.Int)

	for _, r := range inputs {
		var rcv fr.Element
		if err := rcv.SetBytesCanonical(r.RCV); err != nil {
			return jubjub.Signature{}, fmt.Errorf("prover: invalid input RCV bytes: %w", err)
		}
		bskBig.Add(bskBig, rcv.BigInt(new(big.Int)))
	}

	for _, r := range outputs {
		var rcv fr.Element
		if err := rcv.SetBytesCanonical(r.RCV); err != nil {
			return jubjub.Signature{}, fmt.Errorf("prover: invalid output RCV bytes: %w", err)
		}
		bskBig.Sub(bskBig, rcv.BigInt(new(big.Int)))
	}

	bskBig.Mod(bskBig, order)

	var bsk fr.Element
	bsk.SetBigInt(bskBig)

	actionHash := ComputeActionHash(actionType, typeCommitment, inputs, outputs)

	sig, err := jubjub.Sign(bsk, actionHash, jubjub.R)
	if err != nil {
		return jubjub.Signature{}, fmt.Errorf("prover: binding signature generation failed: %w", err)
	}

	return sig, nil
}
