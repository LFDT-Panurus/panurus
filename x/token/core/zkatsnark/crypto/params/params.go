/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package params

import (
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"
)

// CurveID is the default outer pairing curve.
// All arithmetic in circuits and native Go uses this curve's scalar field.
const DefaultCurve = ecc.BLS12_381

// DefaultProofSystem is the ZK proof system used unless overridden.
const DefaultProofSystem = "groth16"

// DefaultMaxBits is the default bit-width of the token value field.
// Value range is [1, 2^64 - 1] by default.
const DefaultMaxBits = 64

// PointAffine is the Jubjub curve point type (twisted Edwards embedded in BLS12-381 Fr).
// Re-exported here so callers only import this package, not gnark-crypto directly.
type PointAffine = twistededwards.PointAffine

// CircuitType identifies which circuit a proving/verification key belongs to.
type CircuitType string

const (
	CircuitSpend     CircuitType = "spend"
	CircuitOutput    CircuitType = "output"
	CircuitMigration CircuitType = "migration"
)
