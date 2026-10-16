/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/stretchr/testify/require"
)

// TestReceiveTransactionTimeoutBudget asserts the invariant that the responder's
// overall receive timeout must exceed the sum of every wait it depends on.
//
// Signature collection runs as two serial phases (requestSignaturesOnIssues then
// requestSignaturesOnTransfers), unconditionally, for any transaction carrying
// both. Each phase fans out concurrently and is bounded by AnswerCollectionTimeout,
// so a transaction with issues and transfers contributes two phase bounds.
// See issue #1266.
func TestReceiveTransactionTimeoutBudget(t *testing.T) {
	const signaturePhases = 2

	requiredMinimum := signaturePhases*ttx.AnswerCollectionTimeout +
		ttx.AuditTimeout +
		fsc.ApprovalTimeout

	require.Greater(t, ttx.DefaultReceiveTransactionTimeout, requiredMinimum,
		"responder receive timeout (%s) must exceed %d signature phases + audit + approval (%s) — see issue #1266",
		ttx.DefaultReceiveTransactionTimeout, signaturePhases, requiredMinimum)
}
