/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"encoding/json"
	"strings"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, (&EndorseResponse{Signature: []byte{0x01}}).Error())
	})
	t.Run("failure surfaces the reason", func(t *testing.T) {
		err := (&EndorseResponse{Err: "unauthorized"}).Error()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
	})
	t.Run("empty response is an error", func(t *testing.T) {
		require.Error(t, (&EndorseResponse{}).Error())
	})
}
