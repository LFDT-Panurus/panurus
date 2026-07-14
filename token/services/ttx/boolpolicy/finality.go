/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// NewFinalityView returns a view that waits for the given transaction to reach finality.
func NewFinalityView(tx *Transaction, opts ...ttx.TxOption) view.View {
	return ttx.NewFinalityView(tx.Transaction, opts...)
}
