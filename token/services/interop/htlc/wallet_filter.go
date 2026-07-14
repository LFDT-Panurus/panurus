/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// SelectFunction is the prototype of a function to select pairs (token,script)
type SelectFunction = func(*token.UnspentToken, *Script) (bool, error)

// PreImageSelector selects htlc-tokens that match a given pre-image
type PreImageSelector struct {
	preImage []byte
}

func (f *PreImageSelector) Filter(tok *token.UnspentToken, script *Script) (bool, error) {
	logger.Debugf("token [%s,%s,%s,%s] contains a script? Yes", tok.Id, view.Identity(tok.Owner).UniqueID(), tok.Type, tok.Quantity)

	if !script.HashInfo.HashFunc.Available() {
		logger.Errorf("script hash function not available [%d]", script.HashInfo.HashFunc)

		return false, nil
	}
	hash := script.HashInfo.HashFunc.New()
	if _, err := hash.Write(f.preImage); err != nil {
		return false, err
	}
	h := hash.Sum(nil)
	h = []byte(script.HashInfo.HashEncoding.New().EncodeToString(h))

	logger.Debugf("searching for script matching (pre-image, image) = (%s,%s)", logging.Base64(f.preImage), logging.Base64(h))

	// does the preimage match?
	logger.Debugf("token [%s,%s,%s,%s] does hashes match?", tok.Id, view.Identity(tok.Owner), tok.Type, tok.Quantity, logging.Base64(h), logging.Base64(script.HashInfo.Hash))

	return bytes.Equal(h, script.HashInfo.Hash), nil
}

// SelectExpired selects expired htlc-tokens
func SelectExpired(tok *token.UnspentToken, script *Script) (bool, error) {
	logger.Debugf("token [%s,%s,%s,%s] contains a script? Yes", tok.Id, view.Identity(tok.Owner).UniqueID(), tok.Type, tok.Quantity)
	now := time.Now()
	logger.Debugf("[%v]<=[%v], sender [%s], recipient [%s]?", script.Deadline, now, script.Sender.UniqueID(), script.Recipient.UniqueID())

	return script.Deadline.Before(now), nil
}

// SelectNonExpired selects non-expired htlc-tokens
func SelectNonExpired(tok *token.UnspentToken, script *Script) (bool, error) {
	now := time.Now()
	logger.Debugf("[%v]>=[%v], sender [%s], recipient [%s]?", script.Deadline, now, script.Sender.UniqueID(), script.Recipient.UniqueID())

	return script.Deadline.After(now), nil
}

// ExpiredAndHashSelector selects expired htlc-tokens with a specific hash
type ExpiredAndHashSelector struct {
	Hash []byte
}

func (s *ExpiredAndHashSelector) Select(tok *token.UnspentToken, script *Script) (bool, error) {
	logger.Debugf("token [%s,%s,%s,%s] contains a script? Yes", tok.Id, view.Identity(tok.Owner).UniqueID(), tok.Type, tok.Quantity)
	now := time.Now()
	logger.Debugf("[%v]<=[%v], sender [%s], recipient [%s]?", script.Deadline, now, script.Sender.UniqueID(), script.Recipient.UniqueID())

	return script.Deadline.Before(now) && bytes.Equal(script.HashInfo.Hash, s.Hash), nil
}

func IsScript(selector SelectFunction) iterators.Predicate[*token.UnspentToken] {
	return func(tok *token.UnspentToken) bool {
		owner, err := identity.UnmarshalTypedIdentity(tok.Owner)
		if err != nil {
			logger.Debugf("Is Mine [%s,%s,%s]? No, failed unmarshalling [%s]", view.Identity(tok.Owner), tok.Type, tok.Quantity, err)

			return false
		}
		if owner.Type != ScriptType {
			return false
		}

		script := &Script{}
		if err := json.Unmarshal(owner.Identity, script); err != nil {
			logger.Debugf("token [%s,%s,%s,%s] contains a script? No", tok.Id, view.Identity(tok.Owner).UniqueID(), tok.Type, tok.Quantity)

			return false
		}
		if script.Sender.IsNone() {
			logger.Debugf("token [%s,%s,%s,%s] contains a script? No", tok.Id, view.Identity(tok.Owner).UniqueID(), tok.Type, tok.Quantity)

			return false
		}
		logger.Debugf("token [%s,%s,%s,%s] contains a script? Yes", tok.Id, view.Identity(tok.Owner).UniqueID(), tok.Type, tok.Quantity)

		pickItem, err := selector(tok, script)
		if err != nil {
			logger.Errorf("failed to select (token,script)[%v:%v] pair: %w", tok, script, err)

			return false
		}

		return pickItem
	}
}
