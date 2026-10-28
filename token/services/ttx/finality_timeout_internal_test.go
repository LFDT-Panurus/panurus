/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"

	"github.com/LFDT-Panurus/panurus/token"
)

// TestCoverNetworkFinality checks a default finality wait outlasts the network's own finality
// timeout. The EVM driver's minimum with the finalized block tag is 13 minutes, longer than either
// fixed default, so waiting only the default gave up before the network decided.
func TestCoverNetworkFinality(t *testing.T) {
	cases := []struct {
		name              string
		fallback, network time.Duration
		want              time.Duration
	}{
		{"network does not say", 5 * time.Minute, 0, 5 * time.Minute},
		{"network decides well within the default", 5 * time.Minute, time.Minute, 5 * time.Minute},
		{"network takes longer than the default", 5 * time.Minute, 13 * time.Minute, 14 * time.Minute},
		{"network ends just before the default", 10 * time.Minute, 9*time.Minute + 30*time.Second, 10*time.Minute + 30*time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, coverNetworkFinality(c.fallback, c.network))
		})
	}
}

type noServices struct{}

func (noServices) GetService(any) (any, error) { return nil, errors.New("no such service") }

// TestDefaultFinalityTimeoutWithoutANetwork checks the fixed default is kept when no network can be
// resolved, rather than failing the wait.
func TestDefaultFinalityTimeoutWithoutANetwork(t *testing.T) {
	got := defaultFinalityTimeout(noServices{}, token.TMSID{Network: "n", Namespace: "ns"}, 5*time.Minute)
	assert.Equal(t, 5*time.Minute, got)
}
