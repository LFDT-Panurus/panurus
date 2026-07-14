/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	slices0 "slices"

	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/utils"
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/utils/protos"
	"github.com/LFDT-Panurus/panurus/token/services/utils/slices"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

const (
	// ProtocolV1 demarks the V1 version of the protocol
	// This version uses a secure structured format with explicit Request and Anchor fields,
	// providing robust boundary separation and preventing collision attacks.
	ProtocolV1 = 1

	// MaxAnchorSize defines the maximum allowed size for anchor parameter in bytes.
	// This limit prevents potential DoS attacks through excessive memory allocation.
	MaxAnchorSize = 128 // bytes
)

// Typed errors for protocol validation
var (
	// ErrAnchorEmpty is returned when the anchor parameter is empty in V1 protocol
	ErrAnchorEmpty = errors.New("anchor cannot be empty")

	// ErrAnchorTooLarge is returned when the anchor exceeds MaxAnchorSize in V1 protocol
	ErrAnchorTooLarge = errors.Errorf("anchor size exceeds maximum allowed size of %d bytes", MaxAnchorSize)

	// ErrUnsupportedVersion is returned when an unsupported protocol version is encountered
	ErrUnsupportedVersion = errors.New("unsupported token request version")

	// ErrInvalidVersion is returned when the protocol version is 0 (invalid)
	ErrInvalidVersion = errors.New("invalid token request: protocol version cannot be 0")

	// ErrVersionBelowMinimum is returned when the protocol version is below the configured minimum
	ErrVersionBelowMinimum = errors.New("token request protocol version is below minimum required version")
)

type (
	// TokenRequestAnchor models the anchor of a token request
	TokenRequestAnchor string
)

type ActionSignature struct {
	ActionID  uint32
	Signature []byte
}

func (r *ActionSignature) ToProtos() (*request.ActionSignature, error) {
	return &request.ActionSignature{
		ActionId: r.ActionID,
		Signature: &request.Signature{
			Raw: r.Signature,
		},
	}, nil
}

func (r *ActionSignature) FromProtos(tr *request.ActionSignature) error {
	r.ActionID = tr.ActionId
	if tr.Signature != nil {
		r.Signature = tr.Signature.Raw
	}

	return nil
}

type AuditorSignature struct {
	Identity  Identity
	Signature []byte
}

func (r *AuditorSignature) ToProtos() (*request.AuditorSignature, error) {
	return &request.AuditorSignature{
		Identity: &protosv1.Identity{
			Raw: r.Identity,
		},
		Signature: &request.Signature{
			Raw: r.Signature,
		},
	}, nil
}

func (r *AuditorSignature) FromProtos(tr *request.AuditorSignature) error {
	if tr.Identity != nil {
		r.Identity = tr.Identity.Raw
	}
	if tr.Signature != nil {
		r.Signature = tr.Signature.Raw
	}

	return nil
}

type RequestSignature struct {
	Action  *ActionSignature
	Auditor *AuditorSignature
}

func (r *RequestSignature) ToProtos() (*request.RequestSignature, error) {
	if r == nil {
		return nil, errors.New("nil request signature")
	}
	switch {
	case r.Action != nil && r.Auditor != nil:
		return nil, errors.New("request signature cannot contain both action and auditor signatures")
	case r.Action != nil:
		actionSignature, err := r.Action.ToProtos()
		if err != nil {
			return nil, errors.Wrap(err, "failed converting action signature")
		}

		return &request.RequestSignature{
			Signature: &request.RequestSignature_ActionSignature{
				ActionSignature: actionSignature,
			},
		}, nil
	case r.Auditor != nil:
		auditorSignature, err := r.Auditor.ToProtos()
		if err != nil {
			return nil, errors.Wrap(err, "failed converting auditor signature")
		}

		return &request.RequestSignature{
			Signature: &request.RequestSignature_AuditorSignature{
				AuditorSignature: auditorSignature,
			},
		}, nil
	default:
		return nil, errors.New("request signature must contain either an action or auditor signature")
	}
}

func (r *RequestSignature) FromProtos(tr *request.RequestSignature) error {
	if tr == nil {
		return errors.New("nil request signature")
	}
	if actionSignature := tr.GetActionSignature(); actionSignature != nil {
		r.Action = &ActionSignature{}

		return r.Action.FromProtos(actionSignature)
	}
	if auditorSignature := tr.GetAuditorSignature(); auditorSignature != nil {
		r.Auditor = &AuditorSignature{}

		return r.Auditor.FromProtos(auditorSignature)
	}

	return errors.New("request signature type not recognized")
}

