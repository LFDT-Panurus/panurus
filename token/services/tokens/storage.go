/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

const (
	AddToken    = "store-token"
	DeleteToken = "delete-token"
)

// Flags represents the ownership and auditing roles associated with a token.
type Flags struct {
	// Mine is true if the token belongs to one of my wallets.
	Mine bool
	// Auditor is true if I am an auditor for this token.
	Auditor bool
	// Issuer is true if I issued this token.
	Issuer bool
	// Redeemed is true if this token is a redeem (empty owner) attributed to one of my issuer identities.
	Redeemed bool
}

// DBStorage provides a high-level wrapper over TokenDB for managing token persistence.
// It handles transaction orchestration and event notification.
type DBStorage struct {
	// Notifier is used to publish events when tokens are added or deleted.
	Notifier events.Publisher
	// TokenDB is the underlying persistent store for tokens.
	TokenDB *tokendb.StoreService
	// TMSID is the identifier for the TMS this storage belongs to.
	TMSID token.TMSID
}

// NewDBStorage creates a new DBStorage instance.
func NewDBStorage(notifier events.Publisher, tokenDB *tokendb.StoreService, tmsID token.TMSID) (*DBStorage, error) {
	return &DBStorage{
		Notifier: notifier,
		TokenDB:  tokenDB,
		TMSID:    tmsID,
	}, nil
}

// NewTransaction starts a new transaction for local storage operations.
func (d *DBStorage) NewTransaction() (*DBTransaction, error) {
	tx, err := d.TokenDB.NewTransaction()
	if err != nil {
		return nil, err
	}

	return NewTransaction(d.Notifier, tx, d.TMSID)
}

// ContinueTransaction starts a new transaction for local storage operations.
func (d *DBStorage) ContinueTransaction(tx dbdriver.Transaction) (*DBTransaction, error) {
	ctx, err := d.TokenDB.ContinueTransaction(tx)
	if err != nil {
		return nil, err
	}

	return NewTransaction(d.Notifier, ctx, d.TMSID)
}

// TransactionExists checks if a transaction with the given ID has already been recorded in local storage.
func (d *DBStorage) TransactionExists(ctx context.Context, id string) (bool, error) {
	return d.TokenDB.TransactionExists(ctx, id)
}

// StorePublicParams persists the public parameters associated with the TMS.
func (d *DBStorage) StorePublicParams(ctx context.Context, raw []byte) error {
	return d.TokenDB.StorePublicParams(ctx, raw)
}

// TokenToAppend contains the detailed information required to store a new token in the database.
type TokenToAppend struct {
	// TxID is the transaction ID that created this token.
	TxID string
	// Index is the output index of the token within the transaction.
	Index uint64
	// Tok is the de-obfuscated token information.
	Tok *token2.Token
	// TokenOnLedgerFormat is the format of the token as it appears on the ledger.
	TokenOnLedgerFormat token2.Format
	// TokenOnLedger is the raw byte representation of the token output on the ledger.
	TokenOnLedger []byte
	// TokenOnLedgerMetadata is the metadata associated with the token output on the ledger.
	TokenOnLedgerMetadata []byte
	// OwnerType is the type of the token owner's identity.
	OwnerType string
	// OwnerIdentity is the de-obfuscated identity of the owner.
	OwnerIdentity token.Identity
	// OwnerWalletID is the local wallet identifier if the owner is mine.
	OwnerWalletID string
	// Owners is the list of unique identifiers for all recipients/owners.
	Owners []string
	// Issuer is the identity of the token issuer.
	Issuer token.Identity
	// Precision is the number of decimal places for quantity calculations.
	Precision uint64
	// Flags indicates my relationship with this token.
	Flags Flags
}

// DBTransaction encapsulates a single atomic update to the token database.
type DBTransaction struct {
	// Notifier is used to publish events upon successful deletion or addition.
	Notifier events.Publisher
	// Tx is the underlying database transaction.
	Tx *tokendb.Transaction
	// TMSID is the TMS identifier for the transaction.
	TMSID token.TMSID
}

// NewTransaction creates a new transaction wrapper.
func NewTransaction(notifier events.Publisher, tx *tokendb.Transaction, tmsID token.TMSID) (*DBTransaction, error) {
	return &DBTransaction{
		Notifier: notifier,
		Tx:       tx,
		TMSID:    tmsID,
	}, nil
}

// DeleteToken removes a single token from the database and notifies listeners.
func (t *DBTransaction) DeleteToken(ctx context.Context, tokenID token2.ID, deletedBy string) error {
	tok, owners, err := t.Tx.GetToken(ctx, tokenID, true)
	if err != nil {
		return errors.WithMessagef(err, "failed to get token [%s]", tokenID)
	}

	err = t.Tx.Delete(ctx, tokenID, deletedBy)
	if err != nil {
		if tok == nil {
			logger.DebugfContext(ctx, "nothing further to delete for [%s]", tokenID)

			return nil
		}

		return errors.WithMessagef(err, "failed to delete token [%s]", tokenID)
	}
	if tok == nil {
		logger.DebugfContext(ctx, "nothing further to delete for [%s]", tokenID)

		return nil
	}
	logger.DebugfContext(ctx, "Notify owners")
	for _, owner := range owners {
		logger.DebugfContext(ctx, "post new delete-token event [%s:%s]", tokenID, owner)
		t.Notify(ctx, DeleteToken, t.TMSID, owner, tok.Type, tokenID.TxId, tokenID.Index)
	}

	return nil
}

