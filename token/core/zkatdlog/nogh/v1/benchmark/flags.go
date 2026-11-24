/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"flag"
	"fmt"
	"strings"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	executor "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/executor"
)

var (
	proofType = flag.String("proof_type", "1", "1 or bulletproof or bf, 2 or csp")
	// generatorDomain and legacyGenerators select the generator mode of the parameter-only
	// generator runs. They are mutually exclusive.
	generatorDomain  = flag.String("generator_domain", "", "generator domain; generates only the public parameter sets under testdata/generated")
	legacyGenerators = flag.Bool("legacy_generators", false, "use legacy-style generators; generates only the public parameter sets under testdata/generated")
	executorFlag     = flag.String("executor", "serial", "execution strategy for range proofs: serial, unbounded, pool")
)

// ProofType returns the proof type flag value (0 = RangeProof, 1 = CSPRangeProof).
func ProofType() rp.ProofType {
	str := *proofType
	if len(str) == 0 {
		return rp.RangeProofType
	}

	switch strings.ToLower(str) {
	case "1":
		return rp.RangeProofType
	case "bf", "bulletproof":
		return rp.RangeProofType
	case "2":
		return rp.CSPRangeProofType
	case "csp":
		return rp.CSPRangeProofType
	}
	panic(fmt.Errorf("invalid proof_type: %s", str))
}

// ExecutorProvider returns the ExecutorProvider selected via the -executor flag.
// Supported values:
//   - "serial" (default): tasks run inline, zero overhead
//   - "unbounded": one goroutine per task
//   - "pool": bounded pool of runtime.NumCPU() goroutines
func ExecutorProvider() executor.ExecutorProvider {
	str := *executorFlag
	if len(str) == 0 {
		return executor.SerialProvider{}
	}

	switch strings.ToLower(str) {
	case "serial":
		return executor.SerialProvider{}
	case "unbounded":
		return executor.UnboundedProvider{}
	case "pool":
		return executor.WorkerPoolProvider{}
	}
	panic(fmt.Errorf("invalid executor: %s (want serial, unbounded, or pool)", str))
}

// GeneratorDomain returns the value of the -generator_domain flag.
func GeneratorDomain() string {
	return *generatorDomain
}

// LegacyGenerators returns the value of the -legacy_generators flag.
func LegacyGenerators() bool {
	return *legacyGenerators
}
