/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelectorInsufficientFunds verifies the error constant is defined
func TestSelectorInsufficientFunds(t *testing.T) {
	require.Error(t, SelectorInsufficientFunds)
	assert.Contains(t, SelectorInsufficientFunds.Error(), "insufficient funds")
}

// TestSelectorSufficientButLockedFunds verifies the error constant is defined
func TestSelectorSufficientButLockedFunds(t *testing.T) {
	require.Error(t, SelectorSufficientButLockedFunds)
	assert.Contains(t, SelectorSufficientButLockedFunds.Error(), "sufficient but partially locked funds")
}

// TestSelectorSufficientButNotCertifiedFunds verifies the error constant is defined
func TestSelectorSufficientButNotCertifiedFunds(t *testing.T) {
	require.Error(t, SelectorSufficientButNotCertifiedFunds)
	assert.Contains(t, SelectorSufficientButNotCertifiedFunds.Error(), "sufficient but partially not certified")
}

// TestSelectorSufficientFundsButConcurrencyIssue verifies the error constant is defined
func TestSelectorSufficientFundsButConcurrencyIssue(t *testing.T) {
	require.Error(t, SelectorSufficientFundsButConcurrencyIssue)
	assert.Contains(t, SelectorSufficientFundsButConcurrencyIssue.Error(), "sufficient funds but concurrency issue")
}

// TestSelectorTimedOut verifies the error constant is defined
func TestSelectorTimedOut(t *testing.T) {
	require.Error(t, SelectorTimedOut)
	assert.Contains(t, SelectorTimedOut.Error(), "timed out")
}
