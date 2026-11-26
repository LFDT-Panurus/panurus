/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"testing"

	viewsdk "github.com/hyperledger-labs/fabric-smart-client/platform/view/sdk/dig"
	web "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/web/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"
)

type verifyGate struct{}

func (verifyGate) NotReady() (string, bool) { return "tms not loaded", true }

// TestReadinessGateEntry_ReachesFSCGroup checks (#2644) that Panurus can
// provide a viewsdk.ReadinessGateEntry and that a consumer of the
// "fsc-readiness-gates" group, declared like FSC's SDK.Start input
// (platform/view/sdk/dig/sdk.go:279), collects it.
func TestReadinessGateEntry_ReachesFSCGroup(t *testing.T) {
	c := dig.New()
	require.NoError(t, c.Provide(func() viewsdk.ReadinessGateEntry {
		return viewsdk.ReadinessGateEntry{Gate: verifyGate{}}
	}))

	var got []web.ReadinessGate
	require.NoError(t, c.Invoke(func(in struct {
		dig.In
		ReadinessGates []web.ReadinessGate `group:"fsc-readiness-gates"`
	}) {
		got = in.ReadinessGates
	}))
	require.Len(t, got, 1)
	reason, notReady := got[0].NotReady()
	require.True(t, notReady)
	require.Equal(t, "tms not loaded", reason)
}
