/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	TokenNamespace = "tns"
)

// Payload contains the core data of a token transaction including the transaction ID,
// signer identity, token request, and network envelope. It represents the serializable
// portion of a transaction that can be transmitted over the network.
type Payload struct {
	TxID      network.TxID
	ID        string
	tmsID     token.TMSID
	Signer    view.Identity
	Transient network.TransientMap

	TokenRequest *token.Request
	Envelope     *network.Envelope
}

// Transaction models a token transaction
type Transaction struct {
	*Payload

	TMS              dep.TokenManagementServiceWithExtensions
	NetworkProvider  GetNetworkFunc
	Opts             *TxOptions
	Context          context.Context
	EndpointResolver *endpoint.Service
	// FromRaw contains the raw material used to unmarshall this transaction.
	// It is nil if the transaction was created from scratch.
	FromRaw              []byte
	FromSignatureRequest *SignatureRequest
}

// NewAnonymousTransaction returns a new Transaction whose envelope will be signed by an anonymous identities.
// Options can be further used to customize the transaction.
func NewAnonymousTransaction(context view.Context, opts ...TxOption) (*Transaction, error) {
	txOpts, err := CompileOpts(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed compiling tx options")
	}
	tms, err := token.GetManagementService(
		context,
		token.WithTMSID(txOpts.TMSID),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}
	net := network.GetInstance(context, tms.Network(), tms.Channel())
	if net == nil {
		return nil, errors.New("failed to get network")
	}
	id, err := net.AnonymousIdentity()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting anonymous identity for transaction")
	}

	return NewTransaction(context, id, opts...)
}

// NewTransaction returns a new token transaction whose envelope will be signed by the signer bound to the given identity..
// Options can be further used to customize the transaction.
// The given identity must be recognizable by the target network.
// For example, in case of fabric, the signer must be a valid fabric identity.
// If the passed signer is nil, then the default identity is used.
func NewTransaction(context view.Context, signer view.Identity, opts ...TxOption) (*Transaction, error) {
	txOpts, err := CompileOpts(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed compiling tx options")
	}

	tms, err := dep.GetManagementService(
		context,
		token.WithTMSID(txOpts.TMSID),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}

	networkProvider, err := dep.GetNetworkProvider(context)
	if err != nil {
		return nil, errors.Join(err, ErrDepNotAvailableInContext)
	}

	networkService, err := networkProvider.GetNetwork(tms.Network(), tms.Channel())
	if err != nil {
		return nil, errors.New("failed to get network")
	}

	if txOpts.AnonymousTransaction && signer == nil {
		// set the signer to anonymous
		id, err := networkService.AnonymousIdentity()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed getting anonymous identity for transaction")
		}
		signer = id
	}

	var txID network.TxID
	if len(txOpts.NetworkTxID.Creator) != 0 {
		txID = txOpts.NetworkTxID
		signer = txID.Creator
	} else {
		if signer.IsNone() {
			signer = networkService.LocalMembership().DefaultIdentity()
		}
		txID = network.TxID{Creator: signer}
	}
	id := networkService.ComputeTxID(&txID)
	tr, err := tms.NewRequest(token.RequestAnchor(id))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed init token request")
	}

	tx := &Transaction{
		Payload: &Payload{
			Signer:       signer,
			TokenRequest: tr,
			Envelope:     nil,
			TxID:         txID,
			ID:           id,
			tmsID:        tms.ID(),
			Transient:    map[string][]byte{},
		},
		TMS:              tms,
		NetworkProvider:  networkProvider.GetNetwork,
		Opts:             txOpts,
		Context:          context.Context(),
		EndpointResolver: endpoint.GetService(context),
	}
	context.OnError(tx.Release)

	return tx, nil
}

// NewTransactionFromBytes unmarshals the given bytes into a Transaction, if possible.
func NewTransactionFromBytes(context view.Context, raw []byte) (*Transaction, error) {
	provider, err := dep.GetNetworkProvider(context)
	if err != nil {
		return nil, errors.Join(err, ErrDepNotAvailableInContext)
	}
	networkProvider := provider.GetNetwork
	payload := &Payload{
		Transient:    map[string][]byte{},
		TokenRequest: token.NewRequest(nil, ""),
	}
	if err := unmarshal(networkProvider, payload, raw); err != nil {
		return nil, errors.Join(err, ErrTxUnmarshalling)
	}
	logger.DebugfContext(context.Context(), "unmarshalling tx, id [%s]", payload.TxID)
	// check there exists a tms for this payload
	tms, err := dep.GetManagementService(context, token.WithTMSID(payload.tmsID))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}
	if !tms.ID().Equal(payload.tmsID) {
		return nil, errors.Errorf("failed to find tms for tmsID [%s], got [%s]", payload.tmsID, tms.ID())
	}
	// check transaction id
	if payload.ID != string(payload.TokenRequest.ID()) {
		return nil, errors.Errorf("invalid transaction, transaction ids do not match [%s][%s]", payload.ID, payload.TokenRequest.ID())
	}

	// finalize
	if err := tms.SetTokenManagementService(payload.TokenRequest); err != nil {
		return nil, errors.WithMessagef(err, "failed to set token management service")
	}
	tx := &Transaction{
		Payload:         payload,
		TMS:             tms,
		NetworkProvider: networkProvider,
		Context:         context.Context(),
		FromRaw:         raw,
	}
	context.OnError(tx.Release)

	return tx, nil
}

