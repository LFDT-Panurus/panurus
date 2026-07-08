/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

import (
	"context"
	"runtime/debug"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.uber.org/zap/zapcore"
)

var logger = logging.MustGetLogger()

// MetaData defines the interface for accessing metadata associated with a token request.
type MetaData interface {
	// SpentTokenID returns the list of token identifiers that have been spent in this transaction.
	SpentTokenID() []*token2.ID
}

// GetTMSProviderFunc is a function type that returns a token management service provider.
type GetTMSProviderFunc = func() *token.ManagementServiceProvider

// UnspendableTokensIterator is an alias for the driver's UnsupportedTokensIterator.
type UnspendableTokensIterator = driver.UnsupportedTokensIterator

// Transaction models a token transaction within Panurus, providing access to its identifiers and request content.
type Transaction interface {
	// ID returns the transaction identifier.
	ID() string
	// Network returns the network name the transaction belongs to.
	Network() string
	// Channel returns the channel name.
	Channel() string
	// Namespace returns the namespace (chaincode ID) of the transaction.
	Namespace() string
	// Request returns the underlying token request.
	Request() *token.Request
}

// Cache defines the interface for caching token requests and their extracted actions.
type Cache interface {
	// Get retrieves a cache entry by key.
	Get(key string) (*CacheEntry, bool)
	// Add adds a new entry to the cache.
	Add(key string, value *CacheEntry)
	// Delete removes an entry from the cache.
	Delete(key string)
}

// CacheEntry represents a cached token request along with its pre-extracted spend and append actions.
type CacheEntry struct {
	// Request is the original token request.
	Request *token.Request
	// ToSpend is the list of token IDs to be marked as spent.
	ToSpend []*token2.ID
	// ToAppend is the list of tokens to be added to the local store.
	ToAppend []TokenToAppend
	// MsgToSign is the serialized message that was signed.
	MsgToSign []byte
}

// Service provides high-level operations for managing the local lifecycle of tokens.
// It handles the synchronization of tokens between the ledger and the local TokenDB,
// manages request caching, and provides utilities for state inspection.
type Service struct {
	tmsID token.TMSID
	// TMSProvider is used to obtain management services for different TMS IDs.
	TMSProvider TMSProvider
	// NetworkProvider is used to interact with the underlying blockchain network.
	NetworkProvider NetworkProvider
	// Storage manages the persistent storage of tokens in TokenDB.
	Storage *DBStorage
	// RequestsCache provides an in-memory cache for pending token requests to optimize commit performance.
	RequestsCache Cache
}

func NewService(tmsID token.TMSID, TMSProvider TMSProvider, networkProvider NetworkProvider, storage *DBStorage, requestsCache Cache) *Service {
	return &Service{tmsID: tmsID, TMSProvider: TMSProvider, NetworkProvider: networkProvider, Storage: storage, RequestsCache: requestsCache}
}

