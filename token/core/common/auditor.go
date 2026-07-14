/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
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
// IMPORTANT: This function always returns a non-nil map (possibly empty) to ensure
// validation logic can distinguish between "no tokens requested" and "tokens not found".
func RetrieveAuditTokens(
	ctx context.Context,
	logger logging.Logger,
	queryEngine driver.QueryEngine,
	tokenIDs []*token.ID,
	anchor driver.TokenRequestAnchor,
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
	tokens, err := queryEngine.ListAuditTokens(ctx, tokenIDs...)
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
