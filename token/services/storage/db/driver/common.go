/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

// ActionType is the type of transaction
type ActionType int

const (
	// Issue is the action type for issuing tokens.
	Issue ActionType = iota
	// Transfer is the action type for transferring tokens.
	Transfer
	// Redeem is the action type for redeeming tokens.
	Redeem
)

// SearchDirection defines the direction of a search.
type SearchDirection int

const (
	// FromLast defines the direction of a search from the last key.
	FromLast SearchDirection = iota
	// FromBeginning defines the direction of a search from the first key.
	FromBeginning
)

// MovementDirection defines the direction of a movement.
type MovementDirection int

const (
	// Sent amount transferred from.
	Sent MovementDirection = iota
	// Received amount transferred to.
	Received
	// All amount transferred to and from.
	All
)

// TxStatus is the status of a transaction
type TxStatus = driver2.TxStatus

const (
	// Unknown is the status of a transaction that is unknown
	Unknown = driver2.Unknown
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending = driver2.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed = driver2.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted = driver2.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan = driver2.Orphan
)

// TxStatusMessage maps TxStatus to string
var TxStatusMessage = map[TxStatus]string{
	Unknown:   "Unknown",
	Pending:   "Pending",
	Confirmed: "Confirmed",
	Deleted:   "Deleted",
	Orphan:    "Orphan",
}

// MovementRecord is a record of a movement of assets.
// Given a Token Transaction, a movement record is created for each enrollment ID that participated in the transaction
// and each token type that was transferred.
// The movement record contains the total amount of the token type that was transferred to/from the enrollment ID
// in a given token transaction.
type MovementRecord struct {
	// TxID is the transaction ID
	TxID string
	// EnrollmentID is the enrollment ID of the account that is receiving or sending
	EnrollmentID string
	// TokenType is the type of token
	TokenType token2.Type
	// Amount is positive if tokens are received. Negative otherwise
	Amount *big.Int
	// Timestamp is the time the transaction was submitted to the db
	Timestamp time.Time
	// Status is the status of the transaction
	Status TxStatus
}

func (r MovementRecord) String() string {
	return fmt.Sprintf("[%s:%s:%s:%d:%d]", r.TxID, r.EnrollmentID, r.TokenType, r.Amount.Int64(), r.Status)
}

// TransactionRecord is a more finer-grained version of a movement record.
// Given a Token Transaction, for each token action in the Token Request,
// a transaction record is created for each unique enrollment ID found in the outputs.
// The transaction record contains the total amount of the token type that was transferred to/from that enrollment ID
// in that action.
type TransactionRecord struct {
	// TxID is the transaction ID
	TxID string
	// ActionType is the type of action performed by this transaction record
	ActionType ActionType
	// SenderEID is the enrollment ID of the account that is sending tokens
	SenderEID string
	// RecipientEID is the enrollment ID of the account that is receiving tokens
	RecipientEID string
	// TokenType is the type of token
	TokenType token2.Type
	// Amount is positive if tokens are received. Negative otherwise
	Amount *big.Int
	// Timestamp is the time the transaction was submitted to the db
	Timestamp time.Time
	// RecoveryClaimedBy identifies the recovery worker/replica currently holding the lease for this transaction.
	RecoveryClaimedBy string
	// RecoveryClaimExpiresAt is the lease expiration for the current recovery claim, if any.
	RecoveryClaimExpiresAt *time.Time
	// Status is the status of the transaction
	Status TxStatus
	// ApplicationMetadata is the metadata sent by the application in the
	// transient field. It is not validated or recorded on the ledger.
	ApplicationMetadata map[string][]byte
	// PublicMetadata is the metadata that is stored on the ledger as part
	// of an Issuance or Transfer Action (for instance the HTLC hash).
	PublicMetadata map[string][]byte
}

func (t TransactionRecord) String() string {
	var s strings.Builder
	s.WriteString("{")
	s.WriteString(t.TxID)
	s.WriteString(" ")
	s.WriteString(strconv.Itoa(int(t.ActionType)))
	s.WriteString(" ")
	s.WriteString(t.SenderEID)
	s.WriteString(" ")
	s.WriteString(t.RecipientEID)
	s.WriteString(" ")
	s.WriteString(string(t.TokenType))
	s.WriteString(" ")
	s.WriteString(t.Amount.String())
	s.WriteString(" ")
	s.WriteString(t.Timestamp.String())
	s.WriteString(" ")
	s.WriteString(TxStatusMessage[t.Status])
	s.WriteString("}")

	return s.String()
}

type TokenRequestRecord struct {
	// TxID is the transaction ID
	TxID string
	// TokenRequest is the token request marshalled
	TokenRequest []byte
	// Status is the status of the transaction
	Status TxStatus
}

// TransactionIterator is an iterator for transactions
type TransactionIterator = iterators.Iterator[*TransactionRecord]

// TokenRequestIterator is an iterator for token requests
type TokenRequestIterator = iterators.Iterator[*TokenRequestRecord]

// QueryMovementsParams defines the parameters for querying movements.
// Movement records will be filtered by EnrollmentID, TokenFormat, and Status.
// SearchDirection tells if the search should start from the oldest to the newest records or vice versa.
// MovementDirection which amounts to consider. Sent correspond to a negative amount,
// Received to a positive amount, and All to both.
type QueryMovementsParams struct {
	// EnrollmentIDs is the enrollment IDs of the accounts to query
	EnrollmentIDs []string
	// TokenTypes is the token types to query
	TokenTypes []token2.Type
	// TxStatuses is the statuses of the transactions to query
	TxStatuses []TxStatus
	// SearchDirection is the direction of the search
	SearchDirection SearchDirection
	// MovementDirection is the direction of the movement
	MovementDirection MovementDirection
	// NumRecords is the number of records to return
	// If 0, all records are returned
	NumRecords int
}

// QueryTransactionsParams defines the parameters for querying transactions.
// One can filter by sender, by recipient, and by time range.
type QueryTransactionsParams struct {
	// IDs is the list of transaction ids. If nil or empty, all transactions are returned
	IDs []string
	// ExcludeToSelf can be used to filter out 'change' transactions where the sender and
	// recipient have the same enrollment id.
	ExcludeToSelf bool
	// SenderWallet is the wallet of the sender
	// If empty, any sender is accepted
	// If the sender does not match but the recipient matches, the transaction is returned
	SenderWallet string
	// RecipientWallet is the wallet of the recipient
	// If empty, any recipient is accepted
	// If the recipient does not match but the sender matches, the transaction is returned
	RecipientWallet string
	// From is the start time of the query
	// If nil, the query starts from the first transaction
	From *time.Time
	// To is the end time of the query
	// If nil, the query ends at the last transaction
	To *time.Time
	// ActionTypes is the list of action types to accept
	// If empty, any action type is accepted
	ActionTypes []ActionType
	// Statuses is the list of transaction status to accept
	// If empty, any status is accepted
	Statuses []TxStatus
	// TokenTypes is the list of token types to accept
	// If empty, any token type is accepted
	TokenTypes []token2.Type
	// SearchDirection is the direction of the search.
	// Default (zero value) is FromLast (descending by stored_at).
	SearchDirection SearchDirection
}

// QueryTokenRequestsParams defines the parameters for querying token requests
type QueryTokenRequestsParams struct {
	// Statuses is the list of transaction status to accept
	// If empty, any status is accepted
	Statuses []TxStatus
}
