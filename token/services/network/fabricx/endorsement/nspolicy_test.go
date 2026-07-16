/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"testing"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/common/policydsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func mustMspRulePolicyItem(t *testing.T, namespace, policy string) *applicationpb.PolicyItem {
	t.Helper()

	env, err := policydsl.FromString(policy)
	require.NoError(t, err)
	mspRule, err := proto.Marshal(env)
	require.NoError(t, err)

	nsPolicy := &applicationpb.NamespacePolicy{Rule: &applicationpb.NamespacePolicy_MspRule{MspRule: mspRule}}
	raw, err := proto.Marshal(nsPolicy)
	require.NoError(t, err)

	return &applicationpb.PolicyItem{Namespace: namespace, Policy: raw}
}

func mustThresholdRulePolicyItem(t *testing.T, namespace string) *applicationpb.PolicyItem {
	t.Helper()

	nsPolicy := &applicationpb.NamespacePolicy{Rule: &applicationpb.NamespacePolicy_ThresholdRule{ThresholdRule: &applicationpb.ThresholdRule{}}}
	raw, err := proto.Marshal(nsPolicy)
	require.NoError(t, err)

	return &applicationpb.PolicyItem{Namespace: namespace, Policy: raw}
}

func TestCandidateMSPSetsFromPolicies(t *testing.T) {
	t.Run("OR policy - two single-MSP candidates", func(t *testing.T) {
		policies := &applicationpb.NamespacePolicies{
			Policies: []*applicationpb.PolicyItem{
				mustMspRulePolicyItem(t, "ns1", "OR('Org1MSP.member', 'Org2MSP.member')"),
			},
		}

		candidates, err := candidateMSPSetsFromPolicies(policies, "ns1")

		require.NoError(t, err)
		require.Len(t, candidates, 2)
		assert.ElementsMatch(t, []string{"Org1MSP"}, candidates[0])
		assert.ElementsMatch(t, []string{"Org2MSP"}, candidates[1])
	})

	t.Run("AND policy - single candidate requiring both MSPs", func(t *testing.T) {
		policies := &applicationpb.NamespacePolicies{
			Policies: []*applicationpb.PolicyItem{
				mustMspRulePolicyItem(t, "ns1", "AND('Org1MSP.member', 'Org2MSP.member')"),
			},
		}

		candidates, err := candidateMSPSetsFromPolicies(policies, "ns1")

		require.NoError(t, err)
		require.Len(t, candidates, 1)
		assert.ElementsMatch(t, []string{"Org1MSP", "Org2MSP"}, candidates[0])
	})

	t.Run("OutOf(2,3) policy - three 2-of-3 candidates", func(t *testing.T) {
		policies := &applicationpb.NamespacePolicies{
			Policies: []*applicationpb.PolicyItem{
				mustMspRulePolicyItem(t, "ns1", "OutOf(2, 'Org1MSP.member', 'Org2MSP.member', 'Org3MSP.member')"),
			},
		}

		candidates, err := candidateMSPSetsFromPolicies(policies, "ns1")

		require.NoError(t, err)
		require.Len(t, candidates, 3)
		for _, c := range candidates {
			assert.Len(t, c, 2)
		}
	})

	t.Run("threshold-rule namespace - hard error", func(t *testing.T) {
		policies := &applicationpb.NamespacePolicies{
			Policies: []*applicationpb.PolicyItem{
				mustThresholdRulePolicyItem(t, "ns1"),
			},
		}

		_, err := candidateMSPSetsFromPolicies(policies, "ns1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "threshold-rule")
	})

	t.Run("namespace not found - error", func(t *testing.T) {
		policies := &applicationpb.NamespacePolicies{
			Policies: []*applicationpb.PolicyItem{
				mustMspRulePolicyItem(t, "ns1", "OR('Org1MSP.member', 'Org2MSP.member')"),
			},
		}

		_, err := candidateMSPSetsFromPolicies(policies, "unknown-ns")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no endorsement policy found")
	})
}