// AppendValid extracts actions from a token request, applies them to the local storage,
// and sets the transaction status to Confirmed. This is a convenience function that combines
// Append with SetStatus for valid/confirmed transactions.
func (t *Service) AppendValid(ctx context.Context, tx dbdriver.Transaction, txID token.RequestAnchor, request *token.Request) (err error) {
	if request == nil {
		logger.DebugfContext(ctx, "transaction [%s], no request found, skip it", txID)

		return nil
	}
	if request.Metadata == nil {
		logger.DebugfContext(ctx, "transaction [%s], no metadata found, skip it", txID)

		return nil
	}

	logger.DebugfContext(ctx, "check transaction exists")
	exists, err := t.Storage.TransactionExists(ctx, string(txID))
	if err != nil {
		logger.ErrorfContext(ctx, "transaction [%s], failed to check existence in db [%s]", txID, err)

		return errors.WithMessagef(err, "transaction [%s], failed to check existence in db", txID)
	}
	if exists {
		logger.DebugfContext(ctx, "transaction [%s], exists in db, skipping", txID)

		return nil
	}

	toSpend, toAppend, err := t.getActions(ctx, txID, request)
	if err != nil {
		return errors.WithMessagef(err, "transaction [%s], failed to extract actions", txID)
	}
	defer t.removeCachedTokenRequest(string(txID))

	logger.DebugfContext(ctx, "transaction [%s] start db transaction", txID)
	ts, err := t.Storage.ContinueTransaction(tx)
	if err != nil {
		return errors.WithMessagef(err, "transaction [%s], failed to start db transaction", txID)
	}
	defer func() {
		if err == nil {
			return
		}
		if err1 := ts.Rollback(); err1 != nil {
			logger.ErrorfContext(ctx, "error rolling back [%s][%s]", err1, string(debug.Stack()))
		} else {
			logger.InfofContext(ctx, "transaction [%s] rolled back", txID)
		}
	}()

	logger.DebugfContext(ctx, "append tokens")
	for _, tta := range toAppend {
		err = ts.AppendToken(ctx, tta)
		if err != nil {
			return errors.WithMessagef(err, "transaction [%s], failed to append token", txID)
		}
	}

	logger.DebugfContext(ctx, "delete spend tokens")
	err = ts.DeleteTokens(ctx, string(txID), toSpend)
	if err != nil {
		return errors.WithMessagef(err, "transaction [%s], failed to delete tokens", txID)
	}

	logger.DebugfContext(ctx, "ready to commit")

	return nil
}

// CacheRequest extracts actions from a token request and caches them locally to avoid redundant parsing during the commit phase.
func (t *Service) CacheRequest(ctx context.Context, request *token.Request) error {
	toSpend, toAppend, err := t.extractActions(ctx, request.Anchor, request)
	if err != nil {
		return errors.WithMessagef(err, "failed to extract actions for request [%s]", request.ID())
	}
	logger.DebugfContext(ctx, "cache request [%s]", request.ID())
	// append to cache
	msgToSign, err := request.MarshalToSign()
	if err != nil {
		return errors.WithMessagef(err, "failed to marshal token request [%s]", request.ID())
	}
	t.RequestsCache.Add(string(request.Anchor), &CacheEntry{
		Request:   request,
		ToSpend:   toSpend,
		ToAppend:  toAppend,
		MsgToSign: msgToSign,
	})

	return nil
}

// GetCachedTokenRequest retrieves a cached token request and its serialized message.
func (t *Service) GetCachedTokenRequest(txID string) (*token.Request, []byte) {
	res, ok := t.RequestsCache.Get(txID)
	if !ok {
		return nil, nil
	}

	return res.Request, res.MsgToSign
}

func (t *Service) removeCachedTokenRequest(txID string) {
	t.RequestsCache.Delete(txID)
}

// StorePublicParams persists the raw byte representation of public parameters in TokenDB.
func (t *Service) StorePublicParams(ctx context.Context, raw []byte) error {
	return t.Storage.StorePublicParams(ctx, raw)
}

// DeleteTokensBy marks the tokens identified by ids as spent in the database, attributed to a specific actor.
func (t *Service) DeleteTokensBy(ctx context.Context, deletedBy string, ids ...*token2.ID) (err error) {
	return t.Storage.TokenDB.DeleteTokens(ctx, deletedBy, ids...)
}

// DeleteTokens marks the tokens as spent in the database, attributed to the caller's stack trace.
func (t *Service) DeleteTokens(ctx context.Context, ids ...*token2.ID) (err error) {
	return t.DeleteTokensBy(ctx, string(debug.Stack()), ids...)
}

