/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage"
)

// TxStatus is the status of a transaction
type TxStatus = storage.TxStatus

const (
	// Unknown is the status of a transaction that is unknown
	Unknown TxStatus = storage.Unknown
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending TxStatus = storage.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed TxStatus = storage.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted TxStatus = storage.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan TxStatus = storage.Orphan
)

// TxStatusMessage maps TxStatus to string
var TxStatusMessage = storage.TxStatusMessage
