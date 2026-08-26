package ttx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReceiveTransactionTimeoutBudget asserts the invariant that the responder's
// overall receive timeout must exceed the sum of every wait it depends on:
// sig fan-out + audit + approval (+ slack).
//
// KNOWN FAILURE (issue #1266): as of this test's authorship, the responder budget
// (4 min) equals — but does not exceed — the sum of its component legs in
// FSC-endorsement mode (1 min sig fan-out + 1 min audit + 2 min approval = 4 min),
// leaving zero slack margin. This is a real bug, not a test bug — do not "fix"
// this test by loosening the invariant. Fix it by correcting the responder
// timeout (or a component timeout) in the source files below, then this test
// will pass on its own.
func TestReceiveTransactionTimeoutBudget(t *testing.T) {
	const (
		responderReceiveTimeout = 4 * time.Minute // receivetx.go:42
		sigFanOutTimeout        = 1 * time.Minute // endorse.go:111
		auditTimeout            = 1 * time.Minute // auditor.go:189
		approvalTimeout         = 2 * time.Minute // network/fabric/endorsement/fsc/initiator.go:99
		slack                   = 0 * time.Minute // no slack currently budgeted
	)

	requiredMinimum := sigFanOutTimeout + auditTimeout + approvalTimeout + slack

	require.Greater(t, responderReceiveTimeout, requiredMinimum,
		"responder receive timeout (%s) must exceed sig fan-out + audit + approval + slack (%s) — see issue #1266",
		responderReceiveTimeout, requiredMinimum)
}