// TypedAction represents a single token action with its type and raw bytes
type TypedAction struct {
	Type request.ActionType // ACTION_TYPE_ISSUE or ACTION_TYPE_TRANSFER
	Raw  []byte             // Serialized action data
}

// TokenRequest represents a collection of token actions (issuance and transfer).
// Each action within the request is logically independent, though they are processed together.
// A TokenRequest also includes the signatures (witnesses) required to authorize its actions.
type TokenRequest struct {
	// Version specifies the protocol version for this token request.
	// Defaults to ProtocolV1 (structured format) for new requests.
	// The asn1 tag with "-" means this field is never included in ASN.1 marshaling,
	// ensuring consistent signature verification.
	Version    uint32
	Actions    []*TypedAction // Unified slice of all actions
	Signatures []*RequestSignature
}

// GetIssues returns all issue actions from the unified Actions slice
func (r *TokenRequest) GetIssues() [][]byte {
	var issues [][]byte
	for _, action := range r.Actions {
		if action.Type == request.ActionType_ACTION_TYPE_ISSUE {
			issues = append(issues, action.Raw)
		}
	}

	return issues
}

// GetTransfers returns all transfer actions from the unified Actions slice
func (r *TokenRequest) GetTransfers() [][]byte {
	var transfers [][]byte
	for _, action := range r.Actions {
		if action.Type == request.ActionType_ACTION_TYPE_TRANSFER {
			transfers = append(transfers, action.Raw)
		}
	}

	return transfers
}

// NumIssues returns the count of issue actions
func (r *TokenRequest) NumIssues() int {
	count := 0
	for _, action := range r.Actions {
		if action.Type == request.ActionType_ACTION_TYPE_ISSUE {
			count++
		}
	}

	return count
}

// NumTransfers returns the count of transfer actions
func (r *TokenRequest) NumTransfers() int {
	count := 0
	for _, action := range r.Actions {
		if action.Type == request.ActionType_ACTION_TYPE_TRANSFER {
			count++
		}
	}

	return count
}

func (r *TokenRequest) Bytes() ([]byte, error) {
	tr, err := r.ToProtos()
	if err != nil {
		return nil, err
	}

	return proto.Marshal(tr)
}

func (r *TokenRequest) FromBytes(raw []byte) error {
	tr := &request.TokenRequest{}
	err := proto.Unmarshal(raw, tr)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling token request")
	}

	return r.FromProtos(tr)
}

func (r *TokenRequest) ToProtos() (*request.TokenRequest, error) {
	// Convert TypedActions to protobuf Actions
	actions := make([]*request.Action, 0, len(r.Actions))
	for _, action := range r.Actions {
		if action == nil {
			return nil, errors.New("nil action found")
		}
		actions = append(actions, &request.Action{
			Action: &request.Action_TypedAction{
				TypedAction: &request.TypedAction{
					Type: action.Type,
					Raw:  action.Raw,
				},
			},
		})
	}

	signatures, err := protos.ToProtosSlice[request.RequestSignature, *RequestSignature](r.Signatures)
	if err != nil {
		return nil, errors.Wrap(err, "failed converting request signatures")
	}

	// Use stored version, defaulting to V1 (structured format) for new requests
	version := r.Version
	if version == 0 {
		version = uint32(ProtocolV1)
	}

	return &request.TokenRequest{
		Version:    version,
		Actions:    actions,
		Signatures: signatures,
	}, nil
}

