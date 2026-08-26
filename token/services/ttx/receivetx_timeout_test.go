package ttx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReceiveTransactionTimeoutBudget asserts the invariant that the responder's
// overall receive timeout must not be less than the sum of every wait it depends
// on: sig fan-out + audit + approval.
//
// KNOWN FAILURE (issue #1266): as of this test's authorship, the responder budget
// equals the sum of its FSC-endorsement-mode components exactly, with zero slack.
// Do not "fix" this test by loosening the invariant — fix the source constants.
func TestReceiveTransactionTimeoutBudget(t *testing.T) {
	const (
		sigFanOutTimeout = 1 * time.Minute // endorse.go:111
		auditTimeout     = 1 * time.Minute // auditor.go:189
		approvalTimeout  = 2 * time.Minute // network/fabric/endorsement/fsc/initiator.go:99
	)

	requiredMinimum := sigFanOutTimeout + auditTimeout + approvalTimeout

	require.GreaterOrEqual(t, DefaultReceiveTransactionTimeout, requiredMinimum,
		"responder receive timeout (%s) must not be less than sig fan-out + audit + approval (%s) — see issue #1266",
		DefaultReceiveTransactionTimeout, requiredMinimum)
}
