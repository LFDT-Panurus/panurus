/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

const (
	testVerifier   = "0x1111111111111111111111111111111111111111"
	testEndorser   = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
	otherEndorser  = "0x2222222222222222222222222222222222222222"
	unknownAddress = "0x3333333333333333333333333333333333333333"
)

// abiWord encodes a single static 32-byte return value: an address, a uint256 or a bool, all of which
// the verifier's reads return in one word.
func abiWord(b []byte) []byte {
	out := make([]byte, 32)
	copy(out[32-len(b):], b)

	return out
}

func abiUint(v uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], v)

	return out
}

func addressWord(t *testing.T, hexAddr string) []byte {
	t.Helper()
	a, err := client.HexToAddress(hexAddr)
	require.NoError(t, err)

	return abiWord(a[:])
}

// policyChain answers the three reads the policy check makes, so a test states the chain's side of
// the policy declaratively instead of matching selectors by hand.
type policyChain struct {
	verifier   string
	threshold  uint64
	registered map[string]bool
	// failOn, when set, makes the named read fail, for the degraded paths.
	failOn string
}

func (p policyChain) install(t *testing.T, evm *mock.EVMClient) {
	t.Helper()
	verifierSel := abi.MethodID(endorsementVerifierMethod)
	thresholdSel := abi.MethodID(getThresholdMethod)
	isEndorserSel := abi.MethodID(isEndorserMethod)

	evm.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		switch {
		case bytes.HasPrefix(data, verifierSel):
			if p.failOn == endorsementVerifierMethod {
				return nil, errors.New("node is unhappy")
			}

			return addressWord(t, p.verifier), nil
		case bytes.HasPrefix(data, thresholdSel):
			if p.failOn == getThresholdMethod {
				return nil, errors.New("node is unhappy")
			}

			return abiUint(p.threshold), nil
		case bytes.HasPrefix(data, isEndorserSel):
			if p.failOn == isEndorserMethod {
				return nil, errors.New("node is unhappy")
			}
			queried := client.BytesToAddress(data[len(isEndorserSel)+12:])
			if p.registered[queried.Hex()] {
				return abiUint(1), nil
			}

			return abiUint(0), nil
		}

		return nil, errors.Errorf("unexpected call %x", data)
	}
}

// policyNetwork builds a Network whose configuration is mutated by tweak before it is validated.
func policyNetwork(t *testing.T, evm *mock.EVMClient, tweak func(*Config)) *Network {
	t.Helper()
	evm.ChainIDReturns(big.NewInt(testChainID), nil)
	deployedTokenState(evm)

	c := validConfig()
	if tweak != nil {
		tweak(c)
	}
	c.applyDefaults()
	require.NoError(t, c.Validate())

	n, err := NewNetwork("evm-net", c, evm, nil, nil, nil)
	require.NoError(t, err)

	return n
}

func registered(addrs ...string) map[string]bool {
	out := map[string]bool{}
	for _, a := range addrs {
		out[a] = true
	}

	return out
}

// TestConnectAcceptsAMatchingPolicy is the control: a configuration that agrees with the chain
// connects, so the checks below are failing on the disagreement and not on the check itself.
func TestConnectAcceptsAMatchingPolicy(t *testing.T) {
	evm := &mock.EVMClient{}
	policyChain{
		verifier:   testVerifier,
		threshold:  1,
		registered: registered(mustAddr(t, testEndorser)),
	}.install(t, evm)

	n := policyNetwork(t, evm, nil)

	_, err := n.Connect("token")
	assert.NoError(t, err)
}

// TestConnectRejectsAThresholdTheChainDoesNotEnforce covers the drift that is hardest to read from
// the outside: the node collects what it believes is a quorum, and the contract rejects the bundle
// for being one signature short, which surfaces as a revert rather than as a misconfiguration.
func TestConnectRejectsAThresholdTheChainDoesNotEnforce(t *testing.T) {
	evm := &mock.EVMClient{}
	policyChain{
		verifier:   testVerifier,
		threshold:  2,
		registered: registered(mustAddr(t, testEndorser)),
	}.install(t, evm)

	n := policyNetwork(t, evm, nil)

	_, err := n.Connect("token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires 2 endorsements, configuration says 1")
}

