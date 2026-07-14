/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
)

type Delivery struct {
	*fabric.Delivery
	*fabric.Ledger
	Logger logging.Logger
}

func (d *Delivery) ScanBlock(background context.Context, callback fabric.BlockCallback) error {
	startingBlock := uint64(0)
	if d.Ledger != nil {
		info, err := d.GetLedgerInfo()
		if err == nil {
			startingBlock = info.Height
		} else {
			d.Logger.ErrorfContext(background, "failed to get ledger info: %s", err)
		}
	}

	return d.ScanBlockFrom(background, startingBlock, callback)
}
