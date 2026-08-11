/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTokenIDsAndCheckDuplicates(t *testing.T) {
	anchor := driver.TokenRequestAnchor("test-tx")

	t.Run("NoTokenIDs", func(t *testing.T) {
		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{},
		}
		ids, err := ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("TransferTokenIDs_NoDuplicates", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}

		transferMeta := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
				{TokenID: id2},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{TransferMetadata: transferMeta},
			},
		}

		ids, err := ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
		require.NoError(t, err)
		assert.Len(t, ids, 2)
		assert.Equal(t, id1, ids[0])
		assert.Equal(t, id2, ids[1])
	})

	t.Run("IssueInputTokenIDs_NoDuplicates", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		issueMeta := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{
				{TokenID: id1},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{IssueMetadata: issueMeta},
			},
		}

		ids, err := ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
		require.NoError(t, err)
		assert.Len(t, ids, 1)
		assert.Equal(t, id1, ids[0])
	})

	t.Run("DuplicateInTransfer", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		transferMeta := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
				{TokenID: id1}, // Duplicate
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{TransferMetadata: transferMeta},
			},
		}

		ids, err := ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate token ID")
		assert.Nil(t, ids)
	})

	t.Run("DuplicateAcrossActions", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		transferMeta1 := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
			},
		}

		transferMeta2 := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1}, // Duplicate from first action
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{TransferMetadata: transferMeta1},
				{TransferMetadata: transferMeta2},
			},
		}

		ids, err := ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate token ID")
		assert.Nil(t, ids)
	})

	t.Run("NilTokenIDsIgnored", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		transferMeta := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: nil}, // Should be ignored
				{TokenID: id1},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{TransferMetadata: transferMeta},
			},
		}

		ids, err := ExtractTokenIDsAndCheckDuplicates(metadata, anchor)
		require.NoError(t, err)
		assert.Len(t, ids, 1)
		assert.Equal(t, id1, ids[0])
	})
}

