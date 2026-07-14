/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	token3 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// HashInfo contains the information regarding the hashing
type HashInfo struct {
	Hash         []byte
	HashFunc     crypto.Hash
	HashEncoding encoding.Encoding
}

// Validate checks that the hash and encoding functions are available
func (i *HashInfo) Validate() error {
	if !i.HashFunc.Available() {
		return errors.New("hash function not available")
	}
	if !i.HashEncoding.Available() {
		return errors.New("encoding function not available")
	}

	return nil
}

// Image computes the image of the passed pre-image using the hash and encoding function of this struct
func (i *HashInfo) Image(preImage []byte) ([]byte, error) {
	if err := i.Validate(); err != nil {
		return nil, errors.WithMessagef(err, "hash info not valid")
	}
	hash := i.HashFunc.New()
	if _, err := hash.Write(preImage); err != nil {
		return nil, errors.Wrapf(err, "failed to compute hash image")
	}
	image := hash.Sum(nil)
	image = []byte(i.HashEncoding.New().EncodeToString(image))

	return image, nil
}

// Compare compares the passed image with the hash contained in this struct
func (i *HashInfo) Compare(image []byte) error {
	if bytes.Equal(image, i.Hash) {
		return nil
	}

	return errors.Errorf("passed image [%v] does not match the hash [%v]", image, i.Hash)
}

// Script contains the details of an htlc
type Script struct {
	Sender    view.Identity
	Recipient view.Identity
	Deadline  time.Time
	HashInfo  HashInfo
}

// Validate performs the following checks:
// - The sender must be set
// - The recipient must be set
// - The deadline must be after the passed time reference
// - HashInfo must be Available
func (s *Script) Validate(timeReference time.Time) error {
	if s.Sender.IsNone() {
		return errors.New("sender not set")
	}
	if s.Recipient.IsNone() {
		return errors.New("recipient not set")
	}
	if s.Deadline.Before(timeReference) {
		return errors.New("expiration date has already passed")
	}
	if err := s.HashInfo.Validate(); err != nil {
		return err
	}

	return nil
}

func (s *Script) FromBytes(raw []byte) error {
	return json.Unmarshal(raw, s)
}

// ScriptAuth implements the Authorization interface for this script
type ScriptAuth struct {
	WalletService driver.WalletService
}

func NewScriptAuth(walletService driver.WalletService) *ScriptAuth {
	return &ScriptAuth{WalletService: walletService}
}

// AmIAnAuditor returns false for script ownership
func (s *ScriptAuth) AmIAnAuditor() bool {
	return false
}

// IsMine returns true if either the sender or the recipient is in one of the owner wallets.
// It returns an empty wallet id.
func (s *ScriptAuth) IsMine(ctx context.Context, tok *token3.Token) (string, []string, bool) {
	owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
	if err != nil {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, failed unmarshalling [%s]", view.Identity(tok.Owner), tok.Type, tok.Quantity, err)

		return "", nil, false
	}
	if owner.Type != ScriptType {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, owner type is [%s] instead of [%s]", view.Identity(tok.Owner), tok.Type, tok.Quantity, owner.Type, ScriptType)

		return "", nil, false
	}
	script := &Script{}
	if err := json.Unmarshal(owner.Identity, script); err != nil {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, failed unmarshalling [%s]", view.Identity(tok.Owner), tok.Type, tok.Quantity, err)

		return "", nil, false
	}
	if script.Sender.IsNone() || script.Recipient.IsNone() {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? No, invalid content [%v]", view.Identity(tok.Owner), tok.Type, tok.Quantity, script)

		return "", nil, false
	}

	var ids []string
	// I'm either the sender
	logger.DebugfContext(ctx, "Is Mine [%s,%s,%s] as a sender?", view.Identity(tok.Owner), tok.Type, tok.Quantity)
	if wallet, err := s.WalletService.OwnerWallet(ctx, script.Sender); err == nil {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s] as a sender? Yes", view.Identity(tok.Owner), tok.Type, tok.Quantity)
		ids = append(ids, senderWallet(ctx, wallet))
	}

	// or the recipient
	logger.DebugfContext(ctx, "Is Mine [%s,%s,%s] as a recipient?", view.Identity(tok.Owner), tok.Type, tok.Quantity)
	if wallet, err := s.WalletService.OwnerWallet(ctx, script.Recipient); err == nil {
		logger.DebugfContext(ctx, "Is Mine [%s,%s,%s] as a recipient? Yes", view.Identity(tok.Owner), tok.Type, tok.Quantity)
		ids = append(ids, recipientWallet(ctx, wallet))
	}

	logger.DebugfContext(ctx, "Is Mine [%s,%s,%s]? %b", len(ids) != 0, view.Identity(tok.Owner), tok.Type, tok.Quantity)

	return "", ids, len(ids) != 0
}

func (s *ScriptAuth) Issued(ctx context.Context, issuer driver.Identity, tok *token3.Token) bool {
	return false
}

func (s *ScriptAuth) OwnerType(raw []byte) (driver.IdentityType, []byte, error) {
	owner, err := identity.UnmarshalTypedIdentity(raw)
	if err != nil {
		return driver.ZeroIdentityType, nil, err
	}

	return owner.Type, owner.Identity, nil
}

type ownerWallet interface {
	ID() string
}

func senderWallet(ctx context.Context, w ownerWallet) string {
	return "htlc.sender" + w.ID()
}

func recipientWallet(ctx context.Context, w ownerWallet) string {
	return "htlc.recipient" + w.ID()
}
