/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

// ResourceLimits bounds the resources a validator will spend deserializing and validating an
// untrusted token request, before any cryptographic work is performed.
//
// These limits are consensus-relevant: every peer validating the same request must be configured
// with the same values, or otherwise-identical requests could be accepted by one peer and rejected
// by another, breaking endorsement determinism. DefaultResourceLimits returns values identical to
// the ones this package has always enforced; deployments that override them are responsible for
// keeping every validating peer in sync.
type ResourceLimits struct {
	// MaxRequestBytes bounds the raw serialized size of a token request, checked before the
	// protobuf decode so an oversized message is rejected without allocating a parsed structure.
	MaxRequestBytes int
	// MaxActions bounds the number of actions (issue + transfer) in a single token request.
	MaxActions int
	// MaxSignatures bounds the number of request signatures (auditor + action) in a single
	// token request.
	MaxSignatures int
	// MaxSignatureBytes bounds the length of a single auditor or action signature.
	MaxSignatureBytes int
	// MaxActionBytes bounds the length of a single action's serialized (raw) bytes.
	MaxActionBytes int

	// MaxInputs bounds the number of inputs (redeemed or spent tokens) in a single action.
	MaxInputs int
	// MaxOutputs bounds the number of outputs in a single action.
	MaxOutputs int
	// MaxMetadataEntries bounds the number of metadata entries attached to an action.
	MaxMetadataEntries int
	// MaxMetadataKeyBytes bounds the length of a single metadata key.
	MaxMetadataKeyBytes int
	// MaxMetadataValueBytes bounds the length of a single metadata value.
	MaxMetadataValueBytes int

	// MaxProofBytes bounds the length of an action's zero-knowledge proof, checked before the
	// proof is handed to the bulletproof/CSP verifier for deserialization. Drivers without a
	// zero-knowledge proof (e.g. fabtoken) ignore this field.
	MaxProofBytes int
}

// DefaultResourceLimits returns the resource limits enforced when no override is configured.
// These values are identical to the ones this package has always enforced as hardcoded
// constants; changing them here changes the out-of-the-box behavior for every deployment that
// does not explicitly override them.
func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{
		MaxRequestBytes:   256 << 10, // 256 KiB
		MaxActions:        256,
		MaxSignatures:     4096,
		MaxSignatureBytes: 4 << 10,   // 4 KiB
		MaxActionBytes:    256 << 10, // 256 KiB

		MaxInputs:             256,
		MaxOutputs:            256,
		MaxMetadataEntries:    64,
		MaxMetadataKeyBytes:   256,
		MaxMetadataValueBytes: 4 << 10, // 4 KiB

		MaxProofBytes: 128 << 10, // 128 KiB
	}
}

// WithDefaults returns a copy of l where every field that is not a positive value (i.e. zero or
// negative) is replaced by the corresponding field from DefaultResourceLimits. It lets callers
// accept a partially-specified ResourceLimits (e.g. parsed from a config file or environment
// variables where most fields are left unset) without ever silently disabling a limit by leaving
// it at zero, and without a negative value (e.g. a config typo) being misread as "unlimited" by
// the comparisons in token/core/common that enforce these limits.
func (l ResourceLimits) WithDefaults() ResourceLimits {
	d := DefaultResourceLimits()
	if l.MaxRequestBytes <= 0 {
		l.MaxRequestBytes = d.MaxRequestBytes
	}
	if l.MaxActions <= 0 {
		l.MaxActions = d.MaxActions
	}
	if l.MaxSignatures <= 0 {
		l.MaxSignatures = d.MaxSignatures
	}
	if l.MaxSignatureBytes <= 0 {
		l.MaxSignatureBytes = d.MaxSignatureBytes
	}
	if l.MaxActionBytes <= 0 {
		l.MaxActionBytes = d.MaxActionBytes
	}
	if l.MaxInputs <= 0 {
		l.MaxInputs = d.MaxInputs
	}
	if l.MaxOutputs <= 0 {
		l.MaxOutputs = d.MaxOutputs
	}
	if l.MaxMetadataEntries <= 0 {
		l.MaxMetadataEntries = d.MaxMetadataEntries
	}
	if l.MaxMetadataKeyBytes <= 0 {
		l.MaxMetadataKeyBytes = d.MaxMetadataKeyBytes
	}
	if l.MaxMetadataValueBytes <= 0 {
		l.MaxMetadataValueBytes = d.MaxMetadataValueBytes
	}
	if l.MaxProofBytes <= 0 {
		l.MaxProofBytes = d.MaxProofBytes
	}

	return l
}

// ResourceLimitsProvider resolves the ResourceLimits a validator should enforce. Different
// implementations back it with different sources (a configuration file, environment variables,
// a static value for tests), letting the composition root choose the source while every
// downstream consumer depends only on this interface.
type ResourceLimitsProvider interface {
	// ResourceLimits returns the resolved resource limits, with defaults already applied to any
	// field the underlying source left unset.
	ResourceLimits() (ResourceLimits, error)
}

// StaticResourceLimits is a ResourceLimitsProvider that always returns the same, pre-resolved
// ResourceLimits value. It is the implicit provider for callers that do not need a configurable
// source (tests, tools, callers that only need the defaults).
type StaticResourceLimits ResourceLimits

// ResourceLimits returns l as a ResourceLimits value, unchanged.
func (l StaticResourceLimits) ResourceLimits() (ResourceLimits, error) {
	return ResourceLimits(l), nil
}
