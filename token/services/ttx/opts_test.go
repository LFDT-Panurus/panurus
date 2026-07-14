/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests opts.go which provides option patterns for transaction configuration.
// Tests verify proper option application, composition, and edge cases.
package ttx_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompileOpts_Empty verifies that CompileOpts works with no options.
func TestCompileOpts_Empty(t *testing.T) {
	opts, err := ttx.CompileOpts()

	require.NoError(t, err)
	require.NotNil(t, opts)
	assert.Nil(t, opts.Auditor)
	assert.Empty(t, opts.TMSID.Network)
	assert.Empty(t, opts.TMSID.Channel)
	assert.Empty(t, opts.TMSID.Namespace)
	assert.False(t, opts.NoTransactionVerification)
	assert.Zero(t, opts.Timeout)
	assert.Empty(t, opts.TxID)
	assert.Nil(t, opts.Transaction)
	assert.Empty(t, opts.NetworkTxID)
	assert.False(t, opts.NoCachingRequest)
	assert.False(t, opts.AnonymousTransaction)
}

// TestWithAuditor verifies the WithAuditor option.
func TestWithAuditor(t *testing.T) {
	auditor := view.Identity("auditor-identity")

	opts, err := ttx.CompileOpts(ttx.WithAuditor(auditor))

	require.NoError(t, err)
	assert.Equal(t, auditor, opts.Auditor)
}

// TestWithNetwork verifies the WithNetwork option.
func TestWithNetwork(t *testing.T) {
	network := "test-network"

	opts, err := ttx.CompileOpts(ttx.WithNetwork(network))

	require.NoError(t, err)
	assert.Equal(t, network, opts.TMSID.Network)
	assert.Empty(t, opts.TMSID.Channel)
	assert.Empty(t, opts.TMSID.Namespace)
}

// TestWithChannel verifies the WithChannel option.
func TestWithChannel(t *testing.T) {
	channel := "test-channel"

	opts, err := ttx.CompileOpts(ttx.WithChannel(channel))

	require.NoError(t, err)
	assert.Equal(t, channel, opts.TMSID.Channel)
	assert.Empty(t, opts.TMSID.Network)
	assert.Empty(t, opts.TMSID.Namespace)
}

// TestWithNamespace verifies the WithNamespace option.
func TestWithNamespace(t *testing.T) {
	namespace := "test-namespace"

	opts, err := ttx.CompileOpts(ttx.WithNamespace(namespace))

	require.NoError(t, err)
	assert.Equal(t, namespace, opts.TMSID.Namespace)
	assert.Empty(t, opts.TMSID.Network)
	assert.Empty(t, opts.TMSID.Channel)
}

// TestWithTMS verifies the WithTMS option sets all TMS fields.
func TestWithTMS(t *testing.T) {
	network := "test-network"
	channel := "test-channel"
	namespace := "test-namespace"

	opts, err := ttx.CompileOpts(ttx.WithTMS(network, channel, namespace))

	require.NoError(t, err)
	assert.Equal(t, network, opts.TMSID.Network)
	assert.Equal(t, channel, opts.TMSID.Channel)
	assert.Equal(t, namespace, opts.TMSID.Namespace)
}

// TestWithTMS_EmptyValues verifies WithTMS works with empty strings.
func TestWithTMS_EmptyValues(t *testing.T) {
	opts, err := ttx.CompileOpts(ttx.WithTMS("", "", ""))

	require.NoError(t, err)
	assert.Empty(t, opts.TMSID.Network)
	assert.Empty(t, opts.TMSID.Channel)
	assert.Empty(t, opts.TMSID.Namespace)
}

// TestWithTMSID verifies the WithTMSID option.
func TestWithTMSID(t *testing.T) {
	tmsID := token.TMSID{
		Network:   "net1",
		Channel:   "chan1",
		Namespace: "ns1",
	}

	opts, err := ttx.CompileOpts(ttx.WithTMSID(tmsID))

	require.NoError(t, err)
	assert.Equal(t, tmsID, opts.TMSID)
}