func (r *TokenRequest) FromProtos(tr *request.TokenRequest) error {
	// Validate version - only ProtocolV1 (structured format) is supported
	if tr.Version != uint32(ProtocolV1) {
		return errors.Wrapf(ErrUnsupportedVersion, "expected [%d], got [%d]", ProtocolV1, tr.Version)
	}

	// Store the version from the protobuf
	r.Version = tr.Version

	// Convert protobuf Actions to TypedActions
	r.Actions = make([]*TypedAction, 0, len(tr.Actions))
	for _, action := range tr.Actions {
		if action == nil {
			return errors.New("nil action found")
		}

		// Handle the Action oneof - currently only TypedAction is supported
		typedAction := action.GetTypedAction()
		if typedAction == nil {
			// HashedAction is not yet supported in this implementation
			return errors.New("only TypedAction is currently supported")
		}

		// Validate that action type is explicitly set (not UNSPECIFIED)
		if typedAction.Type == request.ActionType_ACTION_TYPE_UNSPECIFIED {
			return errors.New("action type must be explicitly specified (ACTION_TYPE_UNSPECIFIED is not allowed)")
		}

		// Validate action type is known
		switch typedAction.Type {
		case request.ActionType_ACTION_TYPE_ISSUE, request.ActionType_ACTION_TYPE_TRANSFER:
			// Valid types
		default:
			return errors.Errorf("unknown action type [%s]", typedAction.Type)
		}

		r.Actions = append(r.Actions, &TypedAction{
			Type: typedAction.Type,
			Raw:  typedAction.Raw,
		})
	}

	for _, signature := range tr.Signatures {
		if signature == nil {
			return errors.New("nil signature found")
		}
		requestSignature := &RequestSignature{}
		if err := requestSignature.FromProtos(signature); err != nil {
			return errors.Wrap(err, "failed converting request signature")
		}
		switch {
		case requestSignature.Action != nil && len(requestSignature.Action.Signature) == 0:
			return errors.New("nil action signature found")
		case requestSignature.Auditor != nil && len(requestSignature.Auditor.Signature) == 0:
			return errors.New("nil auditor signature found")
		}
		r.Signatures = append(r.Signatures, requestSignature)
	}

	// Validate the structural integrity of the token request
	if err := r.Validate(); err != nil {
		return errors.Wrap(err, "token request validation failed")
	}

	return nil
}

// Validate checks the structural validity of the TokenRequest.
// It ensures that all required fields are present and non-empty.
// This method should be called after unmarshalling to verify the request is well-formed.
//
// Special case: Empty requests (nil Actions or empty Actions slice) are allowed
// to support test scenarios where requests are created but not yet populated.
// This is necessary because ToProtos() sets Version=ProtocolV1 even for empty requests.
//
// Returns an error if:
//   - Any action in Actions is nil
//   - Any action has empty Raw bytes
//   - Any signature in Signatures is nil
//   - Any signature has empty signature bytes
func (r *TokenRequest) Validate() error {
	// If we have actions, validate them

	for i, action := range r.Actions {
		if action == nil {
			return errors.Errorf("action at index %d is nil", i)
		}
		if len(action.Raw) == 0 {
			return errors.Errorf("action at index %d has empty Raw bytes", i)
		}
	}

	// Validate signatures (if present)
	for i, sig := range r.Signatures {
		if sig == nil {
			return errors.Errorf("signature at index %d is nil", i)
		}

		// Check that signature has either Action or Auditor signature with non-empty bytes
		switch {
		case sig.Action != nil:
			if len(sig.Action.Signature) == 0 {
				return errors.Errorf("action signature at index %d has empty signature bytes", i)
			}
		case sig.Auditor != nil:
			if len(sig.Auditor.Signature) == 0 {
				return errors.Errorf("auditor signature at index %d has empty signature bytes", i)
			}
		default:
			return errors.Errorf("signature at index %d has neither action nor auditor signature", i)
		}
	}

	return nil
}

// MarshalToMessageToSign creates a canonical byte representation of the TokenRequest
// for signature generation using the structured ASN.1 format.
//
// The ProtocolV1 format uses a secure structured ASN.1 format with separate Request
// and Anchor fields, providing robust boundary separation and preventing collision attacks.
//
// Parameters:
//   - anchor: A unique identifier (e.g., transaction ID) that binds this signature
//     to a specific context. Must be non-empty and within size limits.
//
// Security considerations:
//   - The anchor MUST be unique per transaction to prevent signature reuse
//   - Signatures are not included in the marshaled data to avoid circular dependencies
//   - Structured format provides strong security guarantees against collision attacks
//
// Returns the message bytes to be signed, or an error if marshaling fails.
func (r *TokenRequest) MarshalToMessageToSign(anchor []byte) ([]byte, error) {
	return r.marshalToMessageToSignV1(anchor)
}