// DeleteTokens removes multiple tokens from the database.
func (t *DBTransaction) DeleteTokens(ctx context.Context, deletedBy string, ids []*token2.ID) error {
	for _, id := range ids {
		if err := t.DeleteToken(ctx, *id, deletedBy); err != nil {
			return err
		}
	}

	return nil
}

// AppendToken records a new token in the database and notifies listeners.
func (t *DBTransaction) AppendToken(ctx context.Context, tta TokenToAppend) error {
	q, err := token2.ToQuantity(tta.Tok.Quantity, tta.Precision)
	if err != nil {
		return errors.Wrapf(err, "cannot covert [%s] with precision [%d]", tta.Tok.Quantity, tta.Precision)
	}

	// The owner_raw and owner_identity columns are NOT NULL. A redeem token carries an
	// empty owner, so ensure these are non-nil to avoid violating the constraint.
	ownerRaw := tta.Tok.Owner
	if ownerRaw == nil {
		ownerRaw = []byte{}
	}
	ownerIdentity := tta.OwnerIdentity
	if ownerIdentity == nil {
		ownerIdentity = []byte{}
	}

	err = t.Tx.StoreToken(ctx, tokendb.TokenRecord{
		TxID:           tta.TxID,
		Index:          tta.Index,
		IssuerRaw:      tta.Issuer,
		OwnerRaw:       ownerRaw,
		OwnerType:      tta.OwnerType,
		OwnerIdentity:  ownerIdentity,
		OwnerWalletID:  tta.OwnerWalletID,
		Ledger:         tta.TokenOnLedger,
		LedgerFormat:   tta.TokenOnLedgerFormat,
		LedgerMetadata: tta.TokenOnLedgerMetadata,
		Quantity:       tta.Tok.Quantity,
		Type:           tta.Tok.Type,
		Amount:         q.ToBigInt().Uint64(),
		Owner:          tta.Flags.Mine,
		Auditor:        tta.Flags.Auditor,
		Issuer:         tta.Flags.Issuer,
		Redeemed:       tta.Flags.Redeemed,
	}, tta.Owners)
	if err != nil && !errors.HasCause(err, driver.UniqueKeyViolation) {
		return errors.Wrapf(err, "cannot store token in db")
	}

	logger.DebugfContext(ctx, "Notify owners")
	for _, id := range tta.Owners {
		if len(id) == 0 {
			continue
		}
		t.Notify(ctx, AddToken, t.TMSID, id, tta.Tok.Type, tta.TxID, tta.Index)
	}

	return nil
}

// Notify publishes a token-related event to the system's notification bus.
func (t *DBTransaction) Notify(ctx context.Context, topic string, tmsID token.TMSID, walletID string, tokenType token2.Type, txID string, index uint64) {
	if t.Notifier == nil {
		logger.WarnfContext(ctx, "cannot notify others!")

		return
	}

	e := NewTokenProcessorEvent(topic, &TokenMessage{
		TMSID:     tmsID,
		WalletID:  walletID,
		TokenType: tokenType,
		TxID:      txID,
		Index:     index,
	})

	logger.DebugfContext(ctx, "publish new event %v", e)
	t.Notifier.Publish(e)
}

// Rollback cancels all changes made in the transaction.
func (t *DBTransaction) Rollback() error {
	return t.Tx.Rollback()
}

// Commit persists all changes made in the transaction.
func (t *DBTransaction) Commit() error {
	return t.Tx.Commit()
}

// SetSpendableFlag updates the spendable status for the given tokens in the database.
func (t *DBTransaction) SetSpendableFlag(ctx context.Context, value bool, ids []*token2.ID) error {
	var err error
	for _, id := range ids {
		err = t.Tx.SetSpendable(ctx, *id, value)
		if err != nil {
			return err
		}
	}

	return nil
}

// SetSpendableBySupportedTokenTypes marks all tokens matching the given formats as spendable.
func (t *DBTransaction) SetSpendableBySupportedTokenTypes(ctx context.Context, supportedTokens []token2.Format) error {
	return t.Tx.SetSpendableBySupportedTokenFormats(ctx, supportedTokens)
}

// TokenProcessorEvent encapsulates a message published by the Tokens Service.
type TokenProcessorEvent struct {
	topic   string
	message TokenMessage
}

// NewTokenProcessorEvent creates a new event with the given topic and message.
func NewTokenProcessorEvent(topic string, message *TokenMessage) *TokenProcessorEvent {
	return &TokenProcessorEvent{
		topic:   topic,
		message: *message,
	}
}

// TokenMessage contains the details of a token-related event.
type TokenMessage struct {
	// TMSID is the TMS identifier for the event.
	TMSID token.TMSID
	// WalletID is the unique identifier of the wallet affected by the event.
	WalletID string
	// TokenType is the type of the token.
	TokenType token2.Type
	// TxID is the transaction ID.
	TxID string
	// Index is the token index within the transaction.
	Index uint64
}

// Topic returns the event's topic.
func (t *TokenProcessorEvent) Topic() string {
	return t.topic
}

// Message returns the event's payload.
func (t *TokenProcessorEvent) Message() any {
	return t.message
}