func TestRetrieveAuditTokens(t *testing.T) {
	ctx := context.Background()
	logger := &logging.MockLogger{}
	anchor := driver.TokenRequestAnchor("test-tx")

	// testNumRetries mirrors the production default; testRetryDelay is tiny so the
	// pending-status retry path can be exercised without real sleeps.
	const testNumRetries = DefaultAuditTokensNumRetries
	const testRetryDelay = time.Millisecond

	t.Run("EmptyTokenIDs", func(t *testing.T) {
		qe := &mock.QueryEngine{}

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, nil, anchor, testNumRetries, testRetryDelay)
		require.NoError(t, err)
		assert.NotNil(t, tokens)
		assert.Empty(t, tokens)
		assert.Equal(t, 0, qe.ListAuditTokensCallCount())
	})

	t.Run("Success", func(t *testing.T) {
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}
		tokenIDs := []*token.ID{id1, id2}

		tok1 := &token.Token{Type: "USD", Quantity: "100"}
		tok2 := &token.Token{Type: "USD", Quantity: "200"}
		qe.ListAuditTokensReturns([]*token.Token{tok1, tok2}, nil)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.NoError(t, err)
		assert.Len(t, tokens, 2)
		assert.Equal(t, tok1, tokens[id1.String()])
		assert.Equal(t, tok2, tokens[id2.String()])

		assert.Equal(t, 1, qe.ListAuditTokensCallCount())
	})

	t.Run("QueryEngineError", func(t *testing.T) {
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		qe.ListAuditTokensReturns(nil, assert.AnError)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve audit tokens")
		assert.Nil(t, tokens)
	})

	t.Run("NilTokensFiltered", func(t *testing.T) {
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}
		tokenIDs := []*token.ID{id1, id2}

		tok1 := &token.Token{Type: "USD", Quantity: "100"}
		qe.ListAuditTokensReturns([]*token.Token{tok1, nil}, nil)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.NoError(t, err)
		assert.Len(t, tokens, 2)
		assert.Equal(t, tok1, tokens[id1.String()])
		assert.Nil(t, tokens[id2.String()])
	})

	t.Run("RetriesWhilePendingThenSucceeds", func(t *testing.T) {
		// The producing tx is still pending on the first lookup (row not yet
		// persisted), then becomes available on the retry. This is the exact
		// read-timing race from issue #2105: the token must be resolved, not
		// spuriously rejected.
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		tok1 := &token.Token{Type: "USD", Quantity: "100"}
		qe.ListAuditTokensReturnsOnCall(0, nil, errors.New("token not found for key [tx1:0]"))
		qe.ListAuditTokensReturnsOnCall(1, []*token.Token{tok1}, nil)
		qe.IsPendingReturns(true, nil)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.NoError(t, err)
		assert.Len(t, tokens, 1)
		assert.Equal(t, tok1, tokens[id1.String()])
		assert.Equal(t, 2, qe.ListAuditTokensCallCount())
	})

	t.Run("NoRetryWhenNotPending", func(t *testing.T) {
		// A genuine failure (no requested token is pending) must fail fast,
		// without spending the retry budget.
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		qe.ListAuditTokensReturns(nil, assert.AnError)
		qe.IsPendingReturns(false, nil)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.Error(t, err)
		assert.Nil(t, tokens)
		assert.Equal(t, 1, qe.ListAuditTokensCallCount())
	})

	t.Run("ExhaustsRetriesWhileStillPending", func(t *testing.T) {
		// The producing tx never leaves the pending state within the grace
		// window: we give up with a clear "still pending" error that still
		// carries the underlying lookup error.
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		qe.ListAuditTokensReturns(nil, errors.New("token not found for key [tx1:0]"))
		qe.IsPendingReturns(true, nil)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "still pending")
		assert.Contains(t, err.Error(), "token not found for key [tx1:0]")
		assert.Nil(t, tokens)
		assert.Equal(t, testNumRetries, qe.ListAuditTokensCallCount())
	})

	t.Run("IsPendingErrorIsSurfacedNotMaskedAsPending", func(t *testing.T) {
		// When IsPending itself fails (e.g. a store outage), we must not report
		// the tx as "still pending" nor burn the retry budget: the underlying
		// lookup error and the IsPending error are surfaced immediately.
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		qe.ListAuditTokensReturns(nil, errors.New("connection refused"))
		qe.IsPendingReturns(false, errors.New("db is down"))

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, testNumRetries, testRetryDelay)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "still pending")
		assert.Contains(t, err.Error(), "connection refused")
		assert.Contains(t, err.Error(), "db is down")
		assert.Nil(t, tokens)
		assert.Equal(t, 1, qe.ListAuditTokensCallCount())
	})

	t.Run("ContextCancellationInterruptsBackoff", func(t *testing.T) {
		// A cancelled context must abort the backoff immediately instead of
		// pinning the goroutine for the full grace window.
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		qe.ListAuditTokensReturns(nil, errors.New("token not found for key [tx1:0]"))
		qe.IsPendingReturns(true, nil)

		// Use a long delay: if ctx cancellation were ignored the test would hang.
		tokens, err := RetrieveAuditTokens(cancelCtx, logger, qe, tokenIDs, anchor, testNumRetries, time.Hour)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, tokens)
		assert.Equal(t, 1, qe.ListAuditTokensCallCount())
	})

	t.Run("ZeroRetriesMakesSingleAttemptAndNeverReturnsNilNil", func(t *testing.T) {
		// numRetries <= 0 must be clamped to a single attempt; on failure it must
		// return an error (never a nil slice with a nil error, which would panic
		// the caller when it indexes the slice).
		qe := &mock.QueryEngine{}
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tokenIDs := []*token.ID{id1}

		qe.ListAuditTokensReturns(nil, errors.New("token not found for key [tx1:0]"))
		qe.IsPendingReturns(false, nil)

		tokens, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, 0, testRetryDelay)
		require.Error(t, err)
		assert.Nil(t, tokens)
		assert.Equal(t, 1, qe.ListAuditTokensCallCount())
	})
}