// NewTransactionFromSignatureRequest calls NewTransactionFromBytes with the content of the signature request.
// It sets the transaction's `FromSignatureRequest` upon a success in the deserialization.
func NewTransactionFromSignatureRequest(context view.Context, sr *SignatureRequest) (*Transaction, error) {
	tx, err := NewTransactionFromBytes(context, sr.TX)
	if err != nil {
		return nil, err
	}
	tx.FromSignatureRequest = sr

	return tx, nil
}

// ReceiveTransaction reads from the context's session a message and tries to unmarshal the message payload as a Transaction.
func ReceiveTransaction(context view.Context, opts ...TxOption) (*Transaction, error) {
	opt, err := CompileOpts(opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to parse options")
	}
	logger.DebugfContext(context.Context(), "receive a new transaction...")

	txBoxed, err := context.RunView(NewReceiveTransactionView(opts...), view.WithSameContext())
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to receive transaction")
	}

	cctx, ok := txBoxed.(*Transaction)
	if !ok {
		return nil, errors.Errorf("received transaction of wrong type [%T]", cctx)
	}
	logger.DebugfContext(context.Context(), "received transaction with id [%s]", cctx.ID())
	if !opt.NoTransactionVerification {
		// Check that the transaction is valid
		if err := cctx.IsValid(context.Context()); err != nil {
			return nil, errors.WithMessagef(err, "invalid transaction %s", cctx.ID())
		}
	}

	return cctx, nil
}

// ID returns the ID of this transaction. It is equal to the underlying transaction's ID.
func (t *Transaction) ID() string {
	return t.Payload.ID
}

// Network returns the network ID of this transaction.
func (t *Transaction) Network() string {
	return t.tmsID.Network
}

// Channel returns the channel ID of this transaction.
func (t *Transaction) Channel() string {
	return t.tmsID.Channel
}

// Namespace returns the namespace ID of this transaction.
func (t *Transaction) Namespace() string {
	return t.tmsID.Namespace
}

// Request returns the underlying TokenRequest of this transaction.
func (t *Transaction) Request() *token.Request {
	return t.TokenRequest
}

// NetworkTxID returns the network transaction ID of this transaction.
// The network transaction ID is the identifier the underlying network understands.
func (t *Transaction) NetworkTxID() network.TxID {
	return t.TxID
}

// Bytes returns the serialized version of the transaction.
// If eIDs is not nil, then metadata is filtered by the passed eIDs.
func (t *Transaction) Bytes(eIDs ...string) ([]byte, error) {
	logger.Debugf("marshalling tx, id [%s], for EIDs [%x]", t.TxID, eIDs)

	return marshal(t, eIDs...)
}

// Issue appends a new Issue action to the TokenRequest of this transaction
func (t *Transaction) Issue(wallet *token.IssuerWallet, receiver view.Identity, typ token2.Type, q uint64, opts ...token.IssueOption) error {
	_, err := t.TokenRequest.Issue(t.Context, wallet, receiver, typ, q, opts...)

	return err
}

// Transfer appends a new Transfer action to the TokenRequest of this transaction
func (t *Transaction) Transfer(wallet *token.OwnerWallet, typ token2.Type, values []uint64, owners []view.Identity, opts ...token.TransferOption) error {
	_, err := t.TokenRequest.Transfer(t.Context, wallet, typ, values, owners, opts...)

	return err
}

// Redeem appends a new Redeem action to the TokenRequest of this transaction
func (t *Transaction) Redeem(wallet *token.OwnerWallet, typ token2.Type, value uint64, opts ...token.TransferOption) error {
	// build the redeem action
	action, err := t.TokenRequest.Redeem(t.Context, wallet, typ, value, opts...)
	if err != nil {
		return err
	}

	// check if the opts contain the issuer's network identity,
	// if yes, then bind it to the issuer identity in the redeem action

	// compile the options
	options, err := token.CompileTransferOptions(opts...)
	if err != nil {
		return errors.Wrap(err, "failed to compile transfer options")
	}
	issuerNetworkIdentity, err := GetFSCIssuerIdentityFromOpts(options.Attributes)
	if err != nil {
		return errors.Wrap(err, "failed to get issuer identity")
	}
	if !issuerNetworkIdentity.IsNone() {
		if err := t.EndpointResolver.Bind(t.Context, issuerNetworkIdentity, action.GetIssuer()); err != nil {
			return errors.Wrapf(err, "failed to bind issuer identity [%s]", action.GetIssuer())
		}
	}

	return nil
}

