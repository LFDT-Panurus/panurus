/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

// The driver's error classes (design §13). A caller decides what to do with a failure by class, not
// by reading a message, so the ones the driver can actually tell apart are sentinels it wraps with.
//
// The distinction that earns its keep here is permanent versus transient. A transaction the chain has
// rejected will be rejected again by every retry; a node that could not be reached says nothing about
// the transaction at all. Collapsing the two, which is what an unclassified error does, leaves a
// caller with a choice between retrying a doomed transaction forever and giving up on a working one.
var (
	// ErrTransactionReverted marks a transaction the chain has rejected: it reverted, either when the
	// node executed it for an estimate or once it was mined. Permanent - the request has to be
	// re-derived against current state, not resent (§13, "map to Invalid").
	ErrTransactionReverted = errors.New("transaction is not valid: reverted on chain")

	// ErrNetworkUnavailable marks a failure to reach or be understood by the node. Transient - the
	// transaction is untouched and the call should be retried with backoff (§13).
	ErrNetworkUnavailable = errors.New("evm node unavailable")

	// ErrTransactionRejected marks a transaction the node refused to accept for submission: it never
	// entered the mempool, so it consumed no nonce. Permanent, for the same reason a revert is: every
	// resend of the same transaction is refused the same way.
	//
	// It is always joined with ErrTransactionReverted, so a caller that only knows the two original
	// classes still reads it as the permanent one and maps it to Invalid. A caller that wants to tell
	// "the chain rejected what this would do" from "the node would not carry it" can ask for this.
	ErrTransactionRejected = errors.New("transaction rejected by the node")
)
