/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
)

// OutputStream models a stream over a set of outputs
type OutputStream struct {
	*token.OutputStream
}

// NewOutputStream creates a new OutputStream for the passed outputs
func NewOutputStream(outputs *token.OutputStream) *OutputStream {
	return &OutputStream{OutputStream: outputs}
}

// Filter filters the OutputStream to only include outputs that match the passed predicate
func (o *OutputStream) Filter(f func(t *token.Output) bool) *OutputStream {
	return NewOutputStream(o.OutputStream.Filter(f))
}

// ByScript filters the OutputStream to only include outputs that are owned by an htlc script
func (o *OutputStream) ByScript() *OutputStream {
	return o.Filter(func(t *token.Output) bool {
		owner, err := identity.UnmarshalTypedIdentity(t.Token.Owner)
		if err != nil {
			return false
		}
		switch owner.Type {
		case ScriptType:
			return true
		}

		return false
	})
}

// ScriptAt returns an htlc script that is the owner of the output at the passed index of the OutputStream
func (o *OutputStream) ScriptAt(i int) *Script {
	tok := o.At(i)
	owner, err := identity.UnmarshalTypedIdentity(tok.Token.Owner)
	if err != nil {
		logger.Debugf("failed unmarshalling raw owner [%s]: [%s]", tok, err)

		return nil
	}
	if owner.Type != ScriptType {
		logger.Debugf("owner type is [%s] instead of [%s]", owner.Type, ScriptType)

		return nil
	}
	script := &Script{}
	if err := json.Unmarshal(owner.Identity, script); err != nil {
		logger.Debugf("failed unmarshalling  htlc script [%s]: [%s]", tok, err)

		return nil
	}
	if script.Sender.IsNone() || script.Recipient.IsNone() {
		return nil
	}

	return script
}
