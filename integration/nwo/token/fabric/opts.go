/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
)

// WithFabricCA notifies the backend to activate fabric-ca for the issuance of identities
func WithFabricCA(tms *topology.TMS) {
	tms.BackendParams["fabricca"] = true
}

// IsFabricCA return true if this TMS requires to enable Fabric-CA
func IsFabricCA(tms *topology.TMS) bool {
	boxed, ok := tms.BackendParams["fabricca"]
	if ok {
		return boxed.(bool)
	}

	return false
}

// WithFSCEndorsers tells the backend to use FSC-based endorsement for the passed TMS using the given FSC endorsers identifiers
func WithFSCEndorsers(tms *topology.TMS, endorsers ...string) *topology.TMS {
	tms.BackendParams["endorsements"] = true
	tms.BackendParams["endorsers"] = endorsers

	return tms
}

// IsFSCEndorsementEnabled returns true if the FSC-based endorsement for the given TMS is enabled, false otherwise
func IsFSCEndorsementEnabled(tms *topology.TMS) bool {
	v, ok := tms.BackendParams["endorsements"]

	return ok && v.(bool)
}

// WithEndorserRole tells the backed that a node with this option plays the role of endorser
func WithEndorserRole() node.Option {
	return func(o *node.Options) error {
		to := topology.ToOptions(o)
		to.SetEndorser(true)

		return nil
	}
}

func Endorsers(tms *topology.TMS) []string {
	v, ok := tms.BackendParams["endorsers"]
	if !ok {
		return nil
	}

	return v.([]string)
}