// TestWithTMSIDPointer_NonNil verifies WithTMSIDPointer with a non-nil pointer.
func TestWithTMSIDPointer_NonNil(t *testing.T) {
	tmsID := &token.TMSID{
		Network:   "net1",
		Channel:   "chan1",
		Namespace: "ns1",
	}

	opts, err := ttx.CompileOpts(ttx.WithTMSIDPointer(tmsID))

	require.NoError(t, err)
	assert.Equal(t, *tmsID, opts.TMSID)
}

// TestWithTMSIDPointer_Nil verifies WithTMSIDPointer with a nil pointer does nothing.
func TestWithTMSIDPointer_Nil(t *testing.T) {
	opts, err := ttx.CompileOpts(ttx.WithTMSIDPointer(nil))

	require.NoError(t, err)
	assert.Empty(t, opts.TMSID.Network)
	assert.Empty(t, opts.TMSID.Channel)
	assert.Empty(t, opts.TMSID.Namespace)
}

// TestWithNoCachingRequest verifies the WithNoCachingRequest option.
func TestWithNoCachingRequest(t *testing.T) {
	opts, err := ttx.CompileOpts(ttx.WithNoCachingRequest())

	require.NoError(t, err)
	assert.True(t, opts.NoCachingRequest)
}

// TestWithNoTransactionVerification verifies the WithNoTransactionVerification option.
func TestWithNoTransactionVerification(t *testing.T) {
	opts, err := ttx.CompileOpts(ttx.WithNoTransactionVerification())

	require.NoError(t, err)
	assert.True(t, opts.NoTransactionVerification)
}

// TestWithTimeout verifies the WithTimeout option.
func TestWithTimeout(t *testing.T) {
	timeout := 30 * time.Second

	opts, err := ttx.CompileOpts(ttx.WithTimeout(timeout))

	require.NoError(t, err)
	assert.Equal(t, timeout, opts.Timeout)
}

// TestWithTxID verifies the WithTxID option.
func TestWithTxID(t *testing.T) {
	txID := "test-tx-id-123"

	opts, err := ttx.CompileOpts(ttx.WithTxID(txID))

	require.NoError(t, err)
	assert.Equal(t, txID, opts.TxID)
}

// TestWithTransactions verifies the WithTransactions option.
func TestWithTransactions(t *testing.T) {
	tx := &ttx.Transaction{}

	opts, err := ttx.CompileOpts(ttx.WithTransactions(tx))

	require.NoError(t, err)
	assert.Equal(t, tx, opts.Transaction)
}

// TestWithNetworkTxID verifies the WithNetworkTxID option.
func TestWithNetworkTxID(t *testing.T) {
	networkTxID := network.TxID{
		Nonce:   []byte("nonce-123"),
		Creator: []byte("creator-456"),
	}

	opts, err := ttx.CompileOpts(ttx.WithNetworkTxID(networkTxID))

	require.NoError(t, err)
	assert.Equal(t, networkTxID, opts.NetworkTxID)
}

// TestWithAnonymousTransaction_True verifies WithAnonymousTransaction with true.
func TestWithAnonymousTransaction_True(t *testing.T) {
	opts, err := ttx.CompileOpts(ttx.WithAnonymousTransaction(true))

	require.NoError(t, err)
	assert.True(t, opts.AnonymousTransaction)
}

// TestWithAnonymousTransaction_False verifies WithAnonymousTransaction with false.
func TestWithAnonymousTransaction_False(t *testing.T) {
	opts, err := ttx.CompileOpts(ttx.WithAnonymousTransaction(false))

	require.NoError(t, err)
	assert.False(t, opts.AnonymousTransaction)
}

