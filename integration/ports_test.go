/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllTestTypesIncludesRequiredInfrastructureTypes guards against
// accidentally dropping one of the required infrastructure types from
// AllTestTypes, which would silently narrow integration test coverage.
func TestAllTestTypesIncludesRequiredInfrastructureTypes(t *testing.T) {
	required := []*InfrastructureType{
		WebSocketNoReplication,
		LibP2PNoReplication,
		WebSocketWithReplication,
	}

	for _, r := range required {
		assert.Contains(t, AllTestTypes, r, "AllTestTypes must always include %v", r.Label)
	}
}
