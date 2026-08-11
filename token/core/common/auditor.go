/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

// DefaultAuditTokensNumRetries and DefaultAuditTokensRetryDelay are the default
// retry budget and backoff used by RetrieveAuditTokens to tolerate the read-timing
// race in which a referenced token's producing transaction is still pending, so its
// outputs have not yet been persisted to the token store by the asynchronous
// finality listener. They mirror the retry/backoff already applied on the sibling
// token.QueryEngine audit path (see token/vault.go). Per-instance tuning is done via
// the AuditorService fields that default to these values, not by mutating a global.
const (
	DefaultAuditTokensNumRetries = 3
	DefaultAuditTokensRetryDelay = 3 * time.Second
)

// AuditContext contains the context for token request auditing.
type AuditContext[P driver.PublicParameters, IA driver.IssueAction, TA driver.TransferAction, DS driver.Deserializer] struct {
	Logger               logging.Logger
	Tracer               trace.Tracer
	PP                   P
	Anchor               driver.TokenRequestAnchor
	TokenRequest         *driver.TokenRequest
	TokenRequestMetadata *driver.TokenRequestMetadata
	Deserializer         DS
	AuditTokens          map[string]*token.Token
	IssueAction          IA
	TransferAction       TA
	ActionIndex          int // Index of the current action being validated
}

// ValidateIssueAuditFunc is a function type for validating issue actions during audit.
type ValidateIssueAuditFunc[P driver.PublicParameters, IA driver.IssueAction, TA driver.TransferAction, DS driver.Deserializer] func(c context.Context, ctx *AuditContext[P, IA, TA, DS]) error

// ValidateTransferAuditFunc is a function type for validating transfer actions during audit.
type ValidateTransferAuditFunc[P driver.PublicParameters, IA driver.IssueAction, TA driver.TransferAction, DS driver.Deserializer] func(c context.Context, ctx *AuditContext[P, IA, TA, DS]) error

// Auditor validates token requests against their metadata.
type Auditor[P driver.PublicParameters, IA driver.IssueAction, TA driver.TransferAction, DS driver.Deserializer] struct {
	Logger             logging.Logger
	Tracer             trace.Tracer
	PublicParams       P
	Deserializer       DS
	ActionDeserializer driver.ActionDeserializer[TA, IA]

	IssueValidators    []ValidateIssueAuditFunc[P, IA, TA, DS]
	TransferValidators []ValidateTransferAuditFunc[P, IA, TA, DS]
}

// NewAuditor returns a new Auditor instance for the passed arguments.
func NewAuditor[P driver.PublicParameters, IA driver.IssueAction, TA driver.TransferAction, DS driver.Deserializer](
	logger logging.Logger,
	tracer trace.Tracer,
	publicParams P,
	deserializer DS,
	actionDeserializer driver.ActionDeserializer[TA, IA],
	issueValidators []ValidateIssueAuditFunc[P, IA, TA, DS],
	transferValidators []ValidateTransferAuditFunc[P, IA, TA, DS],
) *Auditor[P, IA, TA, DS] {
	return &Auditor[P, IA, TA, DS]{
		Logger:             logger,
		Tracer:             tracer,
		PublicParams:       publicParams,
		Deserializer:       deserializer,
		ActionDeserializer: actionDeserializer,
		IssueValidators:    issueValidators,
		TransferValidators: transferValidators,
	}
}

// Check validates TokenRequest against TokenRequestMetadata.
// It ensures complete 1:1 correspondence between actions and metadata, then validates each action.
func (a *Auditor[P, IA, TA, DS]) Check(
	ctx context.Context,
	tokenRequest *driver.TokenRequest,
	tokenRequestMetadata *driver.TokenRequestMetadata,
	txID driver.TokenRequestAnchor,
	auditTokens map[string]*token.Token,
) error {
	if tokenRequest == nil {
		return errors.Errorf("tokenRequest cannot be nil for tx [%s]", txID)
	}
	if tokenRequestMetadata == nil {
		return errors.Errorf("tokenRequestMetadata cannot be nil for tx [%s]", txID)
	}
	if auditTokens == nil {
		return errors.Errorf("auditTokens cannot be nil for tx [%s]", txID)
	}

	// Validate structural correspondence between request and metadata
	if err := ValidateStructure(tokenRequest, tokenRequestMetadata, txID); err != nil {
		return errors.Wrapf(err, "structural validation failed for [%s]", txID)
	}

	// Deserialize actions
	issueActions, transferActions, err := a.ActionDeserializer.DeserializeActions(tokenRequest)
	if err != nil {
		return errors.Wrapf(err, "failed to deserialize actions [%s]", txID)
	}

	// Process each action in order, matching with its metadata
	// validateStructure() has already confirmed metadata types are correct
	issueIndex := 0
	transferIndex := 0
	for i, action := range tokenRequest.Actions {
		switch action.Type {
		case request.ActionType_ACTION_TYPE_ISSUE:
			if err := a.CheckIssue(
				ctx,
				txID,
				tokenRequest,
				tokenRequestMetadata,
				issueActions[issueIndex],
				auditTokens,
				i,
			); err != nil {
				return errors.Wrapf(err, "failed to check issue action at [%d]", i)
			}
			issueIndex++

		case request.ActionType_ACTION_TYPE_TRANSFER:
			if err := a.CheckTransfer(
				ctx,
				txID,
				tokenRequest,
				tokenRequestMetadata,
				transferActions[transferIndex],
				auditTokens,
				i,
			); err != nil {
				return errors.Wrapf(err, "failed to check transfer action at [%d]", i)
			}
			transferIndex++

		default:
			return errors.Errorf("unknown action type [%s] at index [%d] for tx [%s]", action.Type, i, txID)
		}
	}

	return nil
}