// marshalToMessageToSignV1 implements the V1 protocol signature message construction
// using a secure structured ASN.1 format that prevents hash collision vulnerabilities.
//
// Security features:
//   - Structured ASN.1 format with explicit Request and Anchor fields
//   - Clear boundary separation prevents collision attacks
//   - Input validation ensures anchor meets security requirements
//   - Hex-encoded error messages prevent sensitive data exposure
//
// This implementation uses an optimized fast marshaller that avoids reflection overhead
// while maintaining full ASN.1 compatibility.
func (r *TokenRequest) marshalToMessageToSignV1(anchor []byte) ([]byte, error) {
	// Input validation with typed errors
	if len(anchor) == 0 {
		return nil, ErrAnchorEmpty
	}
	if len(anchor) > MaxAnchorSize {
		return nil, ErrAnchorTooLarge
	}

	// Marshal the request data using fast marshaller with typed actions in order
	requestBytes, err := fastMarshalTokenRequestForSigning(r.Actions)
	if err != nil {
		return nil, errors.Wrapf(err, "audit of tx [%x] failed: error marshal token request for signature", anchor)
	}

	// Marshal the complete structured message using fast marshaller
	msgBytes, err := fastMarshalSignatureMessageV1(requestBytes, anchor)
	if err != nil {
		return nil, errors.Wrapf(err, "audit of tx [%x] failed: error marshal signature message", anchor)
	}

	return msgBytes, nil
}

type AuditableIdentity struct {
	Identity  Identity
	AuditInfo []byte
}

func (a *AuditableIdentity) ToProtos() (*request.AuditableIdentity, error) {
	return &request.AuditableIdentity{
		Identity: &protosv1.Identity{
			Raw: a.Identity,
		},
		AuditInfo: a.AuditInfo,
	}, nil
}

func (a *AuditableIdentity) FromProtos(auditableIdentity *request.AuditableIdentity) error {
	a.Identity = ToIdentity(auditableIdentity.Identity)
	a.AuditInfo = auditableIdentity.AuditInfo

	return nil
}

type IssueInputMetadata struct {
	TokenID *token.ID
}

func (i *IssueInputMetadata) ToProtos() (*request.IssueInputMetadata, error) {
	tokenID, err := utils.ToTokenID(i.TokenID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal token ID [%s]", i.TokenID)
	}

	return &request.IssueInputMetadata{
		TokenId: tokenID,
	}, nil
}

func (i *IssueInputMetadata) FromProtos(issueInputMetadata *request.IssueInputMetadata) error {
	i.TokenID = ToTokenID(issueInputMetadata.TokenId)

	return nil
}

// IssueOutputMetadata is the metadata of an output in an issue action
type IssueOutputMetadata struct {
	OutputMetadata []byte
	// OutputAuditInfo, for each output owner we have audit info
	OutputAuditInfo []byte
	// Receivers, for each output we have a receiver
	Receivers []*AuditableIdentity
}

func (i *IssueOutputMetadata) ToProtos() (*request.OutputMetadata, error) {
	receivers, err := protos.ToProtosSlice[request.AuditableIdentity, *AuditableIdentity](i.Receivers)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling receivers")
	}

	return &request.OutputMetadata{
		Metadata:  i.OutputMetadata,
		AuditInfo: i.OutputAuditInfo,
		Receivers: receivers,
	}, nil
}

func (i *IssueOutputMetadata) FromProtos(outputsMetadata *request.OutputMetadata) error {
	if outputsMetadata == nil {
		return nil
	}
	i.OutputMetadata = outputsMetadata.Metadata
	i.OutputAuditInfo = outputsMetadata.AuditInfo
	i.Receivers = slices.GenericSliceOfPointers[AuditableIdentity](len(outputsMetadata.Receivers))
	if err := protos.FromProtosSlice(outputsMetadata.Receivers, i.Receivers); err != nil {
		return errors.Wrap(err, "failed unmarshalling receivers metadata")
	}

	return nil
}

func (i *IssueOutputMetadata) RecipientAt(index int) *AuditableIdentity {
	if index < 0 || index >= len(i.Receivers) {
		return nil
	}

	return i.Receivers[index]
}

// IssueMetadata contains the metadata of an issue action.
// In more details, there is an issuer and a list of outputs.
// For each output, there is a token info and a list of receivers with their audit info to recover their enrollment ID.
type IssueMetadata struct {
	// Issuer is the identity of the issuer
	Issuer  AuditableIdentity
	Inputs  []*IssueInputMetadata
	Outputs []*IssueOutputMetadata
	// ExtraSigners is the list of extra identities that are not part of the issue action per se
	// but needs to sign the request
	ExtraSigners []AuditableIdentity
}

func (i *IssueMetadata) ToProtos() (*request.IssueMetadata, error) {
	issuer, err := i.Issuer.ToProtos()
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling issuer [%v]", i.Issuer)
	}
	inputs, err := protos.ToProtosSlice(i.Inputs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling inputs")
	}
	outputs, err := protos.ToProtosSlice(i.Outputs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling outputs")
	}
	extraSigners, err := ToProtoAuditableIdentitySliceFromAuditable(i.ExtraSigners)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling extra signers")
	}

	return &request.IssueMetadata{
		Issuer:       issuer,
		Inputs:       inputs,
		Outputs:      outputs,
		ExtraSigners: extraSigners,
	}, nil
}

