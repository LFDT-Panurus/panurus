/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"context"
	"crypto"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/endpoint"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	ScriptType            = driver.HTLCScriptIdentityType
	ScriptTypeString      = driver.HTLCScriptIdentityTypeString // htlc script
	defaultDeadlineOffset = time.Hour
)

// WithHash sets a hash attribute to be used to customize the transfer command
func WithHash(hash []byte) token.TransferOption {
	return func(o *token.TransferOptions) error {
		if o.Attributes == nil {
			o.Attributes = map[string]any{}
		}
		o.Attributes["htlc.hash"] = hash

		return nil
	}
}

// WithHashFunc sets a hash function attribute to be used to customize the transfer command
func WithHashFunc(hashFunc crypto.Hash) token.TransferOption {
	return func(o *token.TransferOptions) error {
		if o.Attributes == nil {
			o.Attributes = map[string]any{}
		}
		o.Attributes["htlc.hashFunc"] = hashFunc

		return nil
	}
}

// WithHashEncoding sets a hash encoding attribute to be used to customize the transfer command
func WithHashEncoding(encoding encoding.Encoding) token.TransferOption {
	return func(o *token.TransferOptions) error {
		if o.Attributes == nil {
			o.Attributes = map[string]any{}
		}
		o.Attributes["htlc.hashEncoding"] = encoding

		return nil
	}
}

func compileTransferOptions(opts ...token.TransferOption) (*token.TransferOptions, error) {
	txOptions := &token.TransferOptions{}
	for _, opt := range opts {
		if err := opt(txOptions); err != nil {
			return nil, err
		}
	}

	return txOptions, nil
}

type Binder interface {
	Bind(ctx context.Context, longTerm view.Identity, ephemeral ...view.Identity) error
}

// Transaction holds a ttx transaction
type Transaction struct {
	*ttx.Transaction
	Binder Binder
}

// NewTransaction returns a new token transaction customized with the passed opts that will be signed by the passed signer
func NewTransaction(sp view.Context, signer view.Identity, opts ...ttx.TxOption) (*Transaction, error) {
	tx, err := ttx.NewTransaction(sp, signer, opts...)
	if err != nil {
		return nil, err
	}

	return &Transaction{
		Transaction: tx,
		Binder:      endpoint.GetService(sp),
	}, nil
}

// NewAnonymousTransaction returns a new anonymous token transaction customized with the passed opts
func NewAnonymousTransaction(sp view.Context, opts ...ttx.TxOption) (*Transaction, error) {
	tx, err := ttx.NewAnonymousTransaction(sp, opts...)
	if err != nil {
		return nil, err
	}

	return &Transaction{
		Transaction: tx,
		Binder:      endpoint.GetService(sp),
	}, nil
}

// NewTransactionFromBytes returns a new transaction from the passed bytes
func NewTransactionFromBytes(ctx view.Context, network, channel string, raw []byte) (*Transaction, error) {
	tx, err := ttx.NewTransactionFromBytes(ctx, raw)
	if err != nil {
		return nil, err
	}

	return &Transaction{
		Transaction: tx,
		Binder:      endpoint.GetService(ctx),
	}, nil
}

// Outputs returns a new OutputStream of the transaction's outputs
func (t *Transaction) Outputs() (*OutputStream, error) {
	outs, err := t.TokenRequest.Outputs(t.Context)
	if err != nil {
		return nil, err
	}

	return NewOutputStream(outs), nil
}