// BenchmarkRetrieveAuditTokens measures the latency added by the pending-status
// retry/backoff introduced for issue #2105.
//
//   - NoRace:        the common case — the token is present on the first lookup,
//     so no retry occurs and the added latency is only the (skipped) retry check.
//   - RaceResolvesOnRetry: the issue #2105 race — the first lookup misses while
//     the producing tx is pending, and the token is resolved on the retry. This
//     is where the one retryDelay backoff is paid.
//
// Run with a real delay to see the actual grace-window cost:
//
//	go test ./token/core/common/ -run '^$' -bench BenchmarkRetrieveAuditTokens -benchtime=20x
func BenchmarkRetrieveAuditTokens(b *testing.B) {
	ctx := context.Background()
	logger := &logging.MockLogger{}
	anchor := driver.TokenRequestAnchor("bench-tx")
	id1 := &token.ID{TxId: "tx1", Index: 0}
	tokenIDs := []*token.ID{id1}
	tok1 := &token.Token{Type: "USD", Quantity: "100"}

	b.Run("NoRace", func(b *testing.B) {
		qe := &mock.QueryEngine{}
		qe.ListAuditTokensReturns([]*token.Token{tok1}, nil)

		b.ReportAllocs()
		for range b.N {
			if _, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, DefaultAuditTokensNumRetries, time.Millisecond); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("RaceResolvesOnRetry", func(b *testing.B) {
		// Keep the retry mechanics but use a tiny backoff so the benchmark
		// measures the added path cost rather than the wall-clock delay itself.
		qe := &mock.QueryEngine{}
		qe.IsPendingReturns(true, nil)
		qe.ListAuditTokensStub = func(_ context.Context, _ ...*token.ID) ([]*token.Token, error) {
			// Miss on every odd call, hit on every even call, so each iteration
			// pays exactly one retry.
			if qe.ListAuditTokensCallCount()%2 == 1 {
				return nil, errors.New("token not found for key [tx1:0]")
			}

			return []*token.Token{tok1}, nil
		}

		b.ReportAllocs()
		for range b.N {
			if _, err := RetrieveAuditTokens(ctx, logger, qe, tokenIDs, anchor, DefaultAuditTokensNumRetries, time.Millisecond); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestValidateStructure(t *testing.T) {
	txID := driver.TokenRequestAnchor("test-tx")

	t.Run("Success_IssueAction", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue")},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID:      0,
					IssueMetadata: &driver.IssueMetadata{},
				},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.NoError(t, err)
	})

	t.Run("Success_TransferAction", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer")},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID:         0,
					TransferMetadata: &driver.TransferMetadata{},
				},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.NoError(t, err)
	})

	t.Run("ActionCountMismatch", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "action count mismatch")
	})

	t.Run("NilAction", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{nil},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "action at index [0] is nil")
	})

	t.Run("NilMetadata", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{nil},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "metadata at index [0] is nil")
	})

	t.Run("IncorrectActionID", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID:      99, // Should be 0
					IssueMetadata: &driver.IssueMetadata{},
				},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "incorrect ActionID")
	})

	t.Run("IssueAction_MissingIssueMetadata", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0}, // No IssueMetadata
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no IssueMetadata")
	})

	t.Run("IssueAction_HasTransferMetadata", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID:         0,
					IssueMetadata:    &driver.IssueMetadata{},
					TransferMetadata: &driver.TransferMetadata{}, // Should not have both
				},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "also has TransferMetadata")
	})

	t.Run("TransferAction_MissingTransferMetadata", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0}, // No TransferMetadata
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no TransferMetadata")
	})

	t.Run("TransferAction_HasIssueMetadata", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER},
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID:         0,
					TransferMetadata: &driver.TransferMetadata{},
					IssueMetadata:    &driver.IssueMetadata{}, // Should not have both
				},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "also has IssueMetadata")
	})

	t.Run("UnknownActionType", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType(999)}, // Unknown type
			},
		}

		metadata := &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0, IssueMetadata: &driver.IssueMetadata{}},
			},
		}

		err := ValidateStructure(tr, metadata, txID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown type")
	})
}

