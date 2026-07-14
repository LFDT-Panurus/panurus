/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/testutils"
	sbenchmark "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
)

//go:generate go run . -bits=32,64 -curves=BN254,BLS12_381_BBS_GURVY -num_inputs=1,2 -num_outputs=1,2
func main() {
	flag.Parse()
	// generate setup
	bits, curves, testCases, err := sbenchmark.GenerateCasesWithDefaults()
	if err != nil {
		panic(err)
	}
	configurations, err := benchmark.NewSetupConfigurations("./../../../../testdata", bits, curves, idemixnym.IdentityType)
	if err != nil {
		panic(err)
	}
	rootDir := "./../../zero"
	if err := configurations.SaveTo(rootDir); err != nil {
		panic(err)
	}

	for k, configuration := range configurations.Configurations {
		// Create output directory for this configuration
		configDir := filepath.Join(rootDir, k)
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			panic(err)
		}

		// Create a single map to collect all test cases for this configuration
		allTestCases := make(map[string]*testutils.TestCase)

		// Mutex to protect concurrent map writes
		var mu sync.Mutex

		// Generate test cases for all combinations
		for _, testCase := range testCases {
			// Create worker pool with number of CPUs
			numWorkers := runtime.NumCPU()
			taskChan := make(chan int, 64)
			var wg sync.WaitGroup

			// Start workers
			for w := 0; w < numWorkers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range taskChan {
						log.Printf("generate [%d]-th env for [bits=%d,curveID=%d,inputs=%d,outputs=%d]...\n",
							i,
							configuration.Bits, configuration.CurveID, testCase.BenchmarkCase.NumInputs, testCase.BenchmarkCase.NumOutputs,
						)
						env, err := testutils.NewEnv(&sbenchmark.Case{
							Bits:       configuration.Bits,
							CurveID:    configuration.CurveID,
							NumInputs:  testCase.BenchmarkCase.NumInputs,
							NumOutputs: testCase.BenchmarkCase.NumOutputs,
						}, configurations)
						if err != nil {
							panic(err)
						}

						// Convert to test cases with labeled keys
						transferCase, err := env.TransferToTestCase()
						if err != nil {
							panic(err)
						}
						issueCase, err := env.IssueToTestCase()
						if err != nil {
							panic(err)
						}
						redeemCase, err := env.RedeemToTestCase()
						if err != nil {
							panic(err)
						}
						swapCase, err := env.SwapToTestCase()
						if err != nil {
							panic(err)
						}

						// Store in map with labeled keys
						mu.Lock()
						transferKey := fmt.Sprintf("transfers_i%d_o%d_%d", testCase.BenchmarkCase.NumInputs, testCase.BenchmarkCase.NumOutputs, i)
						issueKey := fmt.Sprintf("issues_i%d_o%d_%d", testCase.BenchmarkCase.NumInputs, testCase.BenchmarkCase.NumOutputs, i)
						redeemKey := fmt.Sprintf("redeems_i%d_o%d_%d", testCase.BenchmarkCase.NumInputs, testCase.BenchmarkCase.NumOutputs, i)
						swapKey := fmt.Sprintf("swaps_i%d_o%d_%d", testCase.BenchmarkCase.NumInputs, testCase.BenchmarkCase.NumOutputs, i)

						allTestCases[transferKey] = transferCase
						allTestCases[issueKey] = issueCase
						allTestCases[redeemKey] = redeemCase
						allTestCases[swapKey] = swapCase
						mu.Unlock()
					}
				}()
			}

			// Queue tasks
			for i := range 64 {
				taskChan <- i
			}
			close(taskChan)

			// Wait for all tasks to complete
			wg.Wait()
		}

		// Write single aggregated file for this configuration
		log.Printf("writing aggregated file for configuration %s...\n", k)
		if err := testutils.SaveAggregatedToFile(filepath.Join(configDir, "testdata.json"), allTestCases); err != nil {
			panic(err)
		}
	}
}
