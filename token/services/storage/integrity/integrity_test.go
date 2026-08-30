/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package integrity_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// storedTokenRequest builds the wire format the ttx and audit stores hold: a
// TokenRequestWithMetadata at the given version, anchored to the given anchor.
// It panics rather than taking a *testing.T so it can also build fuzz seeds.
func storedTokenRequest(version uint32, anchor string) []byte {
	raw, err := proto.Marshal(&request.TokenRequestWithMetadata{
		Version: version,
		Anchor:  anchor,
		Request: &request.TokenRequest{
			Version: uint32(driver.ProtocolV1),
			Actions: []*request.Action{{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_TRANSFER,
						Raw:  []byte("action"),
					},
				},
			}},
		},
	})
	if err != nil {
		panic(err)
	}

	return raw
}

// actionsTokenRequest builds the bare actions-and-signatures wire format the
// endorser store holds, with the given number of actions. It panics rather than
// taking a *testing.T so it can also build fuzz seeds.
func actionsTokenRequest(version uint32, actions int) []byte {
	tr := &request.TokenRequest{Version: version}
	for range actions {
		tr.Actions = append(tr.Actions, &request.Action{
			Action: &request.Action_TypedAction{
				TypedAction: &request.TypedAction{
					Type: request.ActionType_ACTION_TYPE_ISSUE,
					Raw:  []byte("action"),
				},
			},
		})
	}
	raw, err := proto.Marshal(tr)
	if err != nil {
		panic(err)
	}

	return raw
}

func TestCheckTokenRequestForStorage(t *testing.T) {
	tests := []struct {
		name     string
		txID     string
		raw      []byte
		ppHash   driver.PPHash
		expected error
	}{
		{
			name:   "valid",
			txID:   "tx1",
			raw:    []byte("token request"),
			ppHash: driver.PPHash("pp-hash"),
		},
		{
			name:     "empty tx id",
			txID:     "",
			raw:      []byte("token request"),
			ppHash:   driver.PPHash("pp-hash"),
			expected: integrity.ErrEmptyTxID,
		},
		{
			name:     "nil token request",
			txID:     "tx1",
			raw:      nil,
			ppHash:   driver.PPHash("pp-hash"),
			expected: integrity.ErrEmptyTokenRequest,
		},
		{
			name:     "empty token request",
			txID:     "tx1",
			raw:      []byte{},
			ppHash:   driver.PPHash("pp-hash"),
			expected: integrity.ErrEmptyTokenRequest,
		},
		{
			name:     "nil public params hash",
			txID:     "tx1",
			raw:      []byte("token request"),
			ppHash:   nil,
			expected: integrity.ErrEmptyPublicParamsHash,
		},
		{
			name:     "empty public params hash",
			txID:     "tx1",
			raw:      []byte("token request"),
			ppHash:   driver.PPHash{},
			expected: integrity.ErrEmptyPublicParamsHash,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := integrity.CheckTokenRequestForStorage(test.txID, test.raw, test.ppHash)
			if test.expected == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.expected), "expected [%v], got [%v]", test.expected, err)
		})
	}
}

func TestCheckStoredTokenRequest(t *testing.T) {
	valid := storedTokenRequest(uint32(driver.ProtocolV1), "tx1")

	tests := []struct {
		name     string
		txID     string
		raw      []byte
		expected error
	}{
		{
			name: "valid",
			txID: "tx1",
			raw:  valid,
		},
		{
			name:     "empty tx id",
			txID:     "",
			raw:      valid,
			expected: integrity.ErrEmptyTxID,
		},
		{
			name:     "empty token request",
			txID:     "tx1",
			raw:      nil,
			expected: integrity.ErrEmptyTokenRequest,
		},
		{
			name:     "not a token request",
			txID:     "tx1",
			raw:      []byte{0xff, 0xff, 0xff, 0xff},
			expected: integrity.ErrMalformedTokenRequest,
		},
		{
			name:     "unsupported version",
			txID:     "tx1",
			raw:      storedTokenRequest(uint32(driver.ProtocolV1)+1, "tx1"),
			expected: integrity.ErrUnsupportedTokenRequestVersion,
		},
		{
			// the request of another transaction, filed under tx1
			name:     "anchor mismatch",
			txID:     "tx1",
			raw:      storedTokenRequest(uint32(driver.ProtocolV1), "tx2"),
			expected: integrity.ErrAnchorMismatch,
		},
		{
			name:     "no anchor",
			txID:     "tx1",
			raw:      storedTokenRequest(uint32(driver.ProtocolV1), ""),
			expected: integrity.ErrAnchorMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := integrity.CheckStoredTokenRequest(test.txID, test.raw)
			if test.expected == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.expected), "expected [%v], got [%v]", test.expected, err)
		})
	}
}