// Lock appends a lock action to the token request of the transaction
func (t *Transaction) Lock(ctx context.Context, wallet *token.OwnerWallet, sender view.Identity, typ token2.Type, value uint64, recipient view.Identity, deadline time.Duration, opts ...token.TransferOption) ([]byte, error) {
	options, err := compileTransferOptions(opts...)
	if err != nil {
		return nil, err
	}
	if deadline == 0 {
		deadline = defaultDeadlineOffset
	}
	if recipient.IsNone() {
		return nil, errors.Errorf("must specify a recipient")
	}

	if sender == nil {
		sender, err = wallet.GetRecipientIdentity(ctx)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed getting sender identity")
		}
	}

	var hash []byte
	hashFunc := crypto.SHA256 // default hash function
	var hashEncoding encoding.Encoding
	if options.Attributes != nil {
		boxed, ok := options.Attributes["htlc.hash"]
		if ok {
			hash, ok = boxed.([]byte)
			if !ok {
				return nil, errors.Errorf("expected htlc.hash attribute to be []byte, got [%T]", boxed)
			}
		}
		boxed, ok = options.Attributes["htlc.hashFunc"]
		if ok {
			hashFunc, ok = boxed.(crypto.Hash)
			if !ok {
				return nil, errors.Errorf("expected htlc.hashFunc attribute to be crypto.Hash, got [%T]", boxed)
			}
			if hashFunc == 0 {
				hashFunc = crypto.SHA256 // default hash function
			}
		}
		boxed, ok = options.Attributes["htlc.hashEncoding"]
		if ok {
			hashEncoding, ok = boxed.(encoding.Encoding)
			if !ok {
				return nil, errors.Errorf("expected htlc.hashEncoding attribute to be Encoding, got [%T]", boxed)
			}
		}
	}
	scriptID, preImage, script, err := t.recipientAsScript(sender, recipient, deadline, hash, hashFunc, hashEncoding)
	if err != nil {
		return nil, err
	}
	_, err = t.TokenRequest.Transfer(
		t.Context,
		wallet,
		typ,
		[]uint64{value},
		[]view.Identity{scriptID},
		append(opts, token.WithTransferMetadata(LockKey(script.HashInfo.Hash), LockValue(script.HashInfo.Hash)))...,
	)
	if err != nil {
		return nil, err
	}

	return preImage, nil
}

// Reclaim appends a reclaim (transfer) action to the token request of the transaction
func (t *Transaction) Reclaim(wallet *token.OwnerWallet, tok *token2.UnspentToken, opts ...token.TransferOption) error {
	q, err := token2.ToQuantity(tok.Quantity, t.TokenRequest.TokenService.PublicParametersManager().PublicParameters().Precision())
	if err != nil {
		return errors.Wrapf(err, "failed to convert quantity [%s]", tok.Quantity)
	}
	owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
	if err != nil {
		return err
	}
	if owner.Type != ScriptType {
		return errors.Errorf("invalid owner type, expected htlc script")
	}
	script := &Script{}
	err = json.Unmarshal(owner.Identity, script)
	if err != nil {
		return errors.Errorf("failed to unmarshal TypedIdentity as an htlc script")
	}

	// Register the signer for the reclaim
	sigService := t.TokenService().SigService()
	signer, err := sigService.GetSigner(t.Context, script.Sender)
	if err != nil {
		return err
	}
	verifier, err := sigService.OwnerVerifier(t.Context, script.Sender)
	if err != nil {
		return err
	}
	logger.Debugf("registering signer for reclaim for identity [%s] with sender [%s]", token.Identity(tok.Owner), script.Sender)
	if err := sigService.RegisterEphemeralSigner(
		t.Context,
		tok.Owner,
		signer,
		verifier,
	); err != nil {
		return err
	}

	if err := t.Binder.Bind(t.Context, script.Sender, tok.Owner); err != nil {
		return err
	}

	return t.Transfer(
		wallet,
		tok.Type,
		[]uint64{q.ToBigInt().Uint64()},
		[]view.Identity{script.Sender},
		append(opts, token.WithTokenIDs(&tok.Id))...,
	)
}

