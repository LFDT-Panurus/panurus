package ttx_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/stretchr/testify/require"
)

// requiredSlack is the margin the responder budget must keep above the sum of
// its component legs, to absorb network jitter and scheduling delay.
const requiredSlack = 1 * time.Minute

// TestReceiveTransactionTimeoutBudget asserts the invariant that the responder's
// overall receive timeout must exceed the sum of every wait it depends on:
// sig fan-out + audit + approval + slack. See issue #1266.
func TestReceiveTransactionTimeoutBudget(t *testing.T) {
	requiredMinimum := ttx.SigFanOutTimeout + ttx.AuditTimeout + fsc.ApprovalTimeout + requiredSlack

	require.Greater(t, ttx.DefaultReceiveTransactionTimeout, requiredMinimum,
		"responder receive timeout (%s) must exceed sig fan-out + audit + approval + slack (%s) — see issue #1266",
		ttx.DefaultReceiveTransactionTimeout, requiredMinimum)
}
