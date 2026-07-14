/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// NewAcceptView returns an instance of the ttx acceptView struct
func NewAcceptView(tx *Transaction) view.View {
	return ttx.NewAcceptView(tx.Transaction)
}