func (i *IssueMetadata) FromProtos(issueMetadata *request.IssueMetadata) error {
	issuer := &AuditableIdentity{}
	if err := issuer.FromProtos(issueMetadata.Issuer); err != nil {
		return errors.Wrapf(err, "failed unmarshalling issuer [%v]", issueMetadata.Issuer)
	}
	i.Issuer = *issuer
	i.Inputs = slices.GenericSliceOfPointers[IssueInputMetadata](len(issueMetadata.Inputs))
	err := protos.FromProtosSlice[request.IssueInputMetadata, *IssueInputMetadata](issueMetadata.Inputs, i.Inputs)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling input metadata")
	}
	i.Outputs = slices.GenericSliceOfPointers[IssueOutputMetadata](len(issueMetadata.Outputs))
	err = protos.FromProtosSlice(issueMetadata.Outputs, i.Outputs)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling output metadata")
	}
	i.ExtraSigners, err = FromProtoAuditableIdentitySliceToAuditable(issueMetadata.ExtraSigners)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling extra signers")
	}

	return nil
}

func (i *IssueMetadata) Receivers() []Identity {
	res := make([]Identity, 0, len(i.Outputs))
	for _, output := range i.Outputs {
		for _, receiver := range output.Receivers {
			if receiver == nil {
				res = append(res, nil)
			} else {
				res = append(res, receiver.Identity)
			}
		}
	}

	return res
}

// Match verifies that the given action matches this metadata.
// It performs a deep check of inputs, outputs, extra signers, and the issuer identity.
func (i *IssueMetadata) Match(action IssueAction) error {
	// Validate the action's structure.
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "failed validating issue action")
	}

	// Check that the number of inputs matches.
	if len(i.Inputs) != action.NumInputs() {
		return errors.Errorf("expected [%d] inputs but got [%d]", len(i.Inputs), action.NumInputs())
	}

	// Check that the number of outputs matches.
	if len(i.Outputs) != action.NumOutputs() {
		return errors.Errorf("expected [%d] outputs but got [%d]", len(i.Outputs), action.NumOutputs())
	}

	// Check that the extra signers match.
	// The action returns []Identity, and metadata has []AuditableIdentity
	extraSigners := action.ExtraSigners()
	if len(i.ExtraSigners) != len(extraSigners) {
		return errors.Errorf("expected [%d] extra signers but got [%d]", len(extraSigners), len(i.ExtraSigners))
	}
	for _, signer := range extraSigners {
		found := slices0.ContainsFunc(i.ExtraSigners, func(a AuditableIdentity) bool {
			return signer.Equal(a.Identity)
		})
		if !found {
			return errors.Errorf("extra signer [%s] from action not found in metadata", signer)
		}
	}

	// Check that the issuer identity matches.
	// The metadata has Issuer.Identity (extracted from AuditableIdentity)
	if !i.Issuer.Identity.Equal(action.GetIssuer()) {
		return errors.Errorf("expected issuer [%s] but got [%s]", i.Issuer.Identity, action.GetIssuer())
	}

	return nil
}

// IsOutputAbsent returns true if the j-th output's metadata is absent (e.g., filtered out).
func (i *IssueMetadata) IsOutputAbsent(j int) bool {
	if j < 0 || j >= len(i.Outputs) {
		return true
	}

	return i.Outputs[j] == nil
}

type TransferInputMetadata struct {
	TokenID *token.ID
	Senders []*AuditableIdentity
}

func (t *TransferInputMetadata) ToProtos() (*request.TransferInputMetadata, error) {
	senders, err := protos.ToProtosSlice(t.Senders)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling senders")
	}
	tokenID, err := utils.ToTokenID(t.TokenID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling tokenID")
	}

	return &request.TransferInputMetadata{
		TokenId: tokenID,
		Senders: senders,
	}, nil
}

func (t *TransferInputMetadata) FromProtos(transferInputMetadata *request.TransferInputMetadata) error {
	if transferInputMetadata == nil {
		return nil
	}
	t.TokenID = ToTokenID(transferInputMetadata.TokenId)
	t.Senders = slices.GenericSliceOfPointers[AuditableIdentity](len(transferInputMetadata.Senders))
	if err := protos.FromProtosSlice(transferInputMetadata.Senders, t.Senders); err != nil {
		return errors.Wrap(err, "failed unmarshalling token metadata")
	}

	return nil
}