// SetSpendableFlag sets the spendable status for the specified tokens.
func (t *Service) SetSpendableFlag(ctx context.Context, value bool, ids ...*token2.ID) error {
	tx, err := t.Storage.NewTransaction()
	if err != nil {
		return errors.Wrapf(err, "failed initiating transaction")
	}
	if err := tx.SetSpendableFlag(ctx, value, ids); err != nil {
		if err2 := tx.Rollback(); err2 != nil {
			logger.Errorf("failed rolling back transaction that set spendable flag [%s]", err2)
		}

		return errors.Wrapf(err, "failed setting spendable flag")
	}

	return tx.Commit()
}

// SetSpendableBySupportedTokenTypes sets the spendable flag for all tokens that match the provided formats.
func (t *Service) SetSpendableBySupportedTokenTypes(ctx context.Context, types []token2.Format) error {
	tx, err := t.Storage.NewTransaction()
	if err != nil {
		return errors.WithMessagef(err, "error creating new transaction")
	}
	if err := tx.SetSpendableBySupportedTokenTypes(ctx, types); err != nil {
		if err2 := tx.Rollback(); err2 != nil {
			logger.Errorf("error rolling back transaction: %v", err2)
		}

		return errors.WithMessagef(err, "error setting supported tokens")
	}
	if err := tx.Commit(); err != nil {
		return errors.WithMessagef(err, "error committing transaction")
	}

	return nil
}

// SetSupportedTokenFormats updates the list of token formats currently supported by the storage.
func (t *Service) SetSupportedTokenFormats(tokenTypes []token2.Format) error {
	return t.Storage.TokenDB.SetSupportedTokenFormats(tokenTypes)
}

// UnsupportedTokensIteratorBy returns an iterator for tokens that are no longer supported,
// typically used during upgrade processes.
func (t *Service) UnsupportedTokensIteratorBy(ctx context.Context, walletID string, typ token2.Type) (driver.UnsupportedTokensIterator, error) {
	return t.Storage.TokenDB.UnsupportedTokensIteratorBy(ctx, walletID, typ)
}

// PruneInvalidUnspentTokens identifies and removes unspent tokens from the local store
// that are no longer available on the ledger.
func (t *Service) PruneInvalidUnspentTokens(ctx context.Context) ([]*token2.ID, error) {
	tmsID := t.Storage.TMSID
	tms, err := t.TMSProvider.GetManagementService(token.WithTMSID(tmsID))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting token management service [%s]", tmsID)
	}
	// network
	tmsID = tms.ID()
	net, err := t.NetworkProvider.GetNetwork(tmsID.Network, tms.Channel())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting network [%s:%s]", tmsID.Network, tmsID.Channel)
	}

	// get unspent tokens
	it, err := tms.Vault().NewQueryEngine().UnspentTokensIterator(ctx)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get an iterator of unspent tokens")
	}
	defer it.Close()

	var deleted []*token2.ID
	var buffer []*token2.UnspentToken
	bufferSize := 50
	for {
		tok, err := it.Next()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to get next unspent token")
		}
		if tok == nil {
			break
		}
		buffer = append(buffer, tok)
		if len(buffer) > bufferSize {
			newDeleted, err := t.deleteTokens(ctx, net, tms, buffer)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to process tokens [%v]", buffer)
			}
			deleted = append(deleted, newDeleted...)
			buffer = nil
		}
	}
	newDeleted, err := t.deleteTokens(ctx, net, tms, buffer)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to process tokens [%v]", buffer)
	}
	deleted = append(deleted, newDeleted...)

	return deleted, nil
}

