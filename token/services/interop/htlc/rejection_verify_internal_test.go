/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"testing"

	fscsession "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/comm/session"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

type baseContext = view.Context

// sessionOnlyContext is a view.Context whose only working method is Session.
type sessionOnlyContext struct {
	baseContext
	s view.Session
}

func (c *sessionOnlyContext) Session() view.Session { return c.s }

// chanSession is a view.Session whose only working method is Receive.
type chanSession struct {
	view.Session
	ch chan *view.Message
}

func (s *chanSession) Receive() <-chan *view.Message { return s.ch }

// TestVerify_ReceiveTransactionRejectionIsTyped asserts the desired behaviour: a remote
// rejection received by receiveTransactionView (endorsement.go:43-44) is matchable with
// errors.Is. It fails while the view builds an untyped error.
func TestVerify_ReceiveTransactionRejectionIsTyped(t *testing.T) {
	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Status: view.ERROR, Payload: []byte(fscsession.ReasonBusy + ": too many")}
	ctx := &sessionOnlyContext{s: &chanSession{ch: ch}}

	_, err := NewReceiveTransactionView("net").Call(ctx)
	require.Error(t, err)
	t.Logf("htlc error: %v", err)
	require.ErrorIs(t, err, fscsession.ErrRejected)
	require.ErrorIs(t, err, fscsession.ErrBusy)
}