// TestConnectRejectsAnEndorserTheVerifierDoesNotKnow covers a stale entry in the endorser set. The
// initiator routes to it and counts its reply, and the contract then rejects the whole bundle as an
// unauthorized signer, so one wrong address can fail every transaction even with enough real signers.
func TestConnectRejectsAnEndorserTheVerifierDoesNotKnow(t *testing.T) {
	evm := &mock.EVMClient{}
	policyChain{
		verifier:   testVerifier,
		threshold:  2,
		registered: registered(mustAddr(t, testEndorser)),
	}.install(t, evm)

	n := policyNetwork(t, evm, func(c *Config) {
		c.Endorsement.Threshold = 2
		c.Endorsement.Endorsers = append(c.Endorsement.Endorsers,
			EndorserBinding{Address: unknownAddress, FSCIdentity: "endorser-2"})
	})

	_, err := n.Connect("token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered in verifier")
	assert.Contains(t, err.Error(), "endorser-2")
}

// TestConnectRejectsAVerifierTheConfigurationDisagreesWith checks the optional configured verifier is
// held to the TokenState's own answer, which is the authoritative one.
func TestConnectRejectsAVerifierTheConfigurationDisagreesWith(t *testing.T) {
	evm := &mock.EVMClient{}
	policyChain{
		verifier:   testVerifier,
		threshold:  1,
		registered: registered(mustAddr(t, testEndorser)),
	}.install(t, evm)

	n := policyNetwork(t, evm, func(c *Config) {
		c.Contracts.EndorsementVerifier = otherEndorser
	})

	_, err := n.Connect("token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "calls verifier")
}

// TestConnectProceedsWhenThePolicyCannotBeRead pins the deliberate asymmetry: only a positively
// observed contradiction is fatal. A read that fails says nothing about the policy -- the contract may
// not be deployed yet, or the node may be briefly unhappy -- and must not take a node down, since no
// check at all is what the driver did before this existed.
//
// Each case makes exactly one read unreadable and puts the contradiction behind that same read, with
// everything else agreeing. That way a passing case is evidence the unreadable check was skipped
// rather than evidence there was nothing to catch.
func TestConnectProceedsWhenThePolicyCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		failOn string
		chain  policyChain
	}{
		{
			// The verifier address is unreachable, so nothing downstream of it can be checked at all.
			failOn: endorsementVerifierMethod,
			chain:  policyChain{threshold: 99, registered: map[string]bool{}},
		},
		{
			// A threshold that would be refused, behind a read that fails.
			failOn: getThresholdMethod,
			chain:  policyChain{verifier: testVerifier, threshold: 99, registered: registered(mustAddr(t, testEndorser))},
		},
		{
			// An endorser set that would be refused, behind a read that fails.
			failOn: isEndorserMethod,
			chain:  policyChain{verifier: testVerifier, threshold: 1, registered: map[string]bool{}},
		},
	} {
		t.Run(tc.failOn, func(t *testing.T) {
			evm := &mock.EVMClient{}
			chain := tc.chain
			chain.failOn = tc.failOn
			chain.install(t, evm)

			n := policyNetwork(t, evm, nil)

			_, err := n.Connect("token")
			assert.NoError(t, err, "an unreadable policy must not refuse the connection")
		})
	}
}

// TestConnectRefusesTheSameContradictionsWhenTheyAreReadable is the counterpart to the case above: the
// exact chain states it tolerates while unreadable are refused once they can actually be read, so the
// tolerance is about the failed read and not about the check being toothless.
func TestConnectRefusesTheSameContradictionsWhenTheyAreReadable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chain policyChain
	}{
		{"threshold", policyChain{verifier: testVerifier, threshold: 99, registered: registered(mustAddr(t, testEndorser))}},
		{"endorser set", policyChain{verifier: testVerifier, threshold: 1, registered: map[string]bool{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evm := &mock.EVMClient{}
			tc.chain.install(t, evm)

			n := policyNetwork(t, evm, nil)

			_, err := n.Connect("token")
			assert.Error(t, err)
		})
	}
}

// TestConnectSkipsAnUninitializedTokenState covers a TokenState that names no verifier yet, which is
// the shape a clone has before initialize runs rather than a policy disagreement.
func TestConnectSkipsAnUninitializedTokenState(t *testing.T) {
	evm := &mock.EVMClient{}
	evm.ChainIDReturns(big.NewInt(testChainID), nil)
	deployedTokenState(evm)                // the clone is deployed; it just has not been initialized
	evm.CallReturns(make([]byte, 32), nil) // the zero address

	c := validConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())
	n, err := NewNetwork("evm-net", c, evm, nil, nil, nil)
	require.NoError(t, err)

	_, err = n.Connect("token")
	assert.NoError(t, err)
}

func mustAddr(t *testing.T, hexAddr string) string {
	t.Helper()
	a, err := client.HexToAddress(hexAddr)
	require.NoError(t, err)

	return a.Hex()
}