func (t *Service) deleteTokens(ctx context.Context, network *network.Network, tms *token.ManagementService, tokens []*token2.UnspentToken) ([]*token2.ID, error) {
	logger.DebugfContext(ctx, "delete tokens from vault [%d][%v]", len(tokens), tokens)
	if len(tokens) == 0 {
		return nil, nil
	}

	// get spent flags
	ids := make([]*token2.ID, len(tokens))
	for i, tok := range tokens {
		ids[i] = &tok.Id
	}
	meta, err := tms.WalletManager().SpentIDs(ids)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to compute spent ids for [%v]", ids)
	}
	spent, err := network.AreTokensSpent(ctx, tms.Namespace(), ids, meta)
	if err != nil {
		return nil, errors.WithMessagef(err, "cannot fetch spent flags from network [%s:%s] for ids [%v]", tms.Network(), tms.Channel(), ids)
	}

	// remove the tokens flagged as spent
	var toDelete []*token2.ID
	for i, tok := range tokens {
		if spent[i] {
			logger.DebugfContext(ctx, "token [%s] is spent", tok.Id)
			toDelete = append(toDelete, &tok.Id)
		} else {
			logger.DebugfContext(ctx, "token [%s] is not spent", tok.Id)
		}
	}
	if err := t.DeleteTokens(ctx, toDelete...); err != nil {
		return nil, errors.WithMessagef(err, "failed to remove token ids [%v]", toDelete)
	}

	return toDelete, nil
}

func (t *Service) getActions(ctx context.Context, anchor token.RequestAnchor, request *token.Request) ([]*token2.ID, []TokenToAppend, error) {
	// check the cache first
	logger.DebugfContext(ctx, "check request cache for [%s]", anchor)
	entry, ok := t.RequestsCache.Get(string(anchor))
	if ok && entry != nil {
		logger.DebugfContext(ctx, "cache hit, return it")

		return entry.ToSpend, entry.ToAppend, nil
	}
	// extract
	return t.extractActions(ctx, anchor, request)
}

func (t *Service) extractActions(ctx context.Context, anchor token.RequestAnchor, request *token.Request) ([]*token2.ID, []TokenToAppend, error) {
	tms, err := t.TMSProvider.GetManagementService(token.WithTMSID(t.tmsID))
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed getting token management service [%s]", t.tmsID)
	}

	logger.DebugfContext(ctx, "transaction [%s on (%s)] is known, extract tokens", anchor, tms.ID())
	pp := tms.PublicParametersManager().PublicParameters()
	graphHiding := pp.GraphHiding()
	precision := pp.Precision()
	auth := tms.Authorization()
	auditorFlag := auth.AmIAnAuditor()
	if auditorFlag {
		logger.DebugfContext(ctx, "transaction [%s], I must be the auditor", anchor)
	}
	md, err := request.GetMetadata()
	if err != nil {
		logger.DebugfContext(ctx, "transaction [%s], failed to get metadata [%s]", anchor, err)

		return nil, nil, errors.WithMessagef(err, "transaction [%s], failed to get request metadata", anchor)
	}

	is, os, err := request.InputsAndOutputsNoRecipients(ctx)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "failed to get request's outputs")
	}
	toSpend, toAppend, err := t.Parse(ctx, auth, anchor, md, is, os, auditorFlag, precision, graphHiding)
	logger.DebugfContext(ctx, "transaction [%s] parsed [%d] inputs and [%d] outputs", anchor, len(toSpend), len(toAppend))

	return toSpend, toAppend, err
}

