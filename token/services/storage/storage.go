/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package storage

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// TxStatus is the status of a transaction
type TxStatus = dbdriver.TxStatus

const (
	// Unknown is the status of a transaction that is unknown
	Unknown = dbdriver.Unknown
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending = dbdriver.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed = dbdriver.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted = dbdriver.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan = dbdriver.Orphan
)

// TxStatusMessage maps TxStatus to string
var TxStatusMessage = dbdriver.TxStatusMessage

type (
	// TransactionRecord is a more finer-grained version of a movement record.
	// Given a Token Transaction, for each token action in the Token Request,
	// a transaction record is created for each unique enrollment ID found in the outputs.
	// The transaction record contains the total amount of the token type that was transferred to/from that enrollment ID
	// in that action.
	TransactionRecord     = dbdriver.TransactionRecord
	TokenRequestRecord    = dbdriver.TokenRequestRecord
	WalletID              = dbdriver.WalletID
	IdentityConfiguration = dbdriver.IdentityConfiguration
	WalletStore           = dbdriver.WalletStore
	IdentityStore         = dbdriver.IdentityStore
	KeyStore              = dbdriver.KeyStore
	// StatusEvent models an event related to the status of a transaction
	StatusEvent                 = common.StatusEvent
	TransactionStoreTransaction = dbdriver.TransactionStoreTransaction
)
