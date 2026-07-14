/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/benchmark"
)

func TestRunBenchmark(t *testing.T) {
	// 1. Define Setup (Heavy, excluded)
	setup := func() []byte {
		// Simulate expensive database fetch or allocation
		time.Sleep(2 * time.Millisecond)

		return make([]byte, 1024)
	}

	// 2. Define Work (The target of measurement)
	work := func(data []byte) error {
		// Simulate processing
		time.Sleep(500 * time.Microsecond)
		_ = len(data)

		return nil
	}

	fmt.Println("Running RunBenchmark...")
	res := benchmark.RunBenchmark(benchmark.NewConfig(8, 2*time.Second, 5*time.Second), setup, work) // 8 workers, 2 seconds
	res.Print()
}