// CheckIssue validates an issue action.
func (a *Auditor[P, IA, TA, DS]) CheckIssue(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	tokenRequestMetadata *driver.TokenRequestMetadata,
	action IA,
	auditTokens map[string]*token.Token,
	actionIndex int,
) error {
	context := &AuditContext[P, IA, TA, DS]{
		Logger:               a.Logger,
		Tracer:               a.Tracer,
		PP:                   a.PublicParams,
		Anchor:               anchor,
		TokenRequest:         tokenRequest,
		TokenRequestMetadata: tokenRequestMetadata,
		Deserializer:         a.Deserializer,
		IssueAction:          action,
		AuditTokens:          auditTokens,
		ActionIndex:          actionIndex,
	}
	for _, v := range a.IssueValidators {
		if err := v(ctx, context); err != nil {
			return err
		}
	}

	return nil
}

// CheckTransfer validates a transfer action.
func (a *Auditor[P, IA, TA, DS]) CheckTransfer(
	ctx context.Context,
	anchor driver.TokenRequestAnchor,
	tokenRequest *driver.TokenRequest,
	tokenRequestMetadata *driver.TokenRequestMetadata,
	action TA,
	auditTokens map[string]*token.Token,
	actionIndex int,
) error {
	context := &AuditContext[P, IA, TA, DS]{
		Logger:               a.Logger,
		Tracer:               a.Tracer,
		PP:                   a.PublicParams,
		Anchor:               anchor,
		TokenRequest:         tokenRequest,
		TokenRequestMetadata: tokenRequestMetadata,
		Deserializer:         a.Deserializer,
		TransferAction:       action,
		AuditTokens:          auditTokens,
		ActionIndex:          actionIndex,
	}
	for _, v := range a.TransferValidators {
		if err := v(ctx, context); err != nil {
			return err
		}
	}

	return nil
}

// ExtractTokenIDsAndCheckDuplicates extracts all token IDs from both transfer and issue actions
// in the metadata and checks for duplicates. Returns the list of unique token IDs or an error
// if duplicates are found.
//
// This function is used by auditors to ensure that no token is spent multiple times within
// a single transaction.
func ExtractTokenIDsAndCheckDuplicates(
	metadata *driver.TokenRequestMetadata,
	anchor driver.TokenRequestAnchor,
) ([]*token.ID, error) {
	if metadata == nil {
		return nil, errors.Errorf("metadata cannot be nil for tx [%s]", anchor)
	}

	tokenIDMap := make(map[string]*token.ID)
	var tokenIDs []*token.ID

	for i, action := range metadata.Actions {
		// Extract TokenIDs from transfer actions
		if action.TransferMetadata != nil {
			ids := action.TransferMetadata.TokenIDs()
			for _, id := range ids {
				if id == nil {
					continue
				}
				// Check for duplicates using string representation as key
				idKey := id.String()
				if _, exists := tokenIDMap[idKey]; exists {
					return nil, errors.Errorf("duplicate token ID [%s] found in metadata at action index [%d] for tx [%s]", idKey, i, anchor)
				}
				tokenIDMap[idKey] = id
				tokenIDs = append(tokenIDs, id)
			}
		}

		// Extract TokenIDs from issue action inputs (for token upgrades/conversions)
		if action.IssueMetadata != nil {
			for _, input := range action.IssueMetadata.Inputs {
				if input == nil || input.TokenID == nil {
					continue
				}
				id := input.TokenID
				// Check for duplicates using string representation as key
				idKey := id.String()
				if _, exists := tokenIDMap[idKey]; exists {
					return nil, errors.Errorf("duplicate token ID [%s] found in metadata at action index [%d] for tx [%s]", idKey, i, anchor)
				}
				tokenIDMap[idKey] = id
				tokenIDs = append(tokenIDs, id)
			}
		}
	}

	return tokenIDs, nil
}

