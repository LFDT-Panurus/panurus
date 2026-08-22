/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
)

// stubNode records whether the context was still live at the moment Stop ran. It has to be sampled
// there rather than afterwards: removeContainer cancels its context on the way out, so a context
// captured for later inspection always reads as cancelled and would pass whatever the fix did.
type stubNode struct {
	stopped        int
	liveWhenCalled bool
	err            error
}

func (s *stubNode) Endpoint() string { return "" }
func (s *stubNode) ChainID() int64   { return 0 }
func (s *stubNode) Stop(ctx context.Context) error {
	s.stopped++
	s.liveWhenCalled = ctx.Err() == nil

	return s.err
}

// TestRemoveContainerUsesAFreshContext pins the reason removeContainer does not take the caller's
// context. A node is removed here precisely because waiting for it ran out of time, so passing the
// expired context on would refuse the removal at the one moment it matters and leave the container
// running with nothing left holding a reference to it.
func TestRemoveContainerUsesAFreshContext(t *testing.T) {
	node := &stubNode{}
	removeContainer(node)

	assert.Equal(t, 1, node.stopped, "the container must be removed")
	assert.True(t, node.liveWhenCalled, "the teardown context must still be live when Stop runs")
}

// TestRemoveContainerToleratesAFailedStop checks a removal that fails is reported rather than
// propagated: the caller is already returning why the node is unusable, which is the more useful of
// the two errors.
func TestRemoveContainerToleratesAFailedStop(t *testing.T) {
	node := &stubNode{err: errors.New("docker is gone")}

	assert.NotPanics(t, func() { removeContainer(node) })
	assert.Equal(t, 1, node.stopped)
}
