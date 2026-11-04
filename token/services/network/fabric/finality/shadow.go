/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/fabricutils"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
)

// A Fabric block can carry several envelopes with the same transaction ID. Fabric
// records a txID for duplicate detection only when its envelope validates as VALID
// (markTXIdDuplicates in Fabric's core/committer/txvalidator), so a block holds at
// most one VALID envelope per txID. Any party that learns a txID before it is
// ordered can sign an envelope carrying it with its own identity and have it
// ordered ahead of the honest one; the peer flags the forgery BAD_PROPOSAL_TXID
// and the honest envelope later in the same block is still VALID.
//
// The single rule applied here, matching fabric-smart-client's committer, is: the
// verdict of a txID in a block is that of its VALID envelope if the block has one,
// and otherwise that of its first envelope. The helpers below implement that rule
// for the two ways Panurus consumes a block: filtering a delivered block before it
// reaches the per-envelope finality/lookup mappers (SkipShadowedEnvelopes), and
// resolving the one envelope that carries a txID's true outcome (ValidEnvelope).

// transactionsFilter returns the block's per-transaction validation flags, or nil
// when the block carries none. The slice is parallel to block.Data.Data: flag i is
// the validation code of envelope i.
func transactionsFilter(block *common.Block) []byte {
	md := block.GetMetadata()
	if md == nil || int(common.BlockMetadataIndex_TRANSACTIONS_FILTER) >= len(md.Metadata) {
		return nil
	}

	return md.Metadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER]
}

// isValidAt reports whether the envelope at index i validated as VALID. A missing
// flag (a filter shorter than the data, which is already a malformed block) is
// treated as not VALID, so a shadowed envelope is never dropped on the strength of
// an absent flag.
func isValidAt(flags []byte, i int) bool {
	return i < len(flags) && peer.TxValidationCode(flags[i]) == peer.TxValidationCode_VALID
}

// SkipShadowedEnvelopes returns a block equal to the input with every endorser
// envelope Fabric found invalid whose transaction ID a VALID endorser envelope in
// the same block also carries removed, together with its aligned
// TRANSACTIONS_FILTER byte. This leaves at most one envelope per shadowed txID —
// the VALID one — so a per-envelope mapper that walks block.Data.Data yields a
// single, correct event for that txID instead of the forgery's Invalid event
// followed by the honest Valid one.
//
// The returned block shares the input's header and untouched metadata entries; only
// Data.Data and the transaction filter are rebuilt, and the input is left
// unmodified. When the block carries no transaction filter, or no invalid envelope
// is shadowed by a valid one, the input block is returned unchanged.
//
// Only endorser transactions are considered: a non-endorser envelope (a config
// transaction, say) is never dropped and never shadows another, matching the
// committer's rule. An envelope whose header cannot be unmarshalled is left in
// place, so a malformed envelope is reported downstream exactly as before rather
// than silently removed.
func SkipShadowedEnvelopes(block *common.Block) *common.Block {
	if block == nil || block.Data == nil {
		return block
	}
	flags := transactionsFilter(block)
	if flags == nil {
		return block
	}
	data := block.Data.Data

	// Collect the txIDs carried by a VALID endorser envelope.
	validIDs := make(map[string]struct{})
	endorserTxIDs := make([]string, len(data))
	for i, raw := range data {
		_, _, chdr, err := fabricutils.UnmarshalTx(raw)
		if err != nil || chdr == nil || common.HeaderType(chdr.Type) != common.HeaderType_ENDORSER_TRANSACTION {
			continue
		}
		endorserTxIDs[i] = chdr.TxId
		if isValidAt(flags, i) {
			validIDs[chdr.TxId] = struct{}{}
		}
	}

	// An envelope is shadowed when it is an invalid endorser envelope whose txID a
	// valid endorser envelope in this block also carries.
	shadowed := func(i int) bool {
		txID := endorserTxIDs[i]
		if txID == "" || isValidAt(flags, i) {
			return false
		}
		_, ok := validIDs[txID]

		return ok
	}

	any := false
	for i := range data {
		if shadowed(i) {
			any = true

			break
		}
	}
	if !any {
		return block
	}

	newData := make([][]byte, 0, len(data))
	newFlags := make([]byte, 0, len(flags))
	for i, raw := range data {
		if shadowed(i) {
			continue
		}
		newData = append(newData, raw)
		// Keep the filter aligned with the data: append this envelope's flag only
		// when it had one, preserving any original data-longer-than-filter shape.
		if i < len(flags) {
			newFlags = append(newFlags, flags[i])
		}
	}

	md := block.Metadata
	newMetadata := make([][]byte, len(md.Metadata))
	copy(newMetadata, md.Metadata)
	newMetadata[common.BlockMetadataIndex_TRANSACTIONS_FILTER] = newFlags

	return &common.Block{
		Header:   block.Header,
		Data:     &common.BlockData{Data: newData},
		Metadata: &common.BlockMetadata{Metadata: newMetadata},
	}
}