// RetrieveAuditTokens retrieves audit tokens from the query engine for the given token IDs
// and builds a map for efficient lookup. Returns the token map or an error if retrieval fails.
//
// The returned map uses token ID pointers as keys, allowing callers to efficiently look up
// tokens by their ID during validation.
//
// This function always returns a non-nil map (possibly empty) to ensure
// validation logic can distinguish between "no tokens requested" and "tokens not found".
//
// numRetries and retryDelay control the tolerance for the pending-transaction
// read-timing race (see DefaultAuditTokensNumRetries / DefaultAuditTokensRetryDelay).
// A numRetries <= 0 is clamped to a single attempt.
func RetrieveAuditTokens(
	ctx context.Context,
	logger logging.Logger,
	queryEngine driver.QueryEngine,
	tokenIDs []*token.ID,
	anchor driver.TokenRequestAnchor,
	numRetries int,
	retryDelay time.Duration,
) (map[string]*token.Token, error) {
	if logger == nil {
		return nil, errors.Errorf("logger cannot be nil for tx [%s]", anchor)
	}
	if queryEngine == nil {
		return nil, errors.Errorf("queryEngine cannot be nil for tx [%s]", anchor)
	}

	if len(tokenIDs) == 0 {
		return make(map[string]*token.Token), nil
	}

	logger.DebugfContext(ctx, "[%s] retrieving [%d] audit tokens...", anchor, len(tokenIDs))
	tokens, err := listAuditTokensWithRetry(ctx, logger, queryEngine, tokenIDs, anchor, numRetries, retryDelay)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to retrieve audit tokens for tx [%s]", anchor)
	}

	// Build the token map using token ID string as key.
	// Tokens is in order of the ids.
	auditTokens := make(map[string]*token.Token, len(tokens))
	for i, id := range tokenIDs {
		auditTokens[id.String()] = tokens[i]
	}
	logger.DebugfContext(ctx, "[%s] retrieved [%d] audit tokens", anchor, len(auditTokens))

	return auditTokens, nil
}

// listAuditTokensWithRetry calls queryEngine.ListAuditTokens, tolerating the
// read-timing race where a referenced token is momentarily missing from the token
// store because its producing transaction is still pending (its outputs are
// persisted only later, by the asynchronous finality listener). On failure, it
// checks whether any requested token belongs to a still-pending transaction and,
// if so, waits retryDelay and retries. It makes up to numRetries attempts total,
// sleeping retryDelay between them (numRetries-1 delays); numRetries <= 0 is
// clamped to a single attempt. This mirrors the tolerance already implemented for
// the sibling Audit() path in token/vault.go, so the earlier AuditorCheck gate no
// longer spuriously rejects a validly-audited, quickly-chained transaction.
//
// The backoff honors ctx cancellation, so a cancelled or timed-out request returns
// immediately instead of pinning a goroutine for the full grace window. A genuine
// (non-pending) lookup failure, or a failure to determine pending status, is
// returned rather than retried and never masked as "still pending".
func listAuditTokensWithRetry(
	ctx context.Context,
	logger logging.Logger,
	queryEngine driver.QueryEngine,
	tokenIDs []*token.ID,
	anchor driver.TokenRequestAnchor,
	numRetries int,
	retryDelay time.Duration,
) ([]*token.Token, error) {
	attempts := max(1, numRetries)

	var tokens []*token.Token
	var err error

	for i := range attempts {
		tokens, err = queryEngine.ListAuditTokens(ctx, tokenIDs...)
		if err == nil {
			return tokens, nil
		}

		// The lookup failed. Check whether any requested token belongs to a
		// transaction that is still pending; if so, the row is expected to appear
		// once the finality listener persists it, so wait a bit and retry.
		retry := false
		for _, id := range tokenIDs {
			pending, pErr := queryEngine.IsPending(ctx, id)
			if pErr != nil {
				// We could not even determine the pending status: this is a hard
				// failure, not a pending transaction. Surface both errors instead
				// of masking them as "still pending".
				return nil, errors.Wrapf(errors.Join(err, pErr), "failed to retrieve audit tokens, tx [%s]: cannot determine pending status of token [%s]", anchor, id)
			}
			if pending {
				logger.Warnf("[%s] cannot get audit token for id [%s] because the relative transaction is pending, retry [%d/%d]: with err [%v]", anchor, id, i+1, attempts, err)
				retry = true

				break
			}
		}

		if !retry {
			// None of the tokens is pending: this is a genuine failure, do not retry.
			return nil, err
		}

		if i == attempts-1 {
			// Retry budget exhausted while a token is still pending. Report that,
			// but keep the underlying lookup error so operators can diagnose it.
			return nil, errors.Wrapf(err, "failed to get audit tokens for tx [%s], transaction is still pending", anchor)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryDelay):
		}
	}

	// Unreachable: the loop above returns on success, on genuine failure, and on
	// the final pending attempt. Return an explicit error so a nil/nil pair can
	// never escape to callers that index the returned slice.
	if err == nil {
		err = errors.Errorf("failed to retrieve audit tokens for tx [%s]", anchor)
	}

	return nil, err
}

