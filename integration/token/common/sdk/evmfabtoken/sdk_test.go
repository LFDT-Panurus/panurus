/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evmfabtoken

import (
	"testing"

	tokensdk "github.com/LFDT-Panurus/panurus/token/sdk/dig"
	dig2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/sdk/dig"
	sdk "github.com/hyperledger-labs/fabric-smart-client/platform/view/sdk/dig"
	"github.com/stretchr/testify/require"
)

// TestEVMWiring is the gate for the composition: the container must resolve every dependency the EVM
// network driver declares, alongside the fabtoken driver. A driver that cannot be constructed by the
// container would fail at node start rather than here, and only once a real network was configured.
//
// There is no fabric platform in the graph, which is the point: an EVM node needs the view SDK and the
// token SDK, nothing from Fabric.
func TestEVMWiring(t *testing.T) {
	require.NoError(t, sdk.DryRunWiring(
		func(sdk dig2.SDK) *SDK { return NewFrom(tokensdk.NewFrom(sdk)) },
		sdk.WithBool("token.enabled", true),
	))
}