// Upgrade performs an upgrade operation of the passed ledger tokens.
// A proof and its challenge will be used to verify that the request of upgrade is legit.
// If the proof verifies then the passed wallet will be used to issue a new amount of tokens
// matching those whose upgrade has been requested.
func (t *Transaction) Upgrade(
	wallet *token.IssuerWallet,
	receiver token.Identity,
	challenge token.TokensUpgradeChallenge,
	tokens []token2.LedgerToken,
	proof token.TokensUpgradeProof,
	opts ...token.IssueOption,
) error {
	_, err := t.TokenRequest.Upgrade(t.Context, wallet, receiver, challenge, tokens, proof, opts...)

	return err
}

// Outputs returns the outputs of this transaction over all the actions.
// The output stream returned can by further filter via the methods it exposes.
func (t *Transaction) Outputs() (*token.OutputStream, error) {
	return t.TokenRequest.Outputs(t.Context)
}

// Inputs returns the inputs of this transaction over all the actions.
// The input stream returned can by further filter via the methods it exposes.
func (t *Transaction) Inputs() (*token.InputStream, error) {
	return t.TokenRequest.Inputs(t.Context)
}

// InputsAndOutputs returns the inputs and outputs of this transaction over all the actions.
// The input and output streams returned can by further filter via the methods they expose.
// The map returned contains the application metadata for all the involved tokens.
func (t *Transaction) InputsAndOutputs(ctx context.Context) (*token.InputStream, *token.OutputStream, token.ActionMetadata, error) {
	return t.TokenRequest.InputsAndOutputs(ctx)
}

// IsValid checks that the transaction is well-formed.
// This means checking that the embedded TokenRequest is valid.
func (t *Transaction) IsValid(ctx context.Context) error {
	if t.TokenRequest == nil {
		return errors.New("invalid transaction: nil token request")
	}

	return t.TokenRequest.IsValid(ctx)
}

// MarshallToAudit returns the marshalled version of this transaction for audit purposes.
func (t *Transaction) MarshallToAudit() ([]byte, error) {
	return t.TokenRequest.MarshalToAudit()
}

// Selector returns the default token selector for this transaction
func (t *Transaction) Selector() (token.Selector, error) {
	sm, err := t.TokenService().SelectorManager()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get selector manager")
	}

	return sm.NewSelector(t.ID())
}

// CloseSelector closes the token selector for this transaction.
func (t *Transaction) CloseSelector() error {
	sm, err := t.TokenService().SelectorManager()
	if err != nil {
		return errors.WithMessagef(err, "failed to get selector manager")
	}

	return sm.Close(t.ID())
}

// Release releases all the resources held by this transaction.
// In particular, all the tokens locked by this transaction are unlocked.
func (t *Transaction) Release() {
	logger.Debugf("releasing resources for tx [%s]", t.ID())
	sm, err := t.TokenService().SelectorManager()
	if err != nil {
		logger.Warnf("failed to get token selector [%s]", err)
	} else {
		// we need to unlock even if t.Context is canceled
		if err := sm.Unlock(context.Background(), t.ID()); err != nil {
			logger.Warnf("failed releasing tokens locked by [%s], [%s]", t.ID(), err)
		}
	}
}

// TokenService returns the token management service associated to this transaction.
func (t *Transaction) TokenService() dep.TokenManagementServiceWithExtensions {
	return t.TMS
}

// ApplicationMetadata returns the application metadata value for the given key.
func (t *Transaction) ApplicationMetadata(k string) []byte {
	return t.TokenRequest.ApplicationMetadata(k)
}

// SetApplicationMetadata sets the application metadata key-value pair.
func (t *Transaction) SetApplicationMetadata(k string, v []byte) {
	t.TokenRequest.SetApplicationMetadata(k, v)
}

// TMSID returns the TMSID of this transaction.
func (t *Transaction) TMSID() token.TMSID {
	return t.tmsID
}

// appendPayload merges the given payload into this transaction by copying its token request
// and transient data. This is used internally for transaction composition.
// TODO: This implementation is incomplete and needs to be enhanced to properly merge all payload components.
func (t *Transaction) appendPayload(payload *Payload) error {
	// TODO: change this
	t.TokenRequest = payload.TokenRequest
	t.Transient = payload.Transient

	return nil

	// for _, bytes := range payload.Request.Issues {
	//	t.Payload.Request.Issues = append(t.Payload.Request.Issues, bytes)
	// }
	// for _, bytes := range payload.Request.Transfers {
	//	t.Payload.Request.Transfers = append(t.Payload.Request.Transfers, bytes)
	// }
	// for _, info := range payload.TokenInfos {
	//	t.Payload.TokenInfos = append(t.Payload.TokenInfos, info)
	// }
	// for _, issueMetadata := range payload.ValidationRecords.Issues {
	//	t.Payload.ValidationRecords.Issues = append(t.Payload.ValidationRecords.Issues, issueMetadata)
	// }
	// for _, transferMetadata := range payload.ValidationRecords.Transfers {
	//	t.Payload.ValidationRecords.Transfers = append(t.Payload.ValidationRecords.Transfers, transferMetadata)
	// }
	//
	// for key, value := range payload.Transient {
	//	for _, v := range value {
	//		if err := t.Set(key, v); err != nil {
	//			return err
	//		}
	//	}
	// }
	// return nil
}