func TestCheckTokenRequestActions(t *testing.T) {
	tests := []struct {
		name     string
		raw      []byte
		expected error
	}{
		{
			name: "valid",
			raw:  actionsTokenRequest(uint32(driver.ProtocolV1), 1),
		},
		{
			name: "valid with several actions",
			raw:  actionsTokenRequest(uint32(driver.ProtocolV1), 3),
		},
		{
			name:     "empty",
			raw:      nil,
			expected: integrity.ErrEmptyTokenRequest,
		},
		{
			name:     "not a token request",
			raw:      []byte{0xff, 0xff, 0xff, 0xff},
			expected: integrity.ErrMalformedTokenRequest,
		},
		{
			name:     "unsupported version",
			raw:      actionsTokenRequest(uint32(driver.ProtocolV1)+1, 1),
			expected: integrity.ErrUnsupportedTokenRequestVersion,
		},
		{
			name:     "no actions",
			raw:      actionsTokenRequest(uint32(driver.ProtocolV1), 0),
			expected: integrity.ErrNoActions,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := integrity.CheckTokenRequestActions(test.raw)
			if test.expected == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.expected), "expected [%v], got [%v]", test.expected, err)
		})
	}
}

// TestCheckStoredTokenRequest_RejectsActionsOnlyFormat pins the two wire formats
// apart: the endorser store's bare actions format has no anchor field, so it can
// never satisfy the anchor binding the ttx and audit stores require.
func TestCheckStoredTokenRequest_RejectsActionsOnlyFormat(t *testing.T) {
	raw := actionsTokenRequest(uint32(driver.ProtocolV1), 1)
	require.NoError(t, integrity.CheckTokenRequestActions(raw))

	err := integrity.CheckStoredTokenRequest("tx1", raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, integrity.ErrAnchorMismatch) || errors.Is(err, integrity.ErrMalformedTokenRequest) ||
		errors.Is(err, integrity.ErrUnsupportedTokenRequestVersion), "unexpected error [%v]", err)
}

func TestCheckEndorsementAck(t *testing.T) {
	tests := []struct {
		name     string
		endorser []byte
		sigma    []byte
		expected error
	}{
		{
			name:     "valid",
			endorser: []byte("endorser"),
			sigma:    []byte("signature"),
		},
		{
			name:     "nil endorser",
			endorser: nil,
			sigma:    []byte("signature"),
			expected: integrity.ErrEmptyEndorser,
		},
		{
			name:     "empty endorser",
			endorser: []byte{},
			sigma:    []byte("signature"),
			expected: integrity.ErrEmptyEndorser,
		},
		{
			name:     "nil signature",
			endorser: []byte("endorser"),
			sigma:    nil,
			expected: integrity.ErrEmptySignature,
		},
		{
			name:     "empty signature",
			endorser: []byte("endorser"),
			sigma:    []byte{},
			expected: integrity.ErrEmptySignature,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := integrity.CheckEndorsementAck(test.endorser, test.sigma)
			if test.expected == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.expected), "expected [%v], got [%v]", test.expected, err)
		})
	}
}

func TestCheckPublicParamsHash(t *testing.T) {
	raw := []byte("public parameters")
	hash := driver.PPHash(utils.Hashable(raw).Raw())
	other := []byte("other public parameters")

	tests := []struct {
		name     string
		hash     driver.PPHash
		raw      []byte
		expected error
	}{
		{
			name: "matching hash",
			hash: hash,
			raw:  raw,
		},
		{
			// the store reports an absent record as nil bytes and no error;
			// telling absent from corrupt is the caller's job
			name: "no stored parameters",
			hash: hash,
			raw:  nil,
		},
		{
			name:     "parameters of another setup",
			hash:     hash,
			raw:      other,
			expected: integrity.ErrPublicParamsHashMismatch,
		},
		{
			name:     "truncated parameters",
			hash:     hash,
			raw:      raw[:len(raw)-1],
			expected: integrity.ErrPublicParamsHashMismatch,
		},
		{
			name:     "hash is not a hash of anything",
			hash:     driver.PPHash("not-a-hash"),
			raw:      raw,
			expected: integrity.ErrPublicParamsHashMismatch,
		},
		{
			name:     "nil hash",
			hash:     nil,
			raw:      raw,
			expected: integrity.ErrEmptyPublicParamsHash,
		},
		{
			name:     "empty hash",
			hash:     driver.PPHash{},
			raw:      raw,
			expected: integrity.ErrEmptyPublicParamsHash,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := integrity.CheckPublicParamsHash(test.hash, test.raw)
			if test.expected == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.expected), "expected [%v], got [%v]", test.expected, err)
		})
	}
}