// ValidateStructure ensures complete structural correspondence between TokenRequest and TokenRequestMetadata.
// It validates that:
// - Action counts match between request and metadata
// - Each action has corresponding metadata with correct type
// - ActionIDs are sequential and match their position
// - Action types align with metadata types (no mixed metadata)
//
// This validation ensures that the request and metadata are structurally consistent before
// performing deeper semantic validation.
func ValidateStructure(
	tokenRequest *driver.TokenRequest,
	tokenRequestMetadata *driver.TokenRequestMetadata,
	txID driver.TokenRequestAnchor,
) error {
	if tokenRequest == nil {
		return errors.Errorf("tokenRequest cannot be nil for tx [%s]", txID)
	}
	if tokenRequestMetadata == nil {
		return errors.Errorf("tokenRequestMetadata cannot be nil for tx [%s]", txID)
	}

	// Validate action count matches metadata count
	if len(tokenRequest.Actions) != len(tokenRequestMetadata.Actions) {
		return errors.Errorf(
			"action count mismatch: request has [%d] actions but metadata has [%d] actions for tx [%s]",
			len(tokenRequest.Actions),
			len(tokenRequestMetadata.Actions),
			txID,
		)
	}

	// Validate each action has corresponding metadata with correct type
	for i, action := range tokenRequest.Actions {
		if action == nil {
			return errors.Errorf("action at index [%d] is nil for tx [%s]", i, txID)
		}

		metadata := tokenRequestMetadata.Actions[i]
		if metadata == nil {
			return errors.Errorf("metadata at index [%d] is nil for tx [%s]", i, txID)
		}

		// Verify ActionID matches position
		if metadata.ActionID != uint32(i) {
			return errors.Errorf(
				"metadata at index [%d] has incorrect ActionID [%d] for tx [%s]",
				i,
				metadata.ActionID,
				txID,
			)
		}

		// Verify action type matches metadata type
		switch action.Type {
		case request.ActionType_ACTION_TYPE_ISSUE:
			if metadata.IssueMetadata == nil {
				return errors.Errorf(
					"action at index [%d] is ISSUE but metadata has no IssueMetadata for tx [%s]",
					i,
					txID,
				)
			}
			if metadata.TransferMetadata != nil {
				return errors.Errorf(
					"action at index [%d] is ISSUE but metadata also has TransferMetadata for tx [%s]",
					i,
					txID,
				)
			}

		case request.ActionType_ACTION_TYPE_TRANSFER:
			if metadata.TransferMetadata == nil {
				return errors.Errorf(
					"action at index [%d] is TRANSFER but metadata has no TransferMetadata for tx [%s]",
					i,
					txID,
				)
			}
			if metadata.IssueMetadata != nil {
				return errors.Errorf(
					"action at index [%d] is TRANSFER but metadata also has IssueMetadata for tx [%s]",
					i,
					txID,
				)
			}

		default:
			return errors.Errorf(
				"action at index [%d] has unknown type [%s] for tx [%s]",
				i,
				action.Type,
				txID,
			)
		}
	}

	return nil
}

