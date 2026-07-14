/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"time"

	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/LFDT-Panurus/panurus/token/token"
)

func HasTokens(colTxID, colIdx common.FieldName, ids ...*token.ID) cond.Condition {
	return hasTokens(colTxID, colIdx, ids...)
}

func hasTokens(colTxID, colIdx common.Field, ids ...*token.ID) cond.Condition {
	if len(ids) == 0 {
		return cond.AlwaysTrue
	}

	vals := make([]common.Tuple, len(ids))
	for i, id := range ids {
		vals[i] = common.Tuple{id.TxId, id.Index}
	}

	return cond.InTuple([]common.Serializable{colTxID, colIdx}, vals)
}

func HasTokenDetails(params driver2.QueryTokenDetailsParams, tokenTable common.Table) cond.Condition {
	conds := []cond.Condition{cond.Eq("owner", true)}

	if len(params.OwnerType) > 0 {
		conds = append(conds, cond.Eq("owner_type", params.OwnerType))
	}
	if len(params.TokenType) > 0 {
		conds = append(conds, cond.Eq("token_type", params.TokenType))
	}
	// WalletIDs takes precedence over WalletID when both are set; normalize
	// to a single list so each branch below applies the rule identically.
	effectiveWallets := params.WalletIDs
	if len(effectiveWallets) == 0 && len(params.WalletID) > 0 {
		effectiveWallets = []string{params.WalletID}
	}

	if tokenTable != nil {
		// Match both wallet_id and owner_wallet_id — some tokens are stored
		// only under the Ownership join, some only under owner_wallet_id.
		if len(effectiveWallets) > 0 {
			conds = append(conds, cond.Or(
				cond.In("wallet_id", effectiveWallets...),
				cond.In("owner_wallet_id", effectiveWallets...),
			))
		}
		conds = append(conds,
			cond.FieldIn(tokenTable.Field("tx_id"), params.TransactionIDs...),
			hasTokens(tokenTable.Field("tx_id"), tokenTable.Field("idx"), params.IDs...),
		)
	} else {
		if len(effectiveWallets) > 0 {
			conds = append(conds, cond.In("owner_wallet_id", effectiveWallets...))
		}
		conds = append(conds,
			cond.FieldIn(common.FieldName("tx_id"), params.TransactionIDs...),
			hasTokens(common.FieldName("tx_id"), common.FieldName("idx"), params.IDs...),
		)
	}
	if !params.IncludeDeleted {
		conds = append(conds, cond.Eq("is_deleted", false))
	}
	switch params.Spendable {
	case driver2.NonSpendableOnly:
		conds = append(conds, cond.Eq("spendable", false))
	case driver2.SpendableOnly:
		conds = append(conds, cond.Eq("spendable", true))
	}

	conds = append(conds, cond.In("ledger_type", params.LedgerTokenFormats...))

	return cond.And(conds...)
}

func HasMovementsParams(params driver2.QueryMovementsParams) cond.Condition {
	conds := []cond.Condition{
		cond.In("enrollment_id", params.EnrollmentIDs...),
		cond.In("token_type", params.TokenTypes...),
		cond.In("status", params.TxStatuses...),
	}

	if len(params.TxStatuses) == 0 {
		conds = append(conds, cond.Neq("status", driver2.Deleted))
	}

	switch params.MovementDirection {
	case driver2.Sent:
		conds = append(conds, cond.Lt("amount", 0))
	case driver2.Received:
		conds = append(conds, cond.Gt("amount", 0))
	}

	return cond.And(conds...)
}

func HasValidationParams(params driver2.QueryValidationRecordsParams, tableName string) cond.Condition {
	return cond.And(
		cond.BetweenTimestamps(common.FieldName(tableName+".stored_at"), utc(params.From), utc(params.To)),
		cond.In("status", params.Statuses...),
	)
}

func HasTransactionParams(params driver2.QueryTransactionsParams, table common.Table) cond.Condition {
	conds := []cond.Condition{
		cond.FieldIn(table.Field("tx_id"), params.IDs...),
		cond.FieldBetweenTimestamps(table.Field("stored_at"), utc(params.From), utc(params.To)),
		cond.In("action_type", params.ActionTypes...),
		// Specific transaction status if requested, defaults to all but Deleted
		cond.In("status", params.Statuses...),
		cond.In("token_type", params.TokenTypes...),
	}

	if params.ExcludeToSelf {
		conds = append(conds, cond.Cmp(common.FieldName("sender_eid"), "!=", common.FieldName("recipient_eid")))
	}

	// See QueryTransactionsParams for expected behavior. If only one of sender or
	// recipient is set, we return all transactions. If both are set, we do an OR.
	if params.SenderWallet != "" && params.RecipientWallet != "" {
		conds = append(conds, cond.Or(
			cond.Eq("sender_eid", params.SenderWallet),
			cond.Eq("recipient_eid", params.RecipientWallet),
		))
	}

	return cond.And(conds...)
}

func utc(t *time.Time) time.Time {
	if t == nil || t.IsZero() {
		return time.Time{}
	}

	return t.UTC()
}
