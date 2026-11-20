/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ErrDeadlineOutOfWindow is returned when an endorser refuses to sign a delta whose ValidUntil is
// outside the window it accepts. The requester can retry with a fresh proposal.
var ErrDeadlineOutOfWindow = errors.New("validUntil outside the accepted window")

// BlockNumberReader reads the number of the chain's latest block.
type BlockNumberReader interface {
	BlockNumber(ctx context.Context) (uint64, error)
}

// DeadlineWindow bounds the StateDelta.ValidUntil an endorser signs, relative to the latest block it
// sees: more than MinBlocks past it, so the transaction has time to land, and at most MaxBlocks past
// it, so a requester cannot make a transaction live forever.
//
// Endorsers may see slightly different latest blocks. That only moves the edges of the window each
// of them accepts, not the value they sign, so their deltas still match.
type DeadlineWindow struct {
	MinBlocks uint64
	MaxBlocks uint64
}

// Check reports whether validUntil may be signed when the chain is at currentBlock.
func (w DeadlineWindow) Check(validUntil, currentBlock uint64) error {
	if validUntil <= currentBlock+w.MinBlocks || validUntil > currentBlock+w.MaxBlocks {
		return errors.Wrapf(ErrDeadlineOutOfWindow,
			"validUntil %d is not in (%d, %d] at block %d",
			validUntil, currentBlock+w.MinBlocks, currentBlock+w.MaxBlocks, currentBlock)
	}

	return nil
}

// checkDeadline reads the latest block and checks validUntil against w.
func (w DeadlineWindow) checkDeadline(ctx context.Context, chain BlockNumberReader, validUntil uint64) error {
	currentBlock, err := chain.BlockNumber(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to read the latest block to check validUntil")
	}

	return w.Check(validUntil, currentBlock)
}
