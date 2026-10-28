/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri

import (
	"runtime"
	"sync"
)

// parallelize splits nbIterations into contiguous [start, end) ranges and runs
// work on each range in its own goroutine, waiting for all of them. At most
// maxCPUs[0] goroutines are used when given, runtime.NumCPU() otherwise. It is a
// copy of gnark's internal/utils.Parallelize, which is not importable outside
// the gnark module.
func parallelize(nbIterations int, work func(int, int), maxCPUs ...int) {
	nbTasks := runtime.NumCPU()
	if len(maxCPUs) == 1 {
		nbTasks = maxCPUs[0]
	}
	nbIterationsPerCPU := nbIterations / nbTasks

	// more CPUs than tasks: a CPU will work on exactly one iteration
	if nbIterationsPerCPU < 1 {
		nbIterationsPerCPU = 1
		nbTasks = nbIterations
	}

	var wg sync.WaitGroup
	extraTasks := nbIterations - (nbTasks * nbIterationsPerCPU)
	extraTasksOffset := 0

	for i := range nbTasks {
		start := i*nbIterationsPerCPU + extraTasksOffset
		end := start + nbIterationsPerCPU
		if extraTasks > 0 {
			end++
			extraTasks--
			extraTasksOffset++
		}
		wg.Go(func() {
			work(start, end)
		})
	}

	wg.Wait()
}