// TransferOutputMetadata is the metadata of an output in a transfer action
type TransferOutputMetadata struct {
	OutputMetadata []byte
	// OutputAuditInfo, for each output owner we have audit info
	OutputAuditInfo []byte
	// Receivers is the list of receivers
	Receivers []*AuditableIdentity
}

func (t *TransferOutputMetadata) ToProtos() (*request.OutputMetadata, error) {
	receivers, err := protos.ToProtosSlice(t.Receivers)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling receivers")
	}

	return &request.OutputMetadata{
		Metadata:  t.OutputMetadata,
		AuditInfo: t.OutputAuditInfo,
		Receivers: receivers,
	}, nil
}

func (t *TransferOutputMetadata) FromProtos(transferOutputMetadata *request.OutputMetadata) error {
	if transferOutputMetadata == nil {
		return nil
	}
	t.OutputMetadata = transferOutputMetadata.Metadata
	t.OutputAuditInfo = transferOutputMetadata.AuditInfo
	t.Receivers = slices.GenericSliceOfPointers[AuditableIdentity](len(transferOutputMetadata.Receivers))
	if err := protos.FromProtosSlice(transferOutputMetadata.Receivers, t.Receivers); err != nil {
		return errors.Wrap(err, "failed unmarshalling receivers metadata")
	}

	return nil
}

func (t *TransferOutputMetadata) RecipientAt(index int) *AuditableIdentity {
	if index < 0 || index >= len(t.Receivers) {
		return nil
	}

	return t.Receivers[index]
}

// TransferMetadata contains the metadata of a transfer action
// For each TokenID there is a sender with its audit info to recover its enrollment ID,
// For each Output there is:
// - A OutputMetadata entry to de-obfuscate the output;
// - A Receiver identity;
// - A ReceiverAuditInfo entry to recover the enrollment ID of the receiver
// - A Flag to indicate if the receiver is a sender in this very same action
type TransferMetadata struct {
	Inputs  []*TransferInputMetadata
	Outputs []*TransferOutputMetadata
	// ExtraSigners is the list of extra identities that are not part of the transfer action per se
	// but needs to sign the request
	ExtraSigners []AuditableIdentity
	// Issuer contains the identity of the issuer to sign the transfer action
	Issuer AuditableIdentity
}

// TokenIDAt returns the TokenID at the given index.
// It returns nil if the index is out of bounds.
func (t *TransferMetadata) TokenIDAt(index int) *token.ID {
	if index < 0 || index >= len(t.Inputs) {
		return nil
	}

	return t.Inputs[index].TokenID
}

func (t *TransferMetadata) ToProtos() (*request.TransferMetadata, error) {
	inputs, err := protos.ToProtosSlice[request.TransferInputMetadata, *TransferInputMetadata](t.Inputs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling inputs")
	}
	outputs, err := protos.ToProtosSlice(t.Outputs)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling outputs")
	}
	extraSigners, err := ToProtoAuditableIdentitySliceFromAuditable(t.ExtraSigners)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling extra signers")
	}

	var issuer *request.AuditableIdentity
	if t.Issuer.Identity != nil {
		issuer, err = t.Issuer.ToProtos()
		if err != nil {
			return nil, errors.Wrapf(err, "failed marshalling issuer")
		}
	}

	return &request.TransferMetadata{
		Inputs:       inputs,
		Outputs:      outputs,
		ExtraSigners: extraSigners,
		Issuer:       issuer,
	}, nil
}

func (t *TransferMetadata) FromProtos(transferMetadata *request.TransferMetadata) error {
	t.Inputs = slices.GenericSliceOfPointers[TransferInputMetadata](len(transferMetadata.Inputs))
	if err := protos.FromProtosSlice(transferMetadata.Inputs, t.Inputs); err != nil {
		return errors.Wrap(err, "failed unmarshalling inputs")
	}
	t.Outputs = slices.GenericSliceOfPointers[TransferOutputMetadata](len(transferMetadata.Outputs))
	if err := protos.FromProtosSlice(transferMetadata.Outputs, t.Outputs); err != nil {
		return errors.Wrap(err, "failed unmarshalling outputs")
	}
	var err error
	t.ExtraSigners, err = FromProtoAuditableIdentitySliceToAuditable(transferMetadata.ExtraSigners)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling extra signers")
	}

	t.Issuer = AuditableIdentity{}
	if transferMetadata.Issuer != nil {
		if err := t.Issuer.FromProtos(transferMetadata.Issuer); err != nil {
			return errors.Wrap(err, "failed unmarshalling issuer")
		}
	}

	return nil
}

