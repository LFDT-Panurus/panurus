/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package bench

import (
	"path"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/integration/benchmark/node"
	viewregistry "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/stretchr/testify/require"
)

func BenchmarkAPI(b *testing.B) {
	testdataPath := b.TempDir()
	nodeConfPath := path.Join(testdataPath, "fsc", "nodes", "test-node.0")

	err := generateConfigWS(testdataPath)
	require.NoError(b, err)

	n, err := setupNodeP2P(nodeConfPath, node.NamedFactory{
		Name:    "token-validation-service",
		Factory: &TokenValidationServiceViewFactory{},
	})

	require.NoError(b, err)

	defer n.Stop()

	vm, err := viewregistry.GetManager(n)
	require.NoError(b, err)

	paramsSlice, err := NewTokenValidationParamsSlice(DefaultTestRoot)
	require.NoError(b, err)

	wl := node.Workload{
		Name:    "token-validation-service",
		Factory: &TokenValidationServiceViewFactory{},
		Params:  paramsSlice[0],
	}

	b.ResetTimer()
	node.RunAPIBenchmark(b, vm, wl)
}
