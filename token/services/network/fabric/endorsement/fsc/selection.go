/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"math/rand"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// SelectEndorsersForMSPSets picks a random policy-satisfying set of MSP IDs out of
// candidates (each entry lists the MSP IDs whose endorsement is jointly required to
// satisfy the namespace endorsement policy), then selects, for each required MSP ID in
// that set, one random configured endorser belonging to it. mspOf resolves a configured
// identity to the MSP ID it belongs to.
//
// It returns an error if none of the candidate sets can be fully covered by the
// configured endorsers.
func SelectEndorsersForMSPSets(configured []view.Identity, mspOf func(view.Identity) (string, error), candidates [][]string) ([]view.Identity, error) {
	if len(candidates) == 0 {
		return nil, errors.Errorf("no candidate MSP set to satisfy the namespace endorsement policy")
	}

	byMSP := make(map[string][]view.Identity)
	for _, id := range configured {
		mspID, err := mspOf(id)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to resolve MSP for configured endorser [%s]", id)
		}
		byMSP[mspID] = append(byMSP[mspID], id)
	}

	for _, idx := range rand.Perm(len(candidates)) {
		requiredMSPIDs := candidates[idx]
		selected := make([]view.Identity, 0, len(requiredMSPIDs))
		satisfied := true
		for _, mspID := range requiredMSPIDs {
			pool := byMSP[mspID]
			if len(pool) == 0 {
				satisfied = false

				break
			}
			selected = append(selected, pool[rand.Intn(len(pool))])
		}
		if satisfied {
			return selected, nil
		}
	}

	return nil, errors.Errorf("no configured endorser covers any of the [%d] policy-satisfying MSP set(s)", len(candidates))
}
