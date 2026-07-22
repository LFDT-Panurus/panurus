/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/stretchr/testify/require"
)

func TestEnableTokenPlatform(t *testing.T) {
	fscTopology := fsc.NewTopology()
	fscTopology.AddNodeByName("alice")
	fscTopology.AddNodeByName("bob")

	tokenTopology := NewTopology()
	tokenTopology.EnableTokenPlatform(fscTopology)

	require.Empty(t, tokenTopology.TMSs)
	require.Len(t, tokenTopology.TokenPlatformNodes, 2)

	var names []string
	for _, n := range tokenTopology.TokenPlatformNodes {
		names = append(names, n.Name)
	}
	require.ElementsMatch(t, []string{"alice", "bob"}, names)
}
