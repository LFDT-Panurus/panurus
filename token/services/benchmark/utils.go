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
	var cases []TestCase
	if workers == nil {
		workers = []int{runtime.NumCPU()}
	}
	if inputs == nil {
		inputs = []int{0}
	}

	for _, w := range workers {
		for _, b := range bits {
			for _, c := range curves {
				for _, ni := range inputs {
					for _, no := range outputs {
						name := fmt.Sprintf("Setup(bits %d, curve %s, #i %d, #o %d) with %d workers", b, math2.CurveIDToString(c), ni, no, w)
						cases = append(cases, TestCase{
							Name: name,
							BenchmarkCase: &Case{
								Workers:    w,
								Bits:       b,
								CurveID:    c,
								NumInputs:  ni,
								NumOutputs: no,
							},
						})
					}
				}
			}
		}
	}

	return cases
}
