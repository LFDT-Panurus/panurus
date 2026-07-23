/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/hyperledger/fabric-protos-go-apiv2/msp"
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

// candidateMSPSetsFromPolicies is a test-only composition of the two pure helpers that
// production code (SelectEndorsers) calls separately, kept here so the policy-lookup and
// candidate-set-derivation behaviors can still be exercised together against a
// NamespacePolicies fixture.
func candidateMSPSetsFromPolicies(policies *applicationpb.NamespacePolicies, namespace string) ([][]string, error) {
	nsPolicy, err := namespacePolicyFor(policies, namespace)
	if err != nil {
		return nil, err
	}

	return candidateMSPSetsFromNamespacePolicy(nsPolicy, namespace)
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

// ecdsaPublicKeyPEM generates a fresh ECDSA P256 key and returns its PEM/PKIX-encoded
// public key, in the same format expected by both applicationpb.ThresholdRule.PublicKey
// and a configured endorser's msp.SerializedIdentity.IdBytes.
func ecdsaPublicKeyPEM() ([]byte, error) {
	sk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(&sk.PublicKey)
	if err != nil {
		return nil, err
	}

	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func mustECDSAPublicKeyPEM(t require.TestingT) []byte {
	pemBytes, err := ecdsaPublicKeyPEM()
	require.NoError(t, err)

	return pemBytes
}

func mustEndorserIdentity(t require.TestingT, pubKeyPEM []byte) view.Identity {
	si := &msp.SerializedIdentity{Mspid: "Org1MSP", IdBytes: pubKeyPEM}
	raw, err := proto.Marshal(si)
	require.NoError(t, err)

	return view.Identity(raw)
}

func TestEndorserForThresholdRule(t *testing.T) {
	t.Run("matching key - selects the one configured endorser", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser := mustEndorserIdentity(t, pubPEM)
		other := mustEndorserIdentity(t, mustECDSAPublicKeyPEM(t))
		rule := &applicationpb.ThresholdRule{Scheme: "ECDSA", PublicKey: pubPEM}

		got, err := endorserForThresholdRule(rule, []view.Identity{other, endorser})

		require.NoError(t, err)
		assert.Equal(t, endorser, got)
	})

	t.Run("scheme match is case-insensitive", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser := mustEndorserIdentity(t, pubPEM)
		rule := &applicationpb.ThresholdRule{Scheme: "ecdsa", PublicKey: pubPEM}

		got, err := endorserForThresholdRule(rule, []view.Identity{endorser})

		require.NoError(t, err)
		assert.Equal(t, endorser, got)
	})

	t.Run("no configured endorser matches - error", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser := mustEndorserIdentity(t, mustECDSAPublicKeyPEM(t))
		rule := &applicationpb.ThresholdRule{Scheme: "ECDSA", PublicKey: pubPEM}

		_, err := endorserForThresholdRule(rule, []view.Identity{endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no configured endorser")
	})

	t.Run("multiple configured endorsers match - error", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser1 := mustEndorserIdentity(t, pubPEM)
		endorser2 := mustEndorserIdentity(t, pubPEM)
		rule := &applicationpb.ThresholdRule{Scheme: "ECDSA", PublicKey: pubPEM}

		_, err := endorserForThresholdRule(rule, []view.Identity{endorser1, endorser2})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected exactly one")
	})

	t.Run("unsupported scheme - error", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser := mustEndorserIdentity(t, pubPEM)
		rule := &applicationpb.ThresholdRule{Scheme: "BLS", PublicKey: pubPEM}

		_, err := endorserForThresholdRule(rule, []view.Identity{endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported scheme")
	})

	t.Run("malformed public key bytes - error", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser := mustEndorserIdentity(t, pubPEM)
		rule := &applicationpb.ThresholdRule{Scheme: "ECDSA", PublicKey: []byte("not-pem")}

		_, err := endorserForThresholdRule(rule, []view.Identity{endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse threshold-rule public key")
	})

	t.Run("non-ECDSA-shaped configured identities are skipped, not errored", func(t *testing.T) {
		pubPEM := mustECDSAPublicKeyPEM(t)
		endorser := mustEndorserIdentity(t, pubPEM)
		garbage := view.Identity([]byte("not a serialized identity"))
		rule := &applicationpb.ThresholdRule{Scheme: "ECDSA", PublicKey: pubPEM}

		got, err := endorserForThresholdRule(rule, []view.Identity{garbage, endorser})

		require.NoError(t, err)
		assert.Equal(t, endorser, got)
	})
}

const maxFuzzThresholdRulePublicKeyBytes = 64 << 10

// FuzzEndorserForThresholdRuleNoPanic hunts for malformed ThresholdRule public keys that
// panic endorserForThresholdRule instead of returning an error. rule.PublicKey is
// attacker-influenced: it arrives over the wire via the FabricX query service's
// GetNamespacePolicies RPC, so a malicious or buggy committer response must not crash the
// endorser-selection path.
func FuzzEndorserForThresholdRuleNoPanic(f *testing.F) {
	validPEM := mustECDSAPublicKeyPEM(f)
	configured := []view.Identity{mustEndorserIdentity(f, validPEM)}

	f.Add(validPEM)
	f.Add([]byte{})
	f.Add([]byte("not pem"))
	f.Add([]byte("-----BEGIN PUBLIC KEY-----\nnot base64\n-----END PUBLIC KEY-----"))
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a real cert")}))

	f.Fuzz(func(t *testing.T, publicKey []byte) {
		if len(publicKey) > maxFuzzThresholdRulePublicKeyBytes {
			t.Skip()
		}
		rule := &applicationpb.ThresholdRule{Scheme: "ECDSA", PublicKey: publicKey}
		require.NotPanics(t, func() {
			_, _ = endorserForThresholdRule(rule, configured)
		})
	})
}
