/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"fmt"
	"runtime"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
)

type Case struct {
	Workers    int
	Bits       uint64
	CurveID    math.CurveID
	NumInputs  int
	NumOutputs int
}

type TestCase struct {
	Name          string
	BenchmarkCase *Case
}

// GenerateCases returns all combinations of Case created
// from the provided slices of bits, curve IDs, number of inputs and outputs.
func GenerateCases(bits []uint64, curves []math.CurveID, inputs []int, outputs []int, workers []int) []TestCase {
	workers, inputs = generateCasesDefaults(workers, inputs)

	var cases []TestCase
	for _, w := range workers {
		for _, b := range bits {
			for _, c := range curves {
				for _, ni := range inputs {
					for _, no := range outputs {
						cases = append(cases, newTestCase(w, b, c, ni, no))
					}
				}
			}
		}
	}

	return cases
}

// generateCasesDefaults fills in GenerateCases' default workers/inputs slices when
// the caller passed nil for either.
func generateCasesDefaults(workers, inputs []int) ([]int, []int) {
	if workers == nil {
		workers = []int{runtime.NumCPU()}
	}
	if inputs == nil {
		inputs = []int{0}
	}

	return workers, inputs
}

// newTestCase builds the named TestCase for one combination of parameters.
func newTestCase(w int, b uint64, c math.CurveID, ni, no int) TestCase {
	name := fmt.Sprintf("Setup(bits %d, curve %s, #i %d, #o %d) with %d workers", b, math2.CurveIDToString(c), ni, no, w)

	return TestCase{
		Name: name,
		BenchmarkCase: &Case{
			Workers:    w,
			Bits:       b,
			CurveID:    c,
			NumInputs:  ni,
			NumOutputs: no,
		},
	}
}
