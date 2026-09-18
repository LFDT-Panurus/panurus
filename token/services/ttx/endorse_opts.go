/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import "github.com/LFDT-Panurus/panurus/token"

// Parameter keys for endorsement options stored in ServiceOptions.Params
const (
	ParamSkipAuditing                     = "SkipAuditing"
	ParamSkipAuditorSignatureVerification = "SkipAuditorSignatureVerification"
	ParamSkipApproval                     = "SkipApproval"
	ParamSkipDistributeEnv                = "SkipDistributeEnv"
	ParamExternalWalletSigners            = "ExternalWalletSigners"
	ParamPolicySigners                    = "PolicySigners"
	ParamApprovalMetadata                 = "ApprovalMetadata"
)

// EndorsementsOpts is used to configure the CollectEndorsementsView
type EndorsementsOpts struct {
	// SkipAuditing set it to true to skip the auditing phase
	SkipAuditing bool
	// SkipAuditorSignatureVerification set it to true to skip the verification of the auditor signature
	SkipAuditorSignatureVerification bool
	// SkipApproval set it to true to skip the approval phase
	SkipApproval bool
	// SkipDistributeEnv set it to true to skip the distribution phase
	SkipDistributeEnv bool
	// External Signers
	ExternalWalletSigners map[string]ExternalWalletSigner
	// PolicySigners, when non-nil, restricts signature collection for policy
	// identities to the listed component identities.  Component identities not
	// in the list produce a nil slot in the PolicySignature, which is valid for
	// OR branches.  When nil, all component identities are contacted (default).
	PolicySigners []token.Identity
	// ApprovalMetadata carries optional application-level metadata forwarded to approvers.
	// Each driver decides how to deliver this information to the approver backend.
	ApprovalMetadata map[string][]byte
}

func (o *EndorsementsOpts) ExternalWalletSigner(id string) ExternalWalletSigner {
	if o.ExternalWalletSigners == nil {
		return nil
	}

	return o.ExternalWalletSigners[id]
}

// CompileCollectEndorsementsOpts compiles the given list of ServiceOption and returns EndorsementsOpts.
// It extracts endorsement-specific options from the ServiceOptions.Params map.
func CompileCollectEndorsementsOpts(opts ...token.ServiceOption) (*EndorsementsOpts, error) {
	serviceOpts, err := token.CompileServiceOptions(opts...)
	if err != nil {
		return nil, err
	}

	endorseOpts := &EndorsementsOpts{}

	// Extract endorsement-specific options from Params
	if serviceOpts.Params != nil {
		setParam(serviceOpts.Params, ParamSkipAuditing, &endorseOpts.SkipAuditing)
		setParam(serviceOpts.Params, ParamSkipAuditorSignatureVerification, &endorseOpts.SkipAuditorSignatureVerification)
		setParam(serviceOpts.Params, ParamSkipApproval, &endorseOpts.SkipApproval)
		setParam(serviceOpts.Params, ParamSkipDistributeEnv, &endorseOpts.SkipDistributeEnv)
		setParam(serviceOpts.Params, ParamExternalWalletSigners, &endorseOpts.ExternalWalletSigners)
		setParam(serviceOpts.Params, ParamPolicySigners, &endorseOpts.PolicySigners)
		setParam(serviceOpts.Params, ParamApprovalMetadata, &endorseOpts.ApprovalMetadata)
	}

	return endorseOpts, nil
}

// setParam assigns params[key] to *dst when it is present and holds a T, leaving
// *dst untouched otherwise.
func setParam[T any](params map[string]any, key string, dst *T) {
	if v, ok := params[key].(T); ok {
		*dst = v
	}
}

// WithSkipAuditing to skip auditing
func WithSkipAuditing() token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		o.Params[ParamSkipAuditing] = true

		return nil
	}
}

// WithSkipAuditorSignatureVerification to skip auditor signature verification
func WithSkipAuditorSignatureVerification() token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		o.Params[ParamSkipAuditorSignatureVerification] = true

		return nil
	}
}

// WithSkipApproval to skip approval
func WithSkipApproval() token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		o.Params[ParamSkipApproval] = true

		return nil
	}
}

// WithSkipDistributeEnv to skip approval
func WithSkipDistributeEnv() token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		o.Params[ParamSkipDistributeEnv] = true

		return nil
	}
}

// WithPolicySigners restricts signature collection for PolicyIdentity owners to
// the given component identities.  Unlisted components produce nil slots in the
// PolicySignature, satisfying OR branches without contacting the other parties.
func WithPolicySigners(signers ...token.Identity) token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		existing, _ := o.Params[ParamPolicySigners].([]token.Identity)
		o.Params[ParamPolicySigners] = append(existing, signers...)

		return nil
	}
}

func WithExternalWalletSigner(walletID string, ews ExternalWalletSigner) token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		signers, ok := o.Params[ParamExternalWalletSigners].(map[string]ExternalWalletSigner)
		if !ok {
			signers = make(map[string]ExternalWalletSigner)
			o.Params[ParamExternalWalletSigners] = signers
		}
		signers[walletID] = ews

		return nil
	}
}

// WithApprovalMetadata attaches application-level metadata to be forwarded to the approver.
// Each key-value pair is delivered to the approver backend in a driver-specific way
// (e.g. transient data for Fabric FSC endorsement, extra transient entries for chaincode).
func WithApprovalMetadata(metadata map[string][]byte) token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = make(map[string]any)
		}
		o.Params[ParamApprovalMetadata] = metadata

		return nil
	}
}
