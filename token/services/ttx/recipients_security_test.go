/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validMultisigRecipientData(t *testing.T) (*MultisigRecipientData, token.Identity) {
	t.Helper()
	identities := []token.Identity{[]byte("alice"), []byte("bob")}
	identity, err := multisig.WrapIdentities(identities...)
	require.NoError(t, err)
	auditInfo, err := multisig.WrapAuditInfo([][]byte{[]byte("alice-audit"), []byte("bob-audit")})
	require.NoError(t, err)

	return &MultisigRecipientData{
		RecipientData: &RecipientData{Identity: identity, AuditInfo: auditInfo},
		Nodes:         []view.Identity{view.Identity("alice-node"), view.Identity("bob-node")},
		Recipients:    identities,
	}, identities[1]
}

func validPolicyRecipientData(t *testing.T) (*PolicyRecipientData, token.Identity) {
	t.Helper()
	const policy = "$0 AND $1"
	identities := []token.Identity{[]byte("alice"), []byte("bob")}
	identity, err := boolpolicy.WrapPolicyIdentity(policy, identities...)
	require.NoError(t, err)
	auditInfo, err := boolpolicy.WrapAuditInfo([][]byte{[]byte("alice-audit"), []byte("bob-audit")})
	require.NoError(t, err)

	return &PolicyRecipientData{
		RecipientData: &RecipientData{Identity: identity, AuditInfo: auditInfo},
		Nodes:         []view.Identity{view.Identity("alice-node"), view.Identity("bob-node")},
		Recipients:    identities,
		Policy:        policy,
	}, identities[1]
}

func TestValidateMultisigRecipientDataRejectsAdversarialPayloads(t *testing.T) {
	valid, local := validMultisigRecipientData(t)
	_, _, err := validateMultisigRecipientData(valid, local)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*MultisigRecipientData) token.Identity
		match  string
	}{
		{
			name: "nil recipient data",
			mutate: func(data *MultisigRecipientData) token.Identity {
				data.RecipientData = nil

				return local
			},
			match: "recipient data is nil",
		},
		{
			name: "short recipients slice",
			mutate: func(data *MultisigRecipientData) token.Identity {
				data.Recipients = data.Recipients[:1]

				return local
			},
			match: "count mismatch",
		},
		{
			name: "recipient does not match composite component",
			mutate: func(data *MultisigRecipientData) token.Identity {
				data.Recipients[0] = []byte("mallory")

				return local
			},
			match: "component/recipient mismatch",
		},
		{
			name: "responder omitted from composite",
			mutate: func(data *MultisigRecipientData) token.Identity {
				return []byte("mallory")
			},
			match: "responder identity is not a component",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, _ := validMultisigRecipientData(t)
			localIdentity := test.mutate(data)
			_, _, err := validateMultisigRecipientData(data, localIdentity)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.match)
		})
	}
}

func TestValidatePolicyRecipientDataRejectsAdversarialPayloads(t *testing.T) {
	valid, local := validPolicyRecipientData(t)
	_, _, err := validatePolicyRecipientData(valid, valid.Policy, local)
	require.NoError(t, err)

	t.Run("outer policy differs from request", func(t *testing.T) {
		data, local := validPolicyRecipientData(t)
		data.Policy = "$0 OR $1"
		_, _, err := validatePolicyRecipientData(data, "$0 AND $1", local)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "policy mismatch")
	})

	t.Run("embedded policy differs from request", func(t *testing.T) {
		data, local := validPolicyRecipientData(t)
		identity, err := boolpolicy.WrapPolicyIdentity("$0 OR $1", data.Recipients...)
		require.NoError(t, err)
		data.RecipientData.Identity = identity
		_, _, err = validatePolicyRecipientData(data, data.Policy, local)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "embedded policy mismatch")
	})

	t.Run("parallel slices differ", func(t *testing.T) {
		data, local := validPolicyRecipientData(t)
		data.Nodes = data.Nodes[:1]
		_, _, err := validatePolicyRecipientData(data, data.Policy, local)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count mismatch")
	})

	t.Run("responder omitted", func(t *testing.T) {
		data, _ := validPolicyRecipientData(t)
		_, _, err := validatePolicyRecipientData(data, data.Policy, []byte("mallory"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "responder identity is not a component")
	})
}

func FuzzCompositeRecipientValidationNeverPanics(f *testing.F) {
	identities := []token.Identity{[]byte("alice"), []byte("bob")}
	multiIdentity, err := multisig.WrapIdentities(identities...)
	if err != nil {
		f.Fatalf("failed preparing multisig fuzz seed: %v", err)
	}
	multiAuditInfo, err := multisig.WrapAuditInfo([][]byte{[]byte("alice-audit"), []byte("bob-audit")})
	if err != nil {
		f.Fatalf("failed preparing multisig audit-info seed: %v", err)
	}
	f.Add([]byte(multiIdentity), multiAuditInfo, []byte(identities[1]), uint8(2), uint8(2), "$0 AND $1")
	f.Add([]byte("not-an-identity"), []byte("not-audit-info"), []byte("local"), uint8(1), uint8(0), "")

	f.Fuzz(func(t *testing.T, identityRaw, auditRaw, localRaw []byte, nodeCount, recipientCount uint8, expectedPolicy string) {
		nodes := make([]view.Identity, int(nodeCount%8))
		for i := range nodes {
			nodes[i] = view.Identity{byte(i)}
		}
		recipients := make([]token.Identity, int(recipientCount%8))
		for i := range recipients {
			recipients[i] = token.Identity{byte(i)}
		}
		rd := &RecipientData{Identity: identityRaw, AuditInfo: auditRaw}
		_, _, _ = validateMultisigRecipientData(&MultisigRecipientData{
			RecipientData: rd,
			Nodes:         nodes,
			Recipients:    recipients,
		}, localRaw)
		_, _, _ = validatePolicyRecipientData(&PolicyRecipientData{
			RecipientData: rd,
			Nodes:         nodes,
			Recipients:    recipients,
			Policy:        expectedPolicy,
		}, expectedPolicy, localRaw)
	})
}