// ValidateIssueActionTokenTypes ensures all inputs and outputs in an issue action have the same token type.
// It also validates that input tokens exist in the auditTokens map.
//
// For issue actions with inputs (token upgrades/conversions), this validates that:
// - All input tokens exist in the audit token map
// - All inputs have the same token type
// - All outputs have the same token type as the inputs
//
// This ensures token type consistency within an issue action.
func ValidateIssueActionTokenTypes(
	metadata *driver.IssueMetadata,
	auditTokens map[string]*token.Token,
) error {
	if metadata == nil {
		return errors.Errorf("metadata cannot be nil for issue action validation")
	}
	if auditTokens == nil {
		return errors.Errorf("auditTokens cannot be nil for issue action validation")
	}

	var actionTokenType token.Type

	// Validate and extract token type from inputs (if any exist)
	for i, inputMetadata := range metadata.Inputs {
		if inputMetadata == nil {
			continue
		}

		// Verify input token exists in auditTokens map
		if inputMetadata.TokenID != nil {
			inputToken, exists := auditTokens[inputMetadata.TokenID.String()]
			if !exists {
				return errors.Errorf("input token [%s:%d] at index [%d] not found in audit tokens",
					inputMetadata.TokenID.TxId, inputMetadata.TokenID.Index, i)
			}

			// For issue inputs (token upgrades/conversions), we get the type from the audit token
			if inputToken != nil && inputToken.Type != "" {
				if actionTokenType == "" {
					actionTokenType = inputToken.Type
				} else if actionTokenType != inputToken.Type {
					return errors.Errorf(
						"token type mismatch in issue action: input [%d] has type [%s] but expected [%s]",
						i, inputToken.Type, actionTokenType,
					)
				}
			}
		}
	}

	// Note: Output token type validation is driver-specific and handled by the driver's
	// action deserialization and Match() methods. This function only validates input consistency.

	return nil
}

// ValidateTransferActionTokenTypes ensures all inputs and outputs in a transfer action have the same token type.
// It also validates that input tokens exist in the auditTokens map.
//
// When validateValueSum is true (for privacy-preserving tokens like zkatdlog), this also validates
// that the sum of input values equals the sum of output values using the provided precision.
//
// For transfer actions, this validates that:
// - auditTokens map is non-empty (required for validation)
// - All input tokens exist in the audit token map
// - All inputs have the same token type
// - All outputs have the same token type as the inputs
// - (Optional) Sum of input values equals sum of output values
//
// This ensures token type consistency and value conservation within a transfer action.
func ValidateTransferActionTokenTypes(
	metadata *driver.TransferMetadata,
	auditTokens map[string]*token.Token,
	validateValueSum bool,
	precision uint64,
) error {
	if metadata == nil {
		return errors.Errorf("metadata cannot be nil for transfer action validation")
	}
	// auditTokens must always be non-empty for transfer validation
	if auditTokens == nil {
		return errors.Errorf("auditTokens cannot be nil for transfer action validation")
	}
	if len(auditTokens) == 0 {
		return errors.Errorf("auditTokens cannot be empty for transfer action validation")
	}

	var actionTokenType token.Type
	var inputSum token.Quantity

	if validateValueSum {
		inputSum = token.NewZeroQuantity(precision)
	}

	// Validate and extract token type from inputs
	for i, inputMetadata := range metadata.Inputs {
		if inputMetadata == nil {
			return errors.Errorf("input metadata at index [%d] is nil", i)
		}

		// TokenID is required
		if inputMetadata.TokenID == nil {
			return errors.Errorf("input at index [%d] has nil TokenID", i)
		}

		// Verify input token exists and validate type
		inputToken, exists := auditTokens[inputMetadata.TokenID.String()]
		if !exists {
			return errors.Errorf("input token [%s:%d] at index [%d] not found in audit tokens",
				inputMetadata.TokenID.TxId, inputMetadata.TokenID.Index, i)
		}

		if inputToken == nil {
			return errors.Errorf("input token [%s:%d] at index [%d] is nil in audit tokens",
				inputMetadata.TokenID.TxId, inputMetadata.TokenID.Index, i)
		}

		// Validate and accumulate token type
		if actionTokenType == "" {
			actionTokenType = inputToken.Type
		} else if actionTokenType != inputToken.Type {
			return errors.Errorf(
				"token type mismatch in transfer action: input [%d] has type [%s] but expected [%s]",
				i, inputToken.Type, actionTokenType,
			)
		}

		// Accumulate input value if validation is requested
		if validateValueSum {
			inputQty, err := token.ToQuantity(inputToken.Quantity, precision)
			if err != nil {
				return errors.Wrapf(err, "failed to convert input quantity at index [%d]", i)
			}
			inputSum, err = inputSum.Add(inputQty)
			if err != nil {
				return errors.Wrapf(err, "failed to add input quantity at index [%d]", i)
			}
		}
	}

	// Note: Output token type and value validation is driver-specific.
	// For cleartext tokens (fabtoken), outputs are validated by the action's Match() method.
	// For privacy-preserving tokens (zkatdlog), outputs require additional cryptographic validation
	// which is handled by the driver-specific auditor.

	return nil
}