func TestCheckIdentity(t *testing.T) {
	require.NoError(t, integrity.CheckIdentity([]byte("alice")))
	assert.True(t, errors.Is(integrity.CheckIdentity(nil), integrity.ErrEmptyIdentity))
	assert.True(t, errors.Is(integrity.CheckIdentity([]byte{}), integrity.ErrEmptyIdentity))
}

func TestCheckIdentityMatch(t *testing.T) {
	tests := []struct {
		name      string
		requested []byte
		stored    []byte
		expected  error
	}{
		{
			name:      "match",
			requested: []byte("alice"),
			stored:    []byte("alice"),
		},
		{
			name:      "different identity of the same length",
			requested: []byte("alice"),
			stored:    []byte("bobby"),
			expected:  integrity.ErrIdentityMismatch,
		},
		{
			name:      "prefix is not a match",
			requested: []byte("alice"),
			stored:    []byte("alice-and-bob"),
			expected:  integrity.ErrIdentityMismatch,
		},
		{
			name:      "no stored identity",
			requested: []byte("alice"),
			stored:    nil,
			expected:  integrity.ErrIdentityMismatch,
		},
		{
			name:      "empty requested identity",
			requested: nil,
			stored:    []byte("alice"),
			expected:  integrity.ErrEmptyIdentity,
		},
		{
			// both empty must not be reported as a match: it is the shared
			// "<empty>" row key, not an identity
			name:      "both empty",
			requested: nil,
			stored:    nil,
			expected:  integrity.ErrEmptyIdentity,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := integrity.CheckIdentityMatch(test.requested, test.stored)
			if test.expected == nil {
				assert.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.expected), "expected [%v], got [%v]", test.expected, err)
		})
	}
}

// TestCheckIdentityMatch_DoesNotLeakIdentities checks that the mismatch error
// reports lengths only: the message ends up in logs, and the identities involved
// are the caller's and the store's, not the log reader's.
func TestCheckIdentityMatch_DoesNotLeakIdentities(t *testing.T) {
	err := integrity.CheckIdentityMatch([]byte("alice-secret"), []byte("bob-secret"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "alice-secret")
	assert.NotContains(t, err.Error(), "bob-secret")
}

func FuzzCheckStoredTokenRequest(f *testing.F) {
	f.Add("tx1", storedTokenRequest(uint32(driver.ProtocolV1), "tx1"))
	f.Add("tx1", storedTokenRequest(uint32(driver.ProtocolV1), "tx2"))
	f.Add("tx1", storedTokenRequest(uint32(driver.ProtocolV1)+1, "tx1"))
	f.Add("tx1", []byte(nil))
	f.Add("", []byte(nil))
	f.Add("tx1", []byte{0xff, 0xff, 0xff, 0xff})
	f.Add("tx1", []byte{0x08})
	f.Add("tx1", actionsTokenRequest(uint32(driver.ProtocolV1), 1))

	f.Fuzz(func(t *testing.T, txID string, raw []byte) {
		// the contract is that no input panics and that success implies the
		// payload is anchored to txID
		if err := integrity.CheckStoredTokenRequest(txID, raw); err != nil {
			return
		}
		requestWithMetadata := &request.TokenRequestWithMetadata{}
		require.NoError(t, proto.Unmarshal(raw, requestWithMetadata))
		require.Equal(t, txID, requestWithMetadata.Anchor)
		require.NotEmpty(t, txID)
	})
}

func FuzzCheckTokenRequestActions(f *testing.F) {
	f.Add(actionsTokenRequest(uint32(driver.ProtocolV1), 1))
	f.Add(actionsTokenRequest(uint32(driver.ProtocolV1), 0))
	f.Add(actionsTokenRequest(uint32(driver.ProtocolV1)+1, 1))
	f.Add([]byte(nil))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0x08})
	f.Add(storedTokenRequest(uint32(driver.ProtocolV1), "tx1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		// the contract is that no input panics and that success implies a
		// deserializable request with at least one action
		if err := integrity.CheckTokenRequestActions(raw); err != nil {
			return
		}
		tokenRequest := &driver.TokenRequest{}
		require.NoError(t, tokenRequest.FromBytes(raw))
		require.NotEmpty(t, tokenRequest.Actions)
	})
}
