/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package db

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

// QueryTransactionsParams defines the parameters for querying movements
type QueryTransactionsParams = ttxdb.QueryTransactionsParams

// Pagination describe a moving page
type Pagination = cdriver.Pagination

// TransactionRecord is a more finer-grained version of a movement record.
// Given a Token Transaction, for each token action in the Token Request,
// a transaction record is created for each unique enrollment ID found in the outputs.
// The transaction record contains the total amount of the token type that was transferred to/from that enrollment ID
// in that action.
type TransactionRecord = storage.TransactionRecord

// PageTransactionsIterator is an iterator of *TransactionRecord with support for pagination
type PageTransactionsIterator = cdriver.PageIterator[*TransactionRecord]

// TransactionStatusEvent models an event related to the status of a transaction
type TransactionStatusEvent = storage.StatusEvent
