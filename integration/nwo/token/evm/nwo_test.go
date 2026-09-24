/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/stretchr/testify/assert"
)

// TestNetworkKeyIsInjective checks that a colon in the network or channel cannot make two distinct
// networks share a key, which would make nodeForNetwork reuse one chain for both.
func TestNetworkKeyIsInjective(t *testing.T) {
	a := networkKey(&topology.TMS{Network: "a:b", Channel: ""})
	b := networkKey(&topology.TMS{Network: "a", Channel: "b:"})
	assert.NotEqual(t, a, b)
}