func (t *TransferMetadata) Receivers() []Identity {
	res := make([]Identity, 0, len(t.Outputs))
	for _, output := range t.Outputs {
		for _, receiver := range output.Receivers {
			if receiver == nil {
				res = append(res, nil)
			} else {
				res = append(res, receiver.Identity)
			}
		}
	}

	return res
}

func (t *TransferMetadata) Senders() []Identity {
	res := make([]Identity, 0, len(t.Inputs))
	for _, output := range t.Inputs {
		for _, sender := range output.Senders {
			if sender == nil {
				res = append(res, nil)
			} else {
				res = append(res, sender.Identity)
			}
		}
	}

	return res
}

func (t *TransferMetadata) TokenIDs() []*token.ID {
	res := make([]*token.ID, 0, len(t.Inputs))
	for _, input := range t.Inputs {
		if input == nil {
			continue
		}
		res = append(res, input.TokenID)
	}

	return res
}

// Match verifies that the given action matches this metadata.
// It performs a deep check of inputs, outputs, extra signers, and the issuer identity (if present).
func (t *TransferMetadata) Match(action TransferAction) error {
	// Validate the action's structure.
	if err := action.Validate(); err != nil {
		return errors.Wrap(err, "failed validating transfer action")
	}

	// Check that the number of inputs matches.
	if len(t.Inputs) != action.NumInputs() {
		return errors.Errorf("expected [%d] inputs but got [%d]", len(t.Inputs), action.NumInputs())
	}

	// Check that the number of outputs matches.
	if len(t.Outputs) != action.NumOutputs() {
		return errors.Errorf("expected [%d] outputs but got [%d]", len(t.Outputs), action.NumOutputs())
	}

	// Check that the extra signers match.
	// The action returns []Identity, and metadata has []AuditableIdentity
	extraSigners := action.ExtraSigners()
	if len(t.ExtraSigners) != len(extraSigners) {
		return errors.Errorf("expected [%d] extra signers but got [%d]", len(t.ExtraSigners), len(extraSigners))
	}
	for i, signer := range extraSigners {
		if !signer.Equal(t.ExtraSigners[i].Identity) {
			return errors.Errorf("expected extra signer [%s] but got [%s]", t.ExtraSigners[i].Identity, signer)
		}
	}

	// Check that the issuer identity matches, if present in the metadata.
	// The metadata has Issuer as AuditableIdentity
	if !t.Issuer.Identity.Equal(action.GetIssuer()) {
		return errors.Errorf("expected issuer [%s] but got [%s]", t.Issuer.Identity, action.GetIssuer().Bytes())
	}

	return nil
}

// IsOutputAbsent returns true if the j-th output's metadata is absent (e.g., filtered out).
func (t *TransferMetadata) IsOutputAbsent(j int) bool {
	if j < 0 || j >= len(t.Outputs) {
		return true
	}

	return t.Outputs[j] == nil
}

// IsInputAbsent returns true if the j-th input's metadata is absent (e.g., filtered out).
func (t *TransferMetadata) IsInputAbsent(j int) bool {
	if j < 0 || j >= len(t.Inputs) {
		return true
	}

	return t.Inputs[j] == nil || len(t.Inputs[j].Senders) == 0
}

// ActionMetadataEntry represents metadata for a single action with its ID and type-discriminated metadata
type ActionMetadataEntry struct {
	ActionID         uint32            // Position in Actions slice
	IssueMetadata    *IssueMetadata    // Non-nil if this is an issue action
	TransferMetadata *TransferMetadata // Non-nil if this is a transfer action
}

// TokenRequestMetadata contains the supplementary information needed to process and interpret a TokenRequest.
// It includes metadata for each issuance and transfer action, enabling de-obfuscation and identity recovery.
type TokenRequestMetadata struct {
	// Actions contains metadata for all actions in the corresponding TokenRequest.
	Actions []*ActionMetadataEntry
	// Application allows for attaching arbitrary application-level metadata to the token request.
	Application map[string][]byte
}

// GetIssueMetadata returns issue metadata at the given index (among issues only)
func (m *TokenRequestMetadata) GetIssueMetadata(index int) (*IssueMetadata, error) {
	issueCount := 0
	for _, action := range m.Actions {
		if action.IssueMetadata != nil {
			if issueCount == index {
				return action.IssueMetadata, nil
			}
			issueCount++
		}
	}

	return nil, errors.Errorf("issue metadata at index [%d] out of range [0:%d]", index, issueCount)
}

