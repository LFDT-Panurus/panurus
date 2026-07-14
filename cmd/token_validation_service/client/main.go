/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0


The Flow
  Client                                  Server
  ──────                                  ──────
  1. Load proof data from disk
     (pub params + token request)
          │
  2. JSON-serialize params               3. Receive gRPC request
     (includes WireProofData)  ──gRPC──►     with name="zkp"
                                          │
                                       4. TokenValidationServiceViewFactory.NewView(jsonBytes)
                                          → deserialize WireProofData → ProofData
                                          → create token.Validator from public params
                                          │
                                       5. TransferServiceView.Call()
                                          → validator.UnmarshallAndVerifyWithMetadata(...)
                                            (full pipeline: auditing, signatures, ZK proofs,
                                             HTLC, upgrade witnesses, metadata checks)
                                          │
  6. Receive response            ◄────  7. Return result (nil, nil) on success
     Record latency/throughput


*/

package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"runtime"
	"time"

	bench "github.com/LFDT-Panurus/panurus/cmd/token_validation_service"
	"github.com/hyperledger-labs/fabric-smart-client/integration/benchmark/node"
	"google.golang.org/grpc/benchmark/flags"
)

var (
	numConn      = flags.IntSlice("numConn", []int{1, 2}, "Number of grpc client connections - may be a comma-separated list")
	numWorker    = flags.IntSlice("cpu", []int{1, 2, 4, 8}, "Number of concurrent worker - may be a comma-separated list")
	workloadFlag = flags.StringSlice("workloads", []string{"sign"}, "Workloads to execute - may be a comma-separated list")
	warmupDur    = flag.Duration("warmup", 5*time.Second, "Warmup duration")
	duration     = flag.Duration("benchtime", 10*time.Second, "Duration for every execution")
	count        = flag.Int("count", 1, "Number of executions")
)

func main() {
	flag.Parse()

	runtime.GOMAXPROCS(runtime.NumCPU())

	testdataPath := "./out/testdata" // for local debugging you can set testdataPath := "out/testdata"
	clientConfPath := path.Join(testdataPath, "fsc", "nodes", "test-node.0", "client-config.yaml")

	paramsSlice, err := bench.NewTokenValidationParamsSlice(bench.DefaultTestRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load test parameters: %v\n", err)
		os.Exit(1)
	}
	params := paramsSlice[0]
	fmt.Println("Sending Pre-computing ZK proof...")

	zkpWorkload := node.Workload{
		Name:    "token-validation-service",
		Factory: &bench.TokenValidationServiceViewFactory{},
		Params:  params,
	}

	selected := make([]node.Workload, 0, len(*workloadFlag))
	selected = append(selected, zkpWorkload)

	node.RunRemoteBenchmarkSuite(node.RemoteBenchmarkConfig{
		Workloads:      selected,
		ClientConfPath: clientConfPath,
		ConnCounts:     *numConn,
		WorkerCounts:   *numWorker,
		WarmupDur:      *warmupDur,
		BenchTime:      *duration,
		Count:          *count,
		BenchName:      "BenchmarkAPIGRPCRemote",
	})
}
