/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectEndorsersForMSPSets(t *testing.T) {
	org1Endorser1 := view.Identity("org1-endorser1")
	org1Endorser2 := view.Identity("org1-endorser2")
	org2Endorser1 := view.Identity("org2-endorser1")
	org3Endorser1 := view.Identity("org3-endorser1")
	staleEndorser := view.Identity("stale-endorser")

	mspOf := func(id view.Identity) (string, error) {
		switch id.String() {
		case org1Endorser1.String(), org1Endorser2.String():
			return "Org1MSP", nil
		case org2Endorser1.String():
			return "Org2MSP", nil
		case org3Endorser1.String():
			return "Org3MSP", nil
		default:
			return "", errors.Errorf("unknown identity [%s]", id)
		}
	}

	t.Run("single candidate, fully covered", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org2Endorser1}

		selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP", "Org2MSP"}})

		require.NoError(t, err)
		assert.Len(t, selected, 2)
		mspIDs := mspIDsOf(t, mspOf, selected)
		assert.ElementsMatch(t, []string{"Org1MSP", "Org2MSP"}, mspIDs)
	})

	t.Run("picks one endorser per required MSP even with multiple candidates in a MSP", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org1Endorser2}

		selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP"}})

		require.NoError(t, err)
		require.Len(t, selected, 1)
		assert.Contains(t, []string{org1Endorser1.String(), org1Endorser2.String()}, selected[0].String())
	})

	t.Run("multiple candidates, only one satisfiable, returns that one", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1}

		selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{
			{"Org2MSP"},
			{"Org1MSP"},
			{"Org3MSP"},
		})

		require.NoError(t, err)
		require.Len(t, selected, 1)
		assert.Equal(t, org1Endorser1.String(), selected[0].String())
	})

	t.Run("multiple satisfiable candidates, returns a valid one", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org2Endorser1, org3Endorser1}
		candidates := [][]string{{"Org1MSP"}, {"Org2MSP"}, {"Org3MSP"}}

		for range 20 {
			selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, candidates)
			require.NoError(t, err)
			require.Len(t, selected, 1)
			mspID, err := mspOf(selected[0])
			require.NoError(t, err)
			assert.Contains(t, []string{"Org1MSP", "Org2MSP", "Org3MSP"}, mspID)
		}
	})

	t.Run("unsatisfiable - no configured endorser in required MSP", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1}

		_, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP", "Org2MSP"}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no configured endorser covers")
	})

	t.Run("unsatisfiable - no candidates at all", func(t *testing.T) {
		_, err := fsc.SelectEndorsersForMSPSets([]view.Identity{org1Endorser1}, mspOf, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no candidate MSP set")
	})

	t.Run("empty candidate set is rejected as malformed, not vacuously satisfied", func(t *testing.T) {
		selected, err := fsc.SelectEndorsersForMSPSets([]view.Identity{org1Endorser1}, mspOf, [][]string{{}})

		require.Error(t, err)
		assert.Nil(t, selected)
		assert.Contains(t, err.Error(), "requires no endorser")
	})

	t.Run("empty candidate set alongside a satisfiable one is still rejected", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1}

		selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP"}, {}})

		require.Error(t, err)
		assert.Nil(t, selected)
		assert.Contains(t, err.Error(), "requires no endorser")
	})

	t.Run("unsatisfiable - stale endorser check", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, staleEndorser}
		_, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP", "Org2MSP"}})
		require.Error(t, err)
	})

	t.Run("satisfiable - stale endorser skipped check", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org2Endorser1, staleEndorser}
		_, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP", "Org2MSP"}})
		require.NoError(t, err)
	})

	t.Run("mspOf error surfaces", func(t *testing.T) {
		badMspOf := func(id view.Identity) (string, error) {
			return "", errors.Errorf("boom")
		}

		_, err := fsc.SelectEndorsersForMSPSets([]view.Identity{org1Endorser1}, badMspOf, [][]string{{"Org1MSP"}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve MSP")
	})

	t.Run("same MSP required twice is filled with two distinct endorsers", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org1Endorser2}
		candidates := [][]string{{"Org1MSP", "Org1MSP"}}

		// Selection is randomized, so a single run proves little; a duplicate must never be
		// observed, however many times the selection is repeated.
		firstSlot := make(map[string]bool)
		secondSlot := make(map[string]bool)
		for range 200 {
			selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, candidates)
			require.NoError(t, err)
			require.Len(t, selected, 2)
			require.False(t, selected[0].Equal(selected[1]), "the same endorser [%s] filled both required Org1MSP slots", selected[0])
			firstSlot[selected[0].String()] = true
			secondSlot[selected[1].String()] = true
		}
		// Both endorsers must be reachable in both slots: no fixed-order bias.
		assert.Len(t, firstSlot, 2)
		assert.Len(t, secondSlot, 2)
	})

	t.Run("same MSP required twice with a single endorser is unsatisfiable", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1}

		_, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP", "Org1MSP"}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no configured endorser covers")
		// Error must name the short MSP and its required-vs-available counts.
		assert.Contains(t, err.Error(), "Org1MSP")
		assert.Contains(t, err.Error(), "requires 2")
		assert.Contains(t, err.Error(), "only 1 configured")
	})

	t.Run("candidate not coverable by distinct endorsers falls through to the next one", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org2Endorser1}
		candidates := [][]string{{"Org1MSP", "Org1MSP"}, {"Org2MSP"}}

		// Candidate order is randomized, so repeat to cover the case where the
		// non-coverable set is drawn first.
		for range 20 {
			selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, candidates)
			require.NoError(t, err)
			require.Len(t, selected, 1)
			assert.Equal(t, org2Endorser1.String(), selected[0].String())
		}
	})

	t.Run("duplicate configured entries count as a single endorser", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org1Endorser1}

		selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP"}})
		require.NoError(t, err)
		require.Len(t, selected, 1)
		assert.Equal(t, org1Endorser1.String(), selected[0].String())

		_, err = fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{{"Org1MSP", "Org1MSP"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no configured endorser covers")
	})

	t.Run("mixed candidate with a repeated and a single MSP", func(t *testing.T) {
		configured := []view.Identity{org1Endorser1, org1Endorser2, org2Endorser1}
		candidates := [][]string{{"Org1MSP", "Org1MSP", "Org2MSP"}}

		for range 20 {
			selected, err := fsc.SelectEndorsersForMSPSets(configured, mspOf, candidates)
			require.NoError(t, err)
			require.Len(t, selected, 3)
			assert.ElementsMatch(t, []string{"Org1MSP", "Org1MSP", "Org2MSP"}, mspIDsOf(t, mspOf, selected))
			assert.ElementsMatch(
				t,
				[]string{org1Endorser1.String(), org1Endorser2.String(), org2Endorser1.String()},
				[]string{selected[0].String(), selected[1].String(), selected[2].String()},
			)
		}
	})
}

func mspIDsOf(t *testing.T, mspOf func(view.Identity) (string, error), ids []view.Identity) []string {
	t.Helper()
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		mspID, err := mspOf(id)
		require.NoError(t, err)
		result = append(result, mspID)
	}

	return result
}
