/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

// Action type identifiers. These strings are hashed into the binding
// signature's message (see prover.ComputeActionHash) and must be identical
// wherever an action of that type is signed or, eventually, verified.
const (
	ActionTypeIssue    = "issue"
	ActionTypeTransfer = "transfer"
)
