/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	"github.com/LFDT-Panurus/panurus/token/driver"
	sbenchmark "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnv(t *testing.T) {
	bits, err := sbenchmark.Bits(32, 64)
	require.NoError(t, err)
	curves := sbenchmark.Curves(math.BN254, math.BLS12_381_BBS_GURVY)
	require.NoError(t, err)
	testCases := sbenchmark.GenerateCases(bits, curves, []int{1, 2}, []int{1, 2}, []int{1})
	configurations, err := benchmark.NewSetupConfigurations("./../testdata", bits, curves, idemixnym.IdentityType)
	if err != nil {
		panic(err)
	}
	for _, configuration := range configurations.Configurations {
		// generate the validator env for transfer
		for _, testCase := range testCases {
			// Create worker pool with number of CPUs
			numWorkers := runtime.NumCPU()
			numTasks := 3
			taskChan := make(chan int, numTasks)
			var wg sync.WaitGroup

			// Start workers
			for range numWorkers {
				wg.Go(func() {
					for i := range taskChan {
						_, err := NewEnv(&sbenchmark.Case{
							Bits:       configuration.Bits,
							CurveID:    configuration.CurveID,
							NumInputs:  testCase.BenchmarkCase.NumInputs,
							NumOutputs: testCase.BenchmarkCase.NumOutputs,
						}, configurations)
						assert.NoError(t, err, "failed to generate [%d]-th env for [bits=%d,curveID=%d,inputs=%d,outputs=%d]",
							i,
							configuration.Bits, configuration.CurveID, testCase.BenchmarkCase.NumInputs, testCase.BenchmarkCase.NumOutputs,
						)
					}
				})
			}

			// Queue tasks
			for i := range numTasks {
				taskChan <- i
			}
			close(taskChan)

			// Wait for all tasks to complete
			wg.Wait()
		}
	}
}

func TestSaveTransferToFile(t *testing.T) {
	metadata := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{
				ActionID: 1,
				IssueMetadata: &driver.IssueMetadata{
					Issuer: driver.AuditableIdentity{
						Identity:  []byte{1, 2, 3, 4, 5},
						AuditInfo: []byte{6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
					},
					Inputs:       []*driver.IssueInputMetadata{},
					Outputs:      []*driver.IssueOutputMetadata{},
					ExtraSigners: []driver.AuditableIdentity{},
				},
			},
		},
		Application: nil,
	}
	inputs := map[string]*token.Token{
		token.ID{TxId: "a_transaction_id", Index: 1}.String(): {
			Owner:    []byte("an_owner"),
			Type:     "a_type",
			Quantity: "a_quantity",
		},
	}
	e := &Env{
		TRWithTransferTxID:     "tx123",
		TRWithTransferRaw:      []byte{1, 2, 3, 4, 5},
		TRWithTransferMetadata: metadata,
		TRWithTransferInputs:   inputs,
	}
	path := filepath.Join(t.TempDir(), "transfer.json")
	err := e.SaveTransferToFile(path)
	require.NoError(t, err)

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	var payload TestCase
	err = json.Unmarshal(b, &payload)
	require.NoError(t, err)
	require.Equal(t, e.TRWithTransferTxID, payload.TxID)
	// Verify req_raw
	decoded, err := base64.StdEncoding.DecodeString(payload.ReqRaw)
	require.NoError(t, err)
	require.Equal(t, e.TRWithTransferRaw, decoded)
	// Verify metadata: decode base64, then deserialize and compare
	metadataBytes, err := base64.StdEncoding.DecodeString(payload.Metadata)
	require.NoError(t, err)
	decodedMetadata := &driver.TokenRequestMetadata{}
	require.NoError(t, decodedMetadata.FromBytes(metadataBytes))
	require.Equal(t, metadata, decodedMetadata)
	// Verify inputs: compare the underlying map
	require.Equal(t, inputs, payload.Inputs.Inputs)
}