// TestCompileOpts_MultipleOptions verifies that multiple options can be composed.
func TestCompileOpts_MultipleOptions(t *testing.T) {
	auditor := view.Identity("auditor")
	timeout := 45 * time.Second
	txID := "tx-123"

	opts, err := ttx.CompileOpts(
		ttx.WithAuditor(auditor),
		ttx.WithTMS("net", "chan", "ns"),
		ttx.WithTimeout(timeout),
		ttx.WithTxID(txID),
		ttx.WithNoCachingRequest(),
		ttx.WithNoTransactionVerification(),
		ttx.WithAnonymousTransaction(true),
	)

	require.NoError(t, err)
	assert.Equal(t, auditor, opts.Auditor)
	assert.Equal(t, "net", opts.TMSID.Network)
	assert.Equal(t, "chan", opts.TMSID.Channel)
	assert.Equal(t, "ns", opts.TMSID.Namespace)
	assert.Equal(t, timeout, opts.Timeout)
	assert.Equal(t, txID, opts.TxID)
	assert.True(t, opts.NoCachingRequest)
	assert.True(t, opts.NoTransactionVerification)
	assert.True(t, opts.AnonymousTransaction)
}

// TestCompileOpts_OverridingOptions verifies that later options override earlier ones.
func TestCompileOpts_OverridingOptions(t *testing.T) {
	opts, err := ttx.CompileOpts(
		ttx.WithNetwork("net1"),
		ttx.WithNetwork("net2"),
		ttx.WithChannel("chan1"),
		ttx.WithChannel("chan2"),
	)

	require.NoError(t, err)
	assert.Equal(t, "net2", opts.TMSID.Network)
	assert.Equal(t, "chan2", opts.TMSID.Channel)
}

// TestCompileServiceOptions_Empty verifies CompileServiceOptions with no options.
func TestCompileServiceOptions_Empty(t *testing.T) {
	opts, err := ttx.CompileServiceOptions()

	require.NoError(t, err)
	require.NotNil(t, opts)
	assert.Nil(t, opts.Params)
}

// TestWithRecipientData verifies the WithRecipientData option.
func TestWithRecipientData(t *testing.T) {
	recipientData := &ttx.RecipientData{}

	opts, err := ttx.CompileServiceOptions(ttx.WithRecipientData(recipientData))

	require.NoError(t, err)
	require.NotNil(t, opts.Params)
	assert.Equal(t, recipientData, opts.Params["RecipientData"])
}

// TestWithRecipientData_MultipleParams verifies WithRecipientData preserves existing params.
func TestWithRecipientData_MultipleParams(t *testing.T) {
	recipientData := &ttx.RecipientData{}

	opts, err := ttx.CompileServiceOptions(
		ttx.WithRecipientData(recipientData),
		ttx.WithRecipientWalletID("wallet-123"),
	)

	require.NoError(t, err)
	require.NotNil(t, opts.Params)
	assert.Equal(t, recipientData, opts.Params["RecipientData"])
	assert.Equal(t, "wallet-123", opts.Params["RecipientWalletID"])
}

// TestWithRecipientWalletID verifies the WithRecipientWalletID option.
func TestWithRecipientWalletID(t *testing.T) {
	walletID := "wallet-456"

	opts, err := ttx.CompileServiceOptions(ttx.WithRecipientWalletID(walletID))

	require.NoError(t, err)
	require.NotNil(t, opts.Params)
	assert.Equal(t, walletID, opts.Params["RecipientWalletID"])
}

// TestWithRecipientWalletID_Empty verifies WithRecipientWalletID with empty string does nothing.
func TestWithRecipientWalletID_Empty(t *testing.T) {
	opts, err := ttx.CompileServiceOptions(ttx.WithRecipientWalletID(""))

	require.NoError(t, err)
	assert.Nil(t, opts.Params)
}

// TestWithRecipientWalletID_EmptyAfterOtherOption verifies empty wallet ID doesn't remove params.
func TestWithRecipientWalletID_EmptyAfterOtherOption(t *testing.T) {
	recipientData := &ttx.RecipientData{}

	opts, err := ttx.CompileServiceOptions(
		ttx.WithRecipientData(recipientData),
		ttx.WithRecipientWalletID(""),
	)

	require.NoError(t, err)
	require.NotNil(t, opts.Params)
	assert.Equal(t, recipientData, opts.Params["RecipientData"])
	_, hasWalletID := opts.Params["RecipientWalletID"]
	assert.False(t, hasWalletID)
}
