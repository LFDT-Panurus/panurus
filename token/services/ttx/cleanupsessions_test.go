/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This white-box (package ttx) file covers cleanupSessions, which needs access to
// the unexported sessions field. It is kept separate from the black-box
// collectendorsements_test.go so the latter can keep importing dep/mock, which
// cannot be imported from package ttx without creating an import cycle.
package ttx

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
)

// countingSession is a minimal view.Session fake that records how many times
// Close was called.
type countingSession struct {
	closes int
}

func (s *countingSession) Info() view.SessionInfo                             { return view.SessionInfo{} }
func (s *countingSession) Send([]byte) error                                  { return nil }
func (s *countingSession) SendWithContext(context.Context, []byte) error      { return nil }
func (s *countingSession) SendError([]byte) error                             { return nil }
func (s *countingSession) SendErrorWithContext(context.Context, []byte) error { return nil }
func (s *countingSession) Receive() <-chan *view.Message                      { return nil }
func (s *countingSession) Close()                                             { s.closes++ }

// TestCleanupSessions_ClosesAllAndEmptiesMap verifies that cleanupSessions closes
// every tracked session and clears the session map, so no session is leaked on any
// return path of Call.
func TestCleanupSessions_ClosesAllAndEmptiesMap(t *testing.T) {
	s1, s2 := &countingSession{}, &countingSession{}
	c := &CollectEndorsementsView{
		sessions: map[string]view.Session{"auditor": s1, "party": s2},
	}

	c.cleanupSessions(t.Context())

	assert.Equal(t, 1, s1.closes, "auditor session should be closed exactly once")
	assert.Equal(t, 1, s2.closes, "party session should be closed exactly once")
	assert.Empty(t, c.sessions, "session map should be emptied after cleanup")
}

// TestCleanupSessions_Idempotent verifies that a second cleanupSessions call is a
// no-op and does not close any session twice. This matters because the deferred
// cleanup may run after sessions were already released earlier in the flow.
func TestCleanupSessions_Idempotent(t *testing.T) {
	s := &countingSession{}
	c := &CollectEndorsementsView{
		sessions: map[string]view.Session{"auditor": s},
	}

	c.cleanupSessions(t.Context())
	c.cleanupSessions(t.Context())

	assert.Equal(t, 1, s.closes, "session must not be closed more than once across repeated cleanup calls")
}

// TestCleanupSessions_NilAndEmptySafe verifies cleanupSessions tolerates an empty
// map and nil session entries without panicking.
func TestCleanupSessions_NilAndEmptySafe(t *testing.T) {
	// Empty map.
	empty := &CollectEndorsementsView{sessions: map[string]view.Session{}}
	assert.NotPanics(t, func() { empty.cleanupSessions(t.Context()) })

	// Nil session entry.
	withNil := &CollectEndorsementsView{sessions: map[string]view.Session{"nil": nil}}
	assert.NotPanics(t, func() { withNil.cleanupSessions(t.Context()) })
	assert.Empty(t, withNil.sessions, "nil entries should also be removed")
}
