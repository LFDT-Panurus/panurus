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

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/testutils"
	sbenchmark "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
)

//go:generate go run . -bits=32,64 -curves=BN254,BLS12_381_BBS_GURVY -num_inputs=1,2 -num_outputs=1,2
//go:generate go run . -proof_type=csp -bits=32,64 -curves=BN254,BLS12_381_BBS_GURVY -num_inputs=1,2 -num_outputs=1,2
func main() {
	flag.Parse()
	// The -proof_type flag (registered by the benchmark package) selects the
	// range-proof system: RangeProofType (IPA/bulletproof, the default) or
	// CSPRangeProofType (CSP). CSP vectors are written to a separate `csp`
	// subtree so they never overwrite the IPA vectors.
	proofType := benchmark.ProofType()

	// generate setup
	bits, curves, testCases, err := sbenchmark.GenerateCasesWithDefaults()
	if err != nil {
		panic(err)
	}
	configurations, err := benchmark.NewSetupConfigurationsWithParams(benchmark.SetupParams{
		IdemixTestdataPath: "./../../../../testdata",
		Bits:               bits,
		CurveIDs:           curves,
		OwnerIdentityType:  idemixnym.IdentityType,
		ProofType:          proofType,
	})
	if err != nil {
		panic(err)
	}
	rootDir := "./../../zero"
	if proofType == rp.CSPRangeProofType {
		rootDir = filepath.Join(rootDir, "csp")
	}
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

		// The scenarios below are single fixed-shape fixtures (gaps 1, 4, 5, 6, 7):
		// unlike the transfer/issue/redeem/swap sweep above, they don't depend on
		// num_inputs/num_outputs, so they are generated once per configuration
		// rather than once per input/output combination.
		log.Printf("generating fixed-shape scenario fixtures for configuration %s...\n", k)
		scenarioEnv, err := testutils.NewEnv(&sbenchmark.Case{
			Bits:       configuration.Bits,
			CurveID:    configuration.CurveID,
			NumInputs:  2,
			NumOutputs: 2,
		}, configurations)
		if err != nil {
			panic(err)
		}

		upgradeWitnessCase, err := scenarioEnv.UpgradeWitnessTransferToTestCase()
		if err != nil {
			panic(err)
		}
		pubMetadataIssueCase, err := scenarioEnv.PublicMetadataIssueToTestCase()
		if err != nil {
			panic(err)
		}
		pubMetadataTransferCase, err := scenarioEnv.PublicMetadataTransferToTestCase()
		if err != nil {
			panic(err)
		}
		unclaimedMetadataCase, err := scenarioEnv.UnclaimedMetadataToTestCase()
		if err != nil {
			panic(err)
		}
		multiAuditorCase, err := scenarioEnv.MultiAuditorTransferToTestCase()
		if err != nil {
			panic(err)
		}
		extraSignatureCase, err := scenarioEnv.ExtraSignatureToTestCase()
		if err != nil {
			panic(err)
		}

		allTestCases["upgrade_witness_0"] = upgradeWitnessCase
		allTestCases["pub_metadata_issue_0"] = pubMetadataIssueCase
		allTestCases["pub_metadata_transfer_0"] = pubMetadataTransferCase
		allTestCases["unclaimed_metadata_0"] = unclaimedMetadataCase
		allTestCases["multi_auditor_0"] = multiAuditorCase
		allTestCases["extra_signature_0"] = extraSignatureCase

		// Write single aggregated file for this configuration
		log.Printf("writing aggregated file for configuration %s...\n", k)
		if err := testutils.SaveAggregatedToFile(filepath.Join(configDir, "testdata.json"), allTestCases); err != nil {
			panic(err)
		}
	}

	// Gap 3 (open issuer/auditor policy): a dedicated sibling PP with an empty
	// PP.IssuerIDs, generated as its own configuration set and saved to its own
	// corpus subtree. Issuer policy is orthogonal to the range-proof system, so
	// this is only generated once, for the IPA/bulletproof proof type.
	if proofType == rp.RangeProofType {
		if err := generateOpenPolicyCorpus(bits, curves); err != nil {
			panic(err)
		}
	}
}

// generateOpenPolicyCorpus generates the gap-3 open-issuer-policy corpus
// (open_policy_issue_0 / open_policy_redeem_0 fixtures), one fixture pair per
// bits/curve configuration, saved under testdata/zero/open-policy/<config>/.
func generateOpenPolicyCorpus(bits []uint64, curves []math.CurveID) error {
	openPolicyConfigurations, err := benchmark.NewOpenIssuerPolicySetupConfigurations(benchmark.SetupParams{
		IdemixTestdataPath: "./../../../../testdata",
		Bits:               bits,
		CurveIDs:           curves,
		OwnerIdentityType:  idemixnym.IdentityType,
		ProofType:          rp.RangeProofType,
	})
	if err != nil {
		return err
	}

	openPolicyRootDir := filepath.Join("./../../zero", "open-policy")
	if err := openPolicyConfigurations.SaveTo(openPolicyRootDir); err != nil {
		return err
	}

	for k, configuration := range openPolicyConfigurations.Configurations {
		configDir := filepath.Join(openPolicyRootDir, k)
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return err
		}

		log.Printf("generating open-policy scenario fixtures for configuration %s...\n", k)
		env, err := testutils.NewOpenPolicyEnv(&sbenchmark.Case{
			Bits:       configuration.Bits,
			CurveID:    configuration.CurveID,
			NumInputs:  1,
			NumOutputs: 2,
		}, openPolicyConfigurations)
		if err != nil {
			return err
		}

		issueCase, err := env.OpenPolicyIssueToTestCase()
		if err != nil {
			return err
		}
		redeemCase, err := env.OpenPolicyRedeemToTestCase()
		if err != nil {
			return err
		}

		allTestCases := map[string]*testutils.TestCase{
			"open_policy_issue_0":  issueCase,
			"open_policy_redeem_0": redeemCase,
		}

		log.Printf("writing aggregated file for open-policy configuration %s...\n", k)
		if err := testutils.SaveAggregatedToFile(filepath.Join(configDir, "testdata.json"), allTestCases); err != nil {
			return err
		}
	}

	return nil
}