// Claim appends a claim (transfer) action to the token request of the transaction
func (t *Transaction) Claim(wallet *token.OwnerWallet, tok *token2.UnspentToken, preImage []byte, opts ...token.TransferOption) error {
	if len(preImage) == 0 {
		return errors.New("preImage is nil")
	}

	q, err := token2.ToQuantity(tok.Quantity, t.TokenRequest.TokenService.PublicParametersManager().PublicParameters().Precision())
	if err != nil {
		return errors.Wrapf(err, "failed to convert quantity [%s]", tok.Quantity)
	}

	owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
	if err != nil {
		return err
	}
	if owner.Type != ScriptType {
		return errors.New("invalid owner type, expected htlc script")
	}
	script := &Script{}
	if err := json.Unmarshal(owner.Identity, script); err != nil {
		return errors.New("failed to unmarshal TypedIdentity as an htlc script")
	}

	image, err := script.HashInfo.Image(preImage)
	if err != nil {
		return errors.Wrapf(err, "failed to compute image of [%x]", preImage)
	}

	if err := script.HashInfo.Compare(image); err != nil {
		return errors.Wrap(err, "passed preImage does not match the hash in the passed script")
	}

	// Register the signer for the claim
	logger.Debugf("registering signer for claim...")
	sigService := t.TokenService().SigService()
	recipientSigner, err := sigService.GetSigner(t.Context, script.Recipient)
	if err != nil {
		return err
	}
	recipientVerifier, err := sigService.OwnerVerifier(t.Context, script.Recipient)
	if err != nil {
		return err
	}
	if err := sigService.RegisterEphemeralSigner(
		t.Context,
		tok.Owner,
		&ClaimSigner{
			Recipient: recipientSigner,
			Preimage:  preImage,
		},
		&ClaimVerifier{
			Recipient: recipientVerifier,
			HashInfo: HashInfo{
				Hash:         script.HashInfo.Hash,
				HashFunc:     script.HashInfo.HashFunc,
				HashEncoding: script.HashInfo.HashEncoding,
			},
		},
	); err != nil {
		return err
	}

	if err := t.Binder.Bind(t.Context, script.Recipient, tok.Owner); err != nil {
		return err
	}

	return t.Transfer(
		wallet,
		tok.Type,
		[]uint64{q.ToBigInt().Uint64()},
		[]view.Identity{script.Recipient},
		append(opts, token.WithTokenIDs(&tok.Id), token.WithTransferMetadata(ClaimKey(image), preImage))...,
	)
}

func (t *Transaction) recipientAsScript(sender, recipient view.Identity, deadline time.Duration, h []byte, hashFunc crypto.Hash, hashEncoding encoding.Encoding) (view.Identity, []byte, *Script, error) {
	// sample pre-image and its hash
	var preImage []byte
	var err error
	if len(h) == 0 {
		preImage, err = CreateNonce()
		if err != nil {
			return nil, nil, nil, err
		}
		hash := hashFunc.New()
		if _, err := hash.Write(preImage); err != nil {
			return nil, nil, nil, err
		}
		h = hash.Sum(nil)
		// no need to encode if encoding is none (=0)
		if hashEncoding != 0 {
			he := hashEncoding.New()
			if he == nil {
				return nil, nil, nil, errors.New("hashEncoding.New() returned nil")
			}
			h = []byte(he.EncodeToString(h))
		}
	}

	logger.Debugf("pair (pre-image, hash) = (%s,%s)", base64.StdEncoding.EncodeToString(preImage), base64.StdEncoding.EncodeToString(h))

	script := &Script{
		HashInfo: HashInfo{
			Hash:         h,
			HashFunc:     hashFunc,
			HashEncoding: hashEncoding,
		},
		Deadline:  time.Now().Add(deadline),
		Recipient: recipient,
		Sender:    sender,
	}
	rawScript, err := json.Marshal(script)
	if err != nil {
		return nil, nil, nil, err
	}
	ro := &identity.TypedIdentity{
		Type:     ScriptType,
		Identity: rawScript,
	}
	raw, err := ro.Bytes()
	if err != nil {
		return nil, nil, nil, err
	}

	return raw, preImage, script, nil
}

// CreateNonce generates a nonce using the common/crypto package
func CreateNonce() ([]byte, error) {
	nonce, err := getRandomNonce()

	return nonce, errors.WithMessagef(err, "error generating random nonce")
}

func getRandomNonce() ([]byte, error) {
	key := make([]byte, 24)
	_, err := rand.Read(key)
	if err != nil {
		return nil, errors.Wrap(err, "error getting random bytes")
	}

	return key, nil
}