func TestValidateIssueActionTokenTypes(t *testing.T) {
	t.Run("NoInputs", func(t *testing.T) {
		metadata := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{},
		}

		// Empty audit tokens map is acceptable for issue actions with no inputs
		err := ValidateIssueActionTokenTypes(metadata, make(map[string]*token.Token))
		require.NoError(t, err)
	})

	t.Run("SingleInput_Success", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{
				{TokenID: id1},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
		}

		err := ValidateIssueActionTokenTypes(metadata, auditTokens)
		require.NoError(t, err)
	})

	t.Run("MultipleInputs_SameType_Success", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}
		tok2 := &token.Token{Type: "USD", Quantity: "200"}

		metadata := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{
				{TokenID: id1},
				{TokenID: id2},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
			id2.String(): tok2,
		}

		err := ValidateIssueActionTokenTypes(metadata, auditTokens)
		require.NoError(t, err)
	})

	t.Run("InputNotFound", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		metadata := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{
				{TokenID: id1},
			},
		}

		auditTokens := map[string]*token.Token{} // Empty

		err := ValidateIssueActionTokenTypes(metadata, auditTokens)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in audit tokens")
	})

	t.Run("TypeMismatch", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}
		tok2 := &token.Token{Type: "EUR", Quantity: "200"} // Different type

		metadata := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{
				{TokenID: id1},
				{TokenID: id2},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
			id2.String(): tok2,
		}

		err := ValidateIssueActionTokenTypes(metadata, auditTokens)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token type mismatch")
	})

	t.Run("NilInputMetadata_Skipped", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.IssueMetadata{
			Inputs: []*driver.IssueInputMetadata{
				nil, // Should be skipped
				{TokenID: id1},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
		}

		err := ValidateIssueActionTokenTypes(metadata, auditTokens)
		require.NoError(t, err)
	})
}

func TestValidateTransferActionTokenTypes(t *testing.T) {
	t.Run("Success_WithoutValueValidation", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, false, 64)
		require.NoError(t, err)
	})

	t.Run("Success_WithValueValidation", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, true, 64)
		require.NoError(t, err)
	})

	t.Run("NilInputMetadata", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{nil},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "input metadata at index [0] is nil")
	})

	t.Run("NilTokenID", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: nil},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has nil TokenID")
	})

	t.Run("TokenNotFound", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}
		tok2 := &token.Token{Type: "USD", Quantity: "100"}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1}, // This one is missing
				{TokenID: id2},
			},
		}

		auditTokens := map[string]*token.Token{
			id2.String(): tok2, // Only id2 is present
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in audit tokens")
	})

	t.Run("NilToken", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): nil, // Nil token
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is nil in audit tokens")
	})

	t.Run("TypeMismatch", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}
		id2 := &token.ID{TxId: "tx1", Index: 1}
		tok1 := &token.Token{Type: "USD", Quantity: "100"}
		tok2 := &token.Token{Type: "EUR", Quantity: "200"} // Different type

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
				{TokenID: id2},
			},
		}

		auditTokens := map[string]*token.Token{
			id1.String(): tok1,
			id2.String(): tok2,
		}

		err := ValidateTransferActionTokenTypes(metadata, auditTokens, false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token type mismatch")
	})

	t.Run("NilAuditTokens_Error", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
			},
		}

		// Nil audit tokens should return error
		err := ValidateTransferActionTokenTypes(metadata, nil, false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "auditTokens cannot be nil")
	})

	t.Run("EmptyAuditTokens_Error", func(t *testing.T) {
		id1 := &token.ID{TxId: "tx1", Index: 0}

		metadata := &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{TokenID: id1},
			},
		}

		// Empty audit tokens should return error
		err := ValidateTransferActionTokenTypes(metadata, make(map[string]*token.Token), false, 64)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "auditTokens cannot be empty")
	})
}
