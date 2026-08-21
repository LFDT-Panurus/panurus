/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

func sampleRequest() *EndorseRequest {
	return &EndorseRequest{
		TokenRequest: []byte("marshalled-token-request"),
		TMSID:        token2.TMSID{Network: "evm", Namespace: "token"},
		Anchor:       "anchor-abc",
		Metadata:     map[string][]byte{"k": []byte("v")},
	}
}

func TestEndorseRequestRoundTrips(t *testing.T) {
	raw, err := json.Marshal(sampleRequest())
	require.NoError(t, err)

	var got EndorseRequest
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, *sampleRequest(), got)
}

// TestEndorseRequestCarriesNoDigest is the wire-format guard for the no-blind-sign rule (design
// §4.5): the request must not carry a precomputed digest field an endorser could be tricked into
// signing without recomputing it. This fails loudly if someone adds one.
func TestEndorseRequestCarriesNoDigest(t *testing.T) {
	raw, err := json.Marshal(sampleRequest())
	require.NoError(t, err)

	lower := strings.ToLower(string(raw))
	assert.NotContains(t, lower, "digest")
	assert.NotContains(t, lower, "eip712")
}

func TestEndorseRequestValidate(t *testing.T) {
	require.NoError(t, sampleRequest().Validate())

	bad := map[string]func(*EndorseRequest){
		"empty token request": func(r *EndorseRequest) { r.TokenRequest = nil },
		"empty anchor":        func(r *EndorseRequest) { r.Anchor = "" },
		"no network":          func(r *EndorseRequest) { r.TMSID.Network = "" },
		"no namespace":        func(r *EndorseRequest) { r.TMSID.Namespace = "" },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			r := sampleRequest()
			mutate(r)
			require.Error(t, r.Validate())
		})
	}
}

func TestEndorseResponseError(t *testing.T) {
	t.Run("success has no error", func(t *testing.T) {
		require.NoError(t, (&EndorseResponse{Delta: &statedelta.StateDelta{}, Signature: []byte{0x01}}).Error())
	})
	t.Run("failure surfaces the reason", func(t *testing.T) {
		err := (&EndorseResponse{Err: "unauthorized"}).Error()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
	})
	t.Run("empty response is an error", func(t *testing.T) {
		require.Error(t, (&EndorseResponse{}).Error())
	})
	t.Run("a signature without a delta is an error", func(t *testing.T) {
		// There would be nothing for the initiator to verify the signature against, and nothing to
		// encode into the transaction.
		require.Error(t, (&EndorseResponse{Signature: []byte{0x01}}).Error())
	})
}

// TestEndorseResponseRoundTripsTheDelta is the wire-format guard for the other half of the flow: the
// initiator builds no delta of its own, so a delta that does not survive the session unchanged means
// no quorum can ever assemble.
func TestEndorseResponseRoundTripsTheDelta(t *testing.T) {
	want := &EndorseResponse{
		Delta: &statedelta.StateDelta{
			Anchor:              [32]byte{0xC1},
			SpentRefs:           [][32]byte{{0x01}, {0x02}},
			Outputs:             []statedelta.OutputToken{{TokenID: [32]byte{0x03}, SNMarker: [32]byte{0x04}, TokenData: []byte("tok")}},
			MetadataKeys:        [][32]byte{{0x05}},
			MetadataVals:        [][]byte{[]byte("meta")},
			TokenRequestHash:    [32]byte{0x06},
			PublicParamsHash:    [32]byte{0x07},
			PublicParamsVersion: 9,
		},
		Signature:       []byte{0xAA, 0xBB},
		EndorserAddress: "0x0000000000000000000000000000000000000001",
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	var got EndorseResponse
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, *want, got)
}
