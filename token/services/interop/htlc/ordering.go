/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// NewOrderingAndFinalityView returns a new instance of the ttx orderingAndFinalityView struct
func NewOrderingAndFinalityView(tx *Transaction) view.View {
	return ttx.NewOrderingAndFinalityView(tx.Transaction)
}

// NewOrderingAndFinalityWithTimeoutView returns a new instance of the ttx orderingAndFinalityWithTimeoutView struct
func NewOrderingAndFinalityWithTimeoutView(tx *Transaction, timeout time.Duration) view.View {
	return ttx.NewOrderingAndFinalityWithTimeoutView(tx.Transaction, timeout)
}
