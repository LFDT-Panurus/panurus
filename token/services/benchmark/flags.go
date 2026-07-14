/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"flag"
	"runtime"
	"strconv"
	"strings"
	"time"

	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	"golang.org/x/exp/constraints"
)

var (
	bits         = flag.String("bits", "", "a comma-separated list of bit sizes (32, 64,...)")
	duration     = flag.Duration("duration", 1*time.Second, "test duration (1s, 1m, 1h,...)")
	curves       = flag.String("curves", "", "comma-separated list of curves. Supported curves are: BN254, BLS12_381_BBS_GURVY, BLS12_381_BBS_GURVY_FAST_RNG")
	numInputs    = flag.String("num_inputs", "", "a comma-separate list of number of inputs (1,2,3,...)")
	numOutputs   = flag.String("num_outputs", "", "a comma-separate list of number of outputs (1,2,3,...)")
	workers      = flag.String("workers", "", "a comma-separate list of workers (1,2,3,...,NumCPU), where NumCPU is converted to the number of available CPUs")
	profile      = flag.Bool("profile", false, "write pprof profiles to file")
	setupSamples = flag.Uint("setup_samples", 0, "number of setup samples, 0 disables it")
)

// Bits parses the package-level `-bits` flag and returns a slice of bit sizes.
// If the flag is empty the provided defaults are returned.
// The returned values are uint64 and an error is returned if parsing fails.
func Bits(defaults ...uint64) ([]uint64, error) {
	return Integers[uint64](*bits, defaults...)
}

// Duration returns the parsed package-level `-duration` flag as a time.Duration.
// If the flag is zero, Duration returns 1 second as a sane default.
func Duration() time.Duration {
	d := *duration
	if d == 0 {
		return 1 * time.Second
	}

	return d
}

// Curves parses the package-level `-curves` flag and returns a slice of math.CurveID.
// The flag may contain comma-separated numeric curve IDs or known curve names
// (e.g. "BN254", "BLS12_381_BBS_GURVY", "BLS12_381_BBS_GURVY_FAST_RNG").
// If the flag is empty, the provided curveIDs are returned as defaults.
func Curves(curveIDs ...math.CurveID) []math.CurveID {
	str := *curves
	if len(str) == 0 {
		return curveIDs
	}

	components := strings.Split(str, ",")
	values := make([]math.CurveID, 0, len(components))
	for _, s := range components {
		s = strings.TrimSpace(s)
		v, err := strconv.Atoi(s)
		if err == nil {
			values = append(values, math.CurveID(v))

			continue
		}

		values = append(values, math2.StringToCurveID(s))
	}

	return values
}

// NumInputs parses the package-level `-num_inputs` flag and returns a slice of ints.
// If the flag is empty the provided defaults are returned. Parsing errors are returned.
func NumInputs(defaults ...int) ([]int, error) {
	return Integers[int](*numInputs, defaults...)
}

// NumOutputs parses the package-level `-num_outputs` flag and returns a slice of ints.
// If the flag is empty the provided defaults are returned. Parsing errors are returned.
func NumOutputs(defaults ...int) ([]int, error) {
	return Integers[int](*numOutputs, defaults...)
}

// Workers parses the package-level `-workers` flag and returns a slice of worker counts.
// The flag accepts comma-separated integers and the special token "NumCPU" which is
// translated to the runtime.NumCPU() value. If the flag is empty the provided defaults
// are returned. Parsing errors are returned.
func Workers(defaults ...int) ([]int, error) {
	str := *workers
	if len(str) == 0 {
		return defaults, nil
	}
	components := strings.Split(str, ",")
	values := make([]int, 0, len(components))
	for _, s := range components {
		s = strings.TrimSpace(s)
		v, err := strconv.Atoi(s)
		if err != nil {
			if s == "NumCPU" {
				v = runtime.NumCPU()
			} else {
				return nil, err
			}
		}
		values = append(values, v)
	}

	return values, nil
}

// Integers is a generic helper that parses a comma-separated string of unsigned
// integers into a slice of type T (which must be an integer type). If the input
// string is empty the provided defaults are returned. It trims whitespace and
// returns a parsing error on invalid numeric values.
func Integers[T constraints.Integer](str string, defaults ...T) ([]T, error) {
	if len(str) == 0 {
		return defaults, nil
	}

	components := strings.Split(str, ",")
	values := make([]T, 0, len(components))
	for _, s := range components {
		s = strings.TrimSpace(s)
		v, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return nil, err
		}
		values = append(values, T(v))
	}

	return values, nil
}

// ProfileEnabled returns true if profiling has been requested, false otherwise
func ProfileEnabled() bool {
	return *profile
}

// SetupSamples returns the number of setup samples to use. When 0, a setup will be generated for each evaluation.
func SetupSamples() uint {
	return *setupSamples
}
