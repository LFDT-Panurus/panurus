/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package session_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscsession "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/comm/session"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

// rejectionMsg is a view.ERROR message with an FSC rejection reason prefix.
func rejectionMsg() *view.Message {
	return &view.Message{Status: view.ERROR, Payload: []byte(fscsession.ReasonUnknownView + ": no responder")}
}

// TestVerify_RejectionErrorIsTyped is the control: FSC's RejectionError is typed, and
// FSC errors.Wrapf keeps it matchable.
func TestVerify_RejectionErrorIsTyped(t *testing.T) {
	err := fscsession.RejectionError(rejectionMsg())
	require.ErrorIs(t, err, fscsession.ErrRejected)
	require.ErrorIs(t, err, fscsession.ErrUnknownView)
	require.ErrorIs(t, errors.Wrapf(err, "got failure from [%s]", "party"), fscsession.ErrRejected)
}

// TestVerify_ReceiveRawRejectionIsTyped asserts the desired behaviour: a remote rejection
// received through the Panurus session wrapper (session.go:90-93) is matchable with
// errors.Is. It fails while the wrapper builds an untyped error.
func TestVerify_ReceiveRawRejectionIsTyped(t *testing.T) {
	ch := make(chan *view.Message, 1)
	ch <- rejectionMsg()
	s := &mock.Session{}
	s.ReceiveReturns(ch)

	_, err := newSession(t, s, &mock.Marshaller{}).ReceiveRaw()
	require.Error(t, err)
	t.Logf("wrapper error: %v", err)
	require.ErrorIs(t, err, fscsession.ErrRejected)
	require.ErrorIs(t, err, fscsession.ErrUnknownView)
}
