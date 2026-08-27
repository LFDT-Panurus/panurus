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
// A candidate set may list the same MSP ID more than once: a policy such as
// AND(Org1MSP.member, Org1MSP.member) requires that many *distinct* signers from that MSP,
// which is exactly the property protecting against a single misbehaving endorser within the
// organization. Every returned identity is therefore distinct by identity bytes: a slot is
// never filled with an identity already selected for an earlier slot of the same set, and
// duplicate entries in configured collapse to a single candidate endorser. The result always
// has exactly as many elements as the chosen candidate set.
//
// A candidate set requiring more distinct signers from an MSP than there are distinct
// configured endorsers in it cannot be satisfied and is skipped, like one naming an MSP with
// no configured endorser at all.
//
// It returns an error if none of the candidate sets can be fully covered by distinct
// configured endorsers. The error names the first MSP that blocked each candidate set.
//
// An empty (but present) candidate set — one requiring no MSP at all — is rejected as
// malformed policy input rather than treated as vacuously satisfied: a policy satisfiable
// by zero endorsers would let a transaction be endorsed by nobody, so it must fail loudly.
func SelectEndorsersForMSPSets(configured []view.Identity, mspOf func(view.Identity) (string, error), candidates [][]string) ([]view.Identity, error) {
	if err := validateCandidates(candidates); err != nil {
		return nil, err
	}

	byMSP, skipped := bucketConfiguredByMSP(configured, mspOf)

	selected, ok, setFailures := trySelectFromCandidates(byMSP, candidates)
	if ok {
		return selected, nil
	}

	return nil, errors.Join(
		errors.Errorf("no configured endorser covers any of the [%d] policy-satisfying MSP set(s) with a distinct endorser per required signer", len(candidates)),
		errors.Join(setFailures...),
		errors.Join(skipped...),
		errors.Errorf("failed to resolve MSP"),
	)
}

// validateCandidates rejects a candidates slice that cannot possibly select
// any endorsers: no MSP sets at all, or a set requiring zero endorsers.
func validateCandidates(candidates [][]string) error {
	if len(candidates) == 0 {
		return errors.Errorf("no candidate MSP set to satisfy the namespace endorsement policy")
	}
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			return errors.Errorf("malformed endorsement policy: a candidate MSP set requires no endorser, which would be satisfied by an empty selection")
		}
	}

	return nil
}

// bucketConfiguredByMSP resolves each configured endorser's MSP and buckets
// it by MSP ID, deduplicating repeats and skipping (with the resolution
// error recorded) any identity whose MSP cannot be resolved.
func bucketConfiguredByMSP(configured []view.Identity, mspOf func(view.Identity) (string, error)) (map[string][]view.Identity, []error) {
	var skipped []error
	byMSP := make(map[string][]view.Identity)
	seen := make(map[string]struct{}, len(configured))
	for _, id := range configured {
		mspID, err := mspOf(id)
		if err != nil {
			skipped = append(skipped, err)
			logger.Warnf("skipping unresolvable MSP for configured endorser [%s]: %v", id, err)

			continue
		}
		if _, ok := seen[string(id)]; ok {
			// configured may legitimately list the same endorser twice; it still provides a
			// single endorsement, so bucket it once.
			continue
		}
		seen[string(id)] = struct{}{}

		byMSP[mspID] = append(byMSP[mspID], id)
	}

	return byMSP, skipped
}

// trySelectFromCandidates tries each candidate MSP set, in random order, for a
// distinct-endorser-per-slot selection, returning the first one that succeeds
// (ok=true). If every attempt fails, ok is false and each attempt's failure
// is accumulated into setFailures.
func trySelectFromCandidates(byMSP map[string][]view.Identity, candidates [][]string) ([]view.Identity, bool, []error) {
	var setFailures []error
	for _, idx := range rand.Perm(len(candidates)) {
		selected, failedMSP, ok := selectDistinctForMSPSet(byMSP, candidates[idx])
		if ok {
			return selected, true, nil
		}
		required := 0
		for _, id := range candidates[idx] {
			if id == failedMSP {
				required++
			}
		}
		available := len(byMSP[failedMSP])
		setFailures = append(setFailures, errors.Errorf("MSP [%s] requires %d distinct endorser(s) but only %d configured", failedMSP, required, available))
	}

	return nil, false, setFailures
}

// selectDistinctForMSPSet fills one slot per entry of requiredMSPIDs with a random
// configured endorser of that MSP, never reusing an identity already selected for an
// earlier slot of the same set. It returns (selected, "", true) on success, or
// (nil, failedMSP, false) where failedMSP is the first MSP ID whose pool was exhausted,
// meaning the set is not coverable by distinct endorsers.
//
// A greedy per-slot pick is complete here: every slot requiring a given MSP ID draws from
// the same pool, so the only way this fails is that some MSP ID appears in requiredMSPIDs
// more times than that MSP has distinct configured endorsers — genuinely unsatisfiable
// whatever the order of the picks. No backtracking is needed.
func selectDistinctForMSPSet(byMSP map[string][]view.Identity, requiredMSPIDs []string) ([]view.Identity, string, bool) {
	used := make(map[string]struct{}, len(requiredMSPIDs))
	selected := make([]view.Identity, 0, len(requiredMSPIDs))
	for _, mspID := range requiredMSPIDs {
		pool := byMSP[mspID]
		available := make([]view.Identity, 0, len(pool))
		for _, id := range pool {
			if _, ok := used[string(id)]; !ok {
				available = append(available, id)
			}
		}
		if len(available) == 0 {
			return nil, mspID, false
		}
		id := available[rand.Intn(len(available))]
		used[string(id)] = struct{}{}
		selected = append(selected, id)
	}

	return selected, "", true
}
