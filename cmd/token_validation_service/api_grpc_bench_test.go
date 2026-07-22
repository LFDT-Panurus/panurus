/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"path"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/integration/benchmark/node"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/benchmark/flags"
)

var numConn = flags.IntSlice("numConn", []int{1, 2, 4, 8}, "Number of grpc client connections - may be a comma-separated list")

// BenchmarkAPIGRPC exercises the ViewAPI via grpc client.
// The proof is pre-computed and embedded in the workload params so every
// gRPC request carries it.
func BenchmarkAPIGRPC(b *testing.B) {
	testdataPath := b.TempDir()
	nodeConfPath := path.Join(testdataPath, "fsc", "nodes", "test-node.0")
	clientConfPath := path.Join(nodeConfPath, "client-config.yaml")

	err := GenerateConfigWS(testdataPath)
	require.NoError(b, err)

	n, err := SetupNodeP2P(nodeConfPath, node.NamedFactory{
		Name:    "token-validation-service",
		Factory: &TokenValidationServiceViewFactory{},
	})

	require.NoError(b, err)

	defer n.Stop()

	paramsSlice, err := NewTokenValidationParamsSlice(*TestRoot)
	require.NoError(b, err)

	wl := node.Workload{
		Name:    "token-validation-service",
		Factory: &TokenValidationServiceViewFactory{},
		Params:  paramsSlice[0],
	}

	node.RunAPIGRPCBenchmark(b, wl, clientConfPath, *numConn)
}