// GetTransferMetadata returns transfer metadata at the given index (among transfers only)
func (m *TokenRequestMetadata) GetTransferMetadata(index int) (*TransferMetadata, error) {
	transferCount := 0
	for _, action := range m.Actions {
		if action.TransferMetadata != nil {
			if transferCount == index {
				return action.TransferMetadata, nil
			}
			transferCount++
		}
	}

	return nil, errors.Errorf("transfer metadata at index [%d] out of range [0:%d]", index, transferCount)
}

// NumIssues returns the count of issue metadata entries
func (m *TokenRequestMetadata) NumIssues() int {
	count := 0
	for _, action := range m.Actions {
		if action.IssueMetadata != nil {
			count++
		}
	}

	return count
}

// NumTransfers returns the count of transfer metadata entries
func (m *TokenRequestMetadata) NumTransfers() int {
	count := 0
	for _, action := range m.Actions {
		if action.TransferMetadata != nil {
			count++
		}
	}

	return count
}

func (m *TokenRequestMetadata) Bytes() ([]byte, error) {
	trm, err := m.ToProtos()
	if err != nil {
		return nil, err
	}

	return proto.Marshal(trm)
}

func (m *TokenRequestMetadata) FromBytes(raw []byte) error {
	trm := &request.TokenRequestMetadata{}
	err := proto.Unmarshal(raw, trm)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal token request metadata")
	}

	return m.FromProtos(trm)
}

func (m *TokenRequestMetadata) ToProtos() (*request.TokenRequestMetadata, error) {
	trm := &request.TokenRequestMetadata{
		Version:             ProtocolV1,
		Metadata:            nil,
		ApplicationMetadata: m.Application,
	}
	trm.Metadata = make([]*request.ActionMetadata, 0, len(m.Actions))

	for _, action := range m.Actions {
		if action == nil {
			return nil, errors.Errorf("nil action metadata found")
		}

		if action.IssueMetadata != nil {
			metaProto, err := action.IssueMetadata.ToProtos()
			if err != nil {
				return nil, errors.Wrapf(err, "failed marshalling issue metadata")
			}
			trm.Metadata = append(trm.Metadata, &request.ActionMetadata{
				ActionId: action.ActionID,
				Metadata: &request.ActionMetadata_IssueMetadata{
					IssueMetadata: metaProto,
				},
			})
		} else if action.TransferMetadata != nil {
			metaProto, err := action.TransferMetadata.ToProtos()
			if err != nil {
				return nil, errors.Wrapf(err, "failed marshalling transfer metadata")
			}
			trm.Metadata = append(trm.Metadata, &request.ActionMetadata{
				ActionId: action.ActionID,
				Metadata: &request.ActionMetadata_TransferMetadata{
					TransferMetadata: metaProto,
				},
			})
		} else {
			return nil, errors.Errorf("action metadata must have either IssueMetadata or TransferMetadata, but it is nil")
		}
	}

	return trm, nil
}

func (m *TokenRequestMetadata) FromProtos(trm *request.TokenRequestMetadata) error {
	// assert version
	if trm.Version != ProtocolV1 {
		return errors.Errorf("invalid token request metadata version, expected [%d], got [%d]", ProtocolV1, trm.Version)
	}

	m.Application = trm.ApplicationMetadata
	m.Actions = make([]*ActionMetadataEntry, 0, len(trm.Metadata))

	for _, meta := range trm.Metadata {
		entry := &ActionMetadataEntry{
			ActionID: meta.ActionId,
		}

		im := meta.GetIssueMetadata()
		if im != nil {
			issueMetadata := &IssueMetadata{}
			if err := issueMetadata.FromProtos(im); err != nil {
				return errors.Wrapf(err, "failed unmarshalling issue metadata")
			}
			entry.IssueMetadata = issueMetadata
			m.Actions = append(m.Actions, entry)

			continue
		}

		tm := meta.GetTransferMetadata()
		if tm != nil {
			transferMetadata := &TransferMetadata{}
			if err := transferMetadata.FromProtos(tm); err != nil {
				return errors.Wrapf(err, "failed unmarshalling transfer metadata")
			}
			entry.TransferMetadata = transferMetadata
			m.Actions = append(m.Actions, entry)

			continue
		}

		return errors.Errorf("failed unmarshalling metadata, type not recognized")
	}

	return nil
}