// Parse returns the tokens to store and spend as the result of a transaction
func (t *Service) Parse(
	ctx context.Context,
	auth driver.Authorization,
	requestAnchor token.RequestAnchor,
	md MetaData,
	is *token.InputStream,
	os *token.OutputStream,
	auditorFlag bool,
	precision uint64,
	graphHiding bool,
) (toSpend []*token2.ID, toAppend []TokenToAppend, err error) {
	if graphHiding {
		ids := md.SpentTokenID()
		logger.DebugfContext(ctx, "transaction [%s] with graph hiding, delete inputs [%v]", requestAnchor, ids)
		toSpend = append(toSpend, ids...)
	}

	logger.DebugfContext(ctx, "parse [%d] inputs and [%d] outputs from [%s]", is.Count(), os.Count(), requestAnchor)

	// parse the inputs
	for _, input := range is.Inputs() {
		if input.Id == nil {
			logger.DebugfContext(ctx, "transaction [%s] found an input that is not mine, skip it", requestAnchor)

			continue
		}
		logger.DebugfContext(ctx, "transaction [%s] delete input [%s]", requestAnchor, input.Id)
		toSpend = append(toSpend, input.Id)
	}

	// parse the outputs
	for _, output := range os.Outputs() {
		// if this is a redeem (empty owner), store it only if it was redeemed against
		// an issuer known to this node; otherwise skip it.
		if len(output.Token.Owner) == 0 {
			logger.DebugfContext(ctx, "output [%s:%d] is a redeem", requestAnchor, output.Index)

			issuer := output.Issuer
			redeemedMine := !issuer.IsNone() && auth.Issued(ctx, issuer, &output.Token)
			if !redeemedMine {
				logger.DebugfContext(ctx, "transaction [%s], discarding redeem, issuer is not mine", requestAnchor)

				continue
			}

			tta := TokenToAppend{
				TxID:                  string(requestAnchor),
				Index:                 output.Index,
				Tok:                   &output.Token,
				TokenOnLedger:         output.LedgerOutput,
				TokenOnLedgerFormat:   output.LedgerOutputFormat,
				TokenOnLedgerMetadata: output.LedgerOutputMetadata,
				OwnerType:             "",
				OwnerIdentity:         []byte{},
				OwnerWalletID:         "",
				Owners:                nil,
				Issuer:                issuer,
				Precision:             precision,
				Flags: Flags{
					Mine:     false,
					Auditor:  auditorFlag,
					Issuer:   true,
					Redeemed: true,
				},
			}
			toAppend = append(toAppend, tta)

			continue
		}

		// process the output to identify the relations with the current TMS
		issuerFlag := !output.Issuer.IsNone() && auth.Issued(ctx, output.Issuer, &output.Token)
		ownerWalletID, ids, mine := auth.IsMine(ctx, &output.Token)
		if logger.IsEnabledFor(zapcore.DebugLevel) {
			if mine {
				logger.DebugfContext(ctx, "transaction [%s], found a token and it is mine with [%s][%v]", requestAnchor, ownerWalletID, ids)
			} else {
				logger.DebugfContext(ctx, "transaction [%s], found a token and it is NOT mine", requestAnchor)
			}
			if issuerFlag {
				logger.DebugfContext(ctx, "transaction [%s], found a token and I have issued it", requestAnchor)
			}
			logger.DebugfContext(ctx, "store token [%s:%d][%s]", requestAnchor, output.Index, utils.Hashable(output.LedgerOutput))
		}
		if !mine && !auditorFlag && !issuerFlag {
			logger.DebugfContext(ctx, "transaction [%s], discarding token, not mine, not an auditor, not an issuer", requestAnchor)

			continue
		}

		ownerType, ownerIdentity, err := auth.OwnerType(output.Token.Owner)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to extract owner type for token [%s:%d]", requestAnchor, output.Index)
		}

		tta := TokenToAppend{
			TxID:                  string(requestAnchor),
			Index:                 output.Index,
			Tok:                   &output.Token,
			TokenOnLedger:         output.LedgerOutput,
			TokenOnLedgerFormat:   output.LedgerOutputFormat,
			TokenOnLedgerMetadata: output.LedgerOutputMetadata,
			OwnerType:             identity.TypeToString(ownerType),
			OwnerIdentity:         ownerIdentity,
			OwnerWalletID:         ownerWalletID,
			Owners:                ids,
			Issuer:                output.Issuer,
			Precision:             precision,
			Flags: Flags{
				Mine:    mine,
				Auditor: auditorFlag,
				Issuer:  issuerFlag,
			},
		}
		toAppend = append(toAppend, tta)

		if logger.IsEnabledFor(zapcore.DebugLevel) {
			logger.DebugfContext(ctx, "done parsing write key [%s]", output.ID(requestAnchor))
		}
	}

	return toSpend, toAppend, err
}
