/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package integration

import (
	"github.com/LFDT-Panurus/panurus/integration/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/onsi/ginkgo/v2"
)

// TestPortRange represents a port range
type TestPortRange integration.TestPortRange

const (
	SimpleTokenSelector    = "simple"
	SherdLockTokenSelector = "sherdlock"
)

var (
	TokenSelectors = []string{SimpleTokenSelector, SherdLockTokenSelector}
)

type InfrastructureType struct {
	Label             ginkgo.Labels
	CommType          fsc.P2PCommunicationType
	ReplicationFactor int
}

var (
	WebSocketNoReplication = &InfrastructureType{
		Label:             ginkgo.Label("websocket"),
		CommType:          fsc.WebSocket,
		ReplicationFactor: token.None,
	}
	WebSocketWithReplication = &InfrastructureType{
		Label:             ginkgo.Label("replicas"),
		CommType:          fsc.WebSocket,
		ReplicationFactor: 3,
	}
	LibP2PNoReplication = &InfrastructureType{
		Label:             ginkgo.Label("libp2p"),
		CommType:          fsc.LibP2P,
		ReplicationFactor: token.None,
	}

	WebSocketNoReplicationOnly = []*InfrastructureType{
		WebSocketNoReplication,
	}
	WebSocketWithReplicationOnly = []*InfrastructureType{
		WebSocketWithReplication,
	}
	LibP2PNoReplicationOnly = []*InfrastructureType{
		LibP2PNoReplication,
	}

	AllTestTypes = []*InfrastructureType{
		WebSocketNoReplication,
		LibP2PNoReplication,
		WebSocketWithReplication,
	}
)

const (
	BasePort integration.TestPortRange = integration.BasePort + integration.PortsPerSuite*iota

	ZKATDLogFungible
	ZKATDLogFungibleStress
	ZKATDLogFungibleHSM
	ZKATDLogFungibleCSP

	FabTokenFungible

	ZKATDLogDVP
	FabTokenDVP

	ZKATDLogNFT
	FabTokenNFT

	FabTokenInteropHTLC
	FabTokenInteropHTLCTwoFabricNetworks
	FabTokenInteropHTLCSwapNoCrossTwoFabricNetworks

	ZKATDLogInteropHTLC
	ZKATDLogInteropHTLCTwoFabricNetworks
	ZKATDLogInteropHTLCSwapNoCrossTwoFabricNetworks

	FabtokenEscrow
	ZKATDLogEscrow

	Mixed
	Updatability

	// The evm suites are appended rather than inserted: the ranges are positional, so adding one in
	// the middle would shift every suite after it onto different ports.
	//
	// evmSkipAirPlay stands between them and the range starting at 7000, which macOS binds to AirPlay
	// Receiver by default. Landing there made the suites fail on every Mac with a port conflict rather
	// than anything to do with the driver, which is a miserable first experience of the backend.
	evmSkipAirPlay
	EVMFungible
	EVMFungibleFabToken
)