// ValidEnvelope returns the index of the VALID endorser envelope that carries txID
// in block, and true when the block has one. It is the counterpart to
// SkipShadowedEnvelopes for a caller that already holds a txID's (forged) verdict
// from a point lookup and only needs to know whether the same block also holds a
// VALID envelope for that txID whose verdict should override it.
//
// found is false when no VALID endorser envelope in the block carries txID — either
// because the block has no VALID envelope for it (the forgery stands as the real
// outcome) or because the block carries no transaction filter. The returned index
// is the envelope's position in block.Data.Data, suitable as the txNum passed to a
// per-envelope mapper against this same (unfiltered) block.
func ValidEnvelope(block *common.Block, txID string) (uint64, bool) {
	if block == nil || block.Data == nil {
		return 0, false
	}
	flags := transactionsFilter(block)
	if flags == nil {
		return 0, false
	}
	for i, raw := range block.Data.Data {
		if !isValidAt(flags, i) {
			continue
		}
		_, _, chdr, err := fabricutils.UnmarshalTx(raw)
		if err != nil || chdr == nil || common.HeaderType(chdr.Type) != common.HeaderType_ENDORSER_TRANSACTION {
			continue
		}
		if chdr.TxId == txID {
			return uint64(i), true
		}
	}

	return 0, false
}

// BlockNumberByTxID resolves the block that carries txID. It is the subset of
// fabric.Ledger that HonestEnvelope needs.
type BlockNumberByTxID interface {
	GetBlockNumberByTxID(txID string) (uint64, error)
}

// BlockScanner scans delivered blocks starting from a given block number. It is the
// subset of fabric.Delivery that HonestEnvelope needs.
type BlockScanner interface {
	ScanBlockFrom(ctx context.Context, block uint64, callback fabric.BlockCallback) error
}

// HonestEnvelope resolves the envelope whose verdict is the real outcome of txID
// when a point lookup may have returned a forgery. It locates the block carrying
// txID and scans it for a VALID endorser envelope with that txID, returning the
// scanned block and the envelope's index within it.
//
// Only the transaction's own block is scanned — bounded to a single block by
// stopping the scan as soon as that block is delivered — because an honest envelope
// in any later block is flagged DUPLICATE_TXID by Fabric and so is never VALID.
//
// found is false, with a nil error, when the block holds no VALID envelope for txID
// (the point-lookup verdict stands). A non-nil error means the block could not be
// read; a caller should fall back to the point-lookup verdict rather than fail.
func HonestEnvelope(ctx context.Context, ledger BlockNumberByTxID, delivery BlockScanner, txID string) (block *common.Block, txNum uint64, found bool, err error) {
	blockNum, err := ledger.GetBlockNumberByTxID(txID)
	if err != nil {
		return nil, 0, false, errors.Wrapf(err, "failed to get block number for transaction [%s]", txID)
	}

	scanErr := delivery.ScanBlockFrom(ctx, blockNum, func(_ context.Context, b *common.Block) (bool, error) {
		// Scan exactly the transaction's block, then stop regardless of the outcome:
		// the delivery resumes from blockNum, so the first delivered block is the one
		// to inspect, and no later block can hold a VALID envelope for this txID.
		if idx, ok := ValidEnvelope(b, txID); ok {
			block = b
			txNum = idx
			found = true
		}

		return true, nil
	})
	if scanErr != nil {
		return nil, 0, false, errors.Wrapf(scanErr, "failed to scan block [%d] for transaction [%s]", blockNum, txID)
	}

	return block, txNum, found, nil
}
