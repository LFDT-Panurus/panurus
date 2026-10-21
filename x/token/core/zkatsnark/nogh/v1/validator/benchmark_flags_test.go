/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"flag"
	"strings"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// defaultBenchmarkProofSystem is used when -proof_system is not set.
const defaultBenchmarkProofSystem = "groth16"

var benchProofSystem = flag.String("proof_system", "", "ZK proof system to benchmark: groth16 or plonk (default groth16)")

// benchmarkProofSystem returns the proof system selected via the
// -proof_system flag, falling back to defaultBenchmarkProofSystem when the
// flag is not set.
func benchmarkProofSystem() (string, error) {
	str := *benchProofSystem
	if len(str) == 0 {
		return defaultBenchmarkProofSystem, nil
	}

	switch strings.ToLower(str) {
	case "groth16", "plonk":
		return strings.ToLower(str), nil
	default:
		return "", errors.Errorf("invalid proof_system [%s]: want groth16 or plonk", str)
	}
}

// curveFromCaseID maps a benchmark.Case's mathlib CurveID (selected via the
// shared -curves flag, see token/services/benchmark.Curves) to the
// gnark-crypto ecc.ID the zkatsnark driver understands. Every BLS12-381
// mathlib variant maps to the single gnark-crypto ecc.BLS12_381.
func curveFromCaseID(id mathlib.CurveID) (ecc.ID, error) {
	switch id {
	case mathlib.BN254:
		return ecc.BN254, nil
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		return ecc.BLS12_381, nil
	default:
		return 0, errors.Errorf("unsupported curve ID [%d] for zkatsnark benchmarks: want BN254 or a BLS12_381 variant", id)
	}
}
