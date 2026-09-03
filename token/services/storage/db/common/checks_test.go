/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AsNamedChecker feeds the legacy plain-message Checker contract, which both the
// on-demand Check API and integration tests treat as "any message is an error".
// SeverityInfo findings are documented as expected to resolve on their own (a
// transaction the ledger has not caught up with yet, for example), so they must
// not surface there - a node restart or a slow index would otherwise turn a
// benign condition into a false alarm on every legacy caller.
func TestAsNamedChecker_DropsInfoSeverity(t *testing.T) {
	checker := NamedFindingChecker{
		Name: "test checker",
		Checker: func(_ context.Context) ([]Finding, error) {
			return []Finding{
				{Checker: "test checker", Code: CodeTxStatusUnavailable, Severity: SeverityInfo, TxID: "tx-info", Message: "info finding"},
				{Checker: "test checker", Code: CodeTxStatusMismatch, Severity: SeverityWarning, TxID: "tx-warn", Message: "warning finding"},
				{Checker: "test checker", Code: CodeTxStatusMismatch, Severity: SeverityCritical, TxID: "tx-crit", Message: "critical finding"},
			}, nil
		},
	}

	named := AsNamedChecker(checker)
	assert.Equal(t, "test checker", named.Name)

	messages, err := named.Checker(t.Context())
	require.NoError(t, err)
	require.Len(t, messages, 2, "the info-severity finding must be dropped, not just the other two kept")
	assert.Contains(t, messages, Finding{Checker: "test checker", Code: CodeTxStatusMismatch, Severity: SeverityWarning, TxID: "tx-warn", Message: "warning finding"}.String())
	assert.Contains(t, messages, Finding{Checker: "test checker", Code: CodeTxStatusMismatch, Severity: SeverityCritical, TxID: "tx-crit", Message: "critical finding"}.String())
}

// A checker that reports nothing but Info findings must downgrade to zero
// messages, not a slice of empty/zero-value entries - this is what "expect zero
// errors" assertions in production callers and integration tests rely on.
func TestAsNamedChecker_AllInfoYieldsNoMessages(t *testing.T) {
	checker := NamedFindingChecker{
		Name: "test checker",
		Checker: func(_ context.Context) ([]Finding, error) {
			return []Finding{
				{Checker: "test checker", Code: CodeTxStatusUnavailable, Severity: SeverityInfo, TxID: "tx-1", Message: "info finding 1"},
				{Checker: "test checker", Code: CodeTxStatusUnavailable, Severity: SeverityInfo, TxID: "tx-2", Message: "info finding 2"},
			}, nil
		},
	}

	messages, err := AsNamedChecker(checker).Checker(t.Context())
	require.NoError(t, err)
	assert.Empty(t, messages)
}

// A checker error must still propagate rather than being swallowed by the
// severity filter.
func TestAsNamedChecker_PropagatesCheckerError(t *testing.T) {
	wantErr := assert.AnError
	checker := NamedFindingChecker{
		Name: "test checker",
		Checker: func(_ context.Context) ([]Finding, error) {
			return nil, wantErr
		},
	}

	_, err := AsNamedChecker(checker).Checker(t.Context())
	require.ErrorIs(t, err, wantErr)
}
