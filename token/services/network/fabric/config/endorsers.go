/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// EndorsersMSPIDsKey restricts Token Chaincode endorsement to the peers that belong
	// to the listed MSP IDs.
	EndorsersMSPIDsKey = "services.network.fabric.endorsement.mspIDs"
	// FSCEndorsementKey selects FSC endorsement for a TMS. Its presence switches the
	// endorsers from Token Chaincode peers to FSC nodes, which is why the endorser
	// selection does not apply to it.
	FSCEndorsementKey = "services.network.fabric.fsc_endorsement"
)

// EndorserSelection records which peers this node allows to endorse the Token Chaincode
// invocations it issues. The zero value selects nothing, which leaves Fabric's default
// discovery in charge — the behaviour of a node that configures no selection.
//
// A node that wants to keep endorsement inside its own organization lists its own MSP ID.
// There is deliberately no "my org" shorthand: it would have to be resolved from the
// node's identity at runtime, and an MSP ID is static, known to the operator, and already
// in the channel configuration.
//
// It applies to chaincode endorsement mode only. In FSC endorsement mode the endorsers
// are FSC nodes chosen by the configured policy type, a separate mechanism that ignores
// this selection; ResolveEndorserSelection is the single place that decides this.
type EndorserSelection struct {
	// MSPIDs restricts endorsement to peers in the listed MSP IDs.
	MSPIDs []string
}

// IsSet reports whether the operator expressed any preference. A selection that is not
// set leaves endorser discovery at its default behaviour.
func (s EndorserSelection) IsSet() bool {
	return len(s.MSPIDs) > 0
}

// Explain renders the selection, and the configuration key that set it, for an error
// message.
//
// It exists because the runtime symptom of a selection that no peer satisfies — an MSP ID
// that does not exist, or an organization that hosts no peer able to satisfy the
// chaincode's endorsement policy — is a discovery failure that names no endorser and no
// configuration key. Quoting the restriction in force is what turns that into something
// an operator can act on, since nothing can be checked at load time: the configuration
// layer has no channel, so it cannot know which MSP IDs exist, let alone which of them
// host peers.
func (s EndorserSelection) Explain() string {
	if len(s.MSPIDs) == 0 {
		return "endorsement is not restricted to any organization"
	}

	return "[" + EndorsersMSPIDsKey + "] restricts endorsement to peers in [" + strings.Join(s.MSPIDs, ", ") + "]"
}

// EndorserSelectionConfiguration is the slice of the TMS configuration that
// LoadEndorserSelection and ResolveEndorserSelection read. It is satisfied by
// config.Configuration.
type EndorserSelectionConfiguration interface {
	IsSet(key string) bool
	UnmarshalKey(key string, rawVal any) error
}

// LoadEndorserSelection reads the endorser selection out of a TMS configuration and
// validates it. It does not consider the endorsement mode; callers that act on the
// selection want ResolveEndorserSelection instead.
//
// The MSP IDs themselves cannot be validated here — see EndorserSelection.Explain — so
// what is rejected is only what is locally detectable: a blank entry, and a repeated one.
// Both are operator mistakes rather than expressible intentions, and naming them at
// startup is cheaper than diagnosing the discovery failure they would otherwise cause.
//
// Each entry is trimmed before it is validated, compared and stored. Surrounding
// whitespace in a YAML list is an editing artefact and never part of an MSP ID, and
// trimming at every step keeps the three consistent: validating a trimmed value while
// storing the raw one would send " Org1MSP" to discovery, which matches no MSP and
// produces exactly the endorser-less discovery failure this selection exists to prevent,
// and would also let "Org1MSP " past the duplicate check.
func LoadEndorserSelection(c EndorserSelectionConfiguration) (EndorserSelection, error) {
	var configured []string
	if err := c.UnmarshalKey(EndorsersMSPIDsKey, &configured); err != nil {
		return EndorserSelection{}, errors.WithMessagef(err, "failed to load [%s]", EndorsersMSPIDsKey)
	}

	// Left nil when nothing is configured, so an unset selection stays the zero value.
	var mspIDs []string
	seen := make(map[string]struct{}, len(configured))
	for _, entry := range configured {
		mspID := strings.TrimSpace(entry)
		if len(mspID) == 0 {
			return EndorserSelection{}, errors.Errorf("[%s] contains an empty MSP ID", EndorsersMSPIDsKey)
		}
		if _, duplicate := seen[mspID]; duplicate {
			return EndorserSelection{}, errors.Errorf("[%s] lists MSP ID [%s] more than once", EndorsersMSPIDsKey, mspID)
		}
		seen[mspID] = struct{}{}
		mspIDs = append(mspIDs, mspID)
	}

	return EndorserSelection{MSPIDs: mspIDs}, nil
}

// ResolveEndorserSelection loads the endorser selection out of a TMS configuration and
// reports whether chaincode endorsement — the only mode the selection applies to — is the
// mode that TMS runs in. The returned selection is what the operator configured, whether
// or not it applies, so a caller can report a selection it is about to ignore.
//
// This is the single place that decides the selection does not apply under FSC
// endorsement. There the endorsers are FSC nodes chosen by the configured policy type,
// and the local organization frequently hosts no peer at all, so honouring the keys would
// strand the invocation with no endorser.
//
// The selection is loaded, and therefore validated, before the mode is examined,
// deliberately: contradictory keys are an operator mistake in either mode, and checking
// the mode first would let a broken selection sit unnoticed until the TMS was switched to
// chaincode endorsement.
func ResolveEndorserSelection(c EndorserSelectionConfiguration) (selection EndorserSelection, chaincodeEndorsement bool, err error) {
	selection, err = LoadEndorserSelection(c)
	if err != nil {
		return EndorserSelection{}, false, err
	}

	return selection, !c.IsSet(FSCEndorsementKey), nil
}

// EndorserSelectable is a chaincode invocation builder that accepts an endorser
// restriction. Both the FSC endorse and query views satisfy it, each returning its own
// type, which is why it is parameterised rather than a plain interface.
type EndorserSelectable[T any] interface {
	WithEndorsersByMSPIDs(mspIDs ...string) T
}

// ApplyEndorserSelection restricts v's endorsers according to s and returns the
// resulting builder. An unset selection returns v untouched, leaving Fabric's default
// discovery in charge.
func ApplyEndorserSelection[T EndorserSelectable[T]](v T, s EndorserSelection) T {
	if len(s.MSPIDs) > 0 {
		v = v.WithEndorsersByMSPIDs(s.MSPIDs...)
	}

	return v
}

// restrictedFailure annotates err with the endorser restriction that was in force and the
// guidance that fits the failure.
//
// It is the one place the sentence an operator acts on is written. Both the endorse path
// and the public-parameters query need it, and a reworded fix to a copy in one of them
// would silently leave the other inconsistent.
//
// subject says what failed, for example "endorse for [n1:c1:ns1]"; restriction is normally
// EndorserSelection.Explain, which a caller may extend with a resolution detail.
func restrictedFailure(err error, subject, restriction, guidance string) error {
	return errors.WithMessagef(err, "failed to %s, where %s; %s", subject, restriction, guidance)
}

// NoUsableEndorserError annotates the failure of an invocation that reached Fabric and
// found no endorser the restriction allows — a misspelled MSP ID, or an organization whose
// peers cannot satisfy the chaincode's endorsement policy.
func NoUsableEndorserError(err error, subject, restriction string) error {
	return restrictedFailure(err, subject, restriction,
		"check that the organizations it allows host peers able to satisfy the chaincode endorsement policy")
}
