/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"math"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/tokens/core/fabtoken"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrInvalidUpgradeInput is returned when an issue input is not a well-formed,
	// upgradable fabtoken output.
	ErrInvalidUpgradeInput = errors.New("invalid upgrade input")
	// ErrDuplicateUpgradeInput is returned when the same ledger token is listed
	// more than once among the inputs of an issue action.
	ErrDuplicateUpgradeInput = errors.New("duplicate upgrade input")
)

// IssueActionInput is a legacy ledger token redeemed by an upgrade issuance
// (burn-and-re-issue). Token holds the serialized fabtoken or ZKAT-DLOG output
// exactly as recorded on the ledger: the ledger translator derives the key it
// checks and deletes from these bytes, so they must never be re-encoded.
type IssueActionInput struct {
	// ID is the ledger identifier of the redeemed token.
	ID token2.ID
	// Token is the serialized fabtoken or ZKAT-DLOG output, as stored on the ledger.
	Token []byte
	// Migration proves the opening of a ZKAT-DLOG Token. It is set if and only
	// if Token is a ZKAT-DLOG output.
	Migration *MigrationDescription
}

// UpgradeInput is the cleartext content of a redeemed fabtoken output.
type UpgradeInput struct {
	// Owner is the identity that owns the fabtoken and must consent to its upgrade.
	Owner driver.Identity
	// Type is the token type.
	Type token2.Type
	// Value is the token quantity, parsed with the zkatsnark precision.
	Value uint64
}

// IsFabtokenOutput reports whether raw is tagged as a fabtoken output. It does
// not check that the content is well-formed (see ParseFabtokenOutput).
func IsFabtokenOutput(raw []byte) bool {
	typed, err := tokens.UnmarshalTypedToken(raw)

	return err == nil && typed.Type == fabtoken.Type
}

// ParseFabtokenOutput parses raw as a fabtoken output with a non-empty owner
// and type, and a positive quantity representable with precision bits. It is
// the check shared by both upgrade paths: in place, inside a transfer, and
// burn-and-re-issue (see ParseUpgradeInput).
func ParseFabtokenOutput(raw []byte, precision uint64) (*UpgradeInput, error) {
	if !IsFabtokenOutput(raw) {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "not a fabtoken output")
	}
	out := &actions.Output{}
	if err := out.Deserialize(raw); err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "not a fabtoken output"))
	}
	if len(out.Owner) == 0 {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "empty owner")
	}
	if len(out.Type) == 0 {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "empty token type")
	}
	q, err := token2.NewUBigQuantity(out.Quantity, precision)
	if err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrapf(err, "invalid quantity [%s]", out.Quantity))
	}
	if q.Sign() <= 0 || !q.IsUint64() {
		return nil, errors.Wrapf(ErrInvalidUpgradeInput, "quantity [%s] out of range", out.Quantity)
	}

	return &UpgradeInput{Owner: out.Owner, Type: out.Type, Value: q.Uint64()}, nil
}

// ParseUpgradeInput parses raw as a fabtoken output (see ParseFabtokenOutput)
// that can be upgraded by burn-and-re-issue: its owner must also be a plain
// identity (x509, idemix or idemix nym), since script, multisig and policy
// owners cannot sign the upgrade proof.
func ParseUpgradeInput(raw []byte, precision uint64) (*UpgradeInput, error) {
	in, err := ParseFabtokenOutput(raw, precision)
	if err != nil {
		return nil, err
	}
	typed, err := identity.UnmarshalTypedIdentity(in.Owner)
	if err != nil {
		return nil, errors.Join(ErrInvalidUpgradeInput, errors.Wrap(err, "malformed owner"))
	}
	switch typed.Type {
	case driver.X509IdentityType, driver.IdemixIdentityType, driver.IdemixNymIdentityType:
	default:
		return nil, errors.Wrapf(ErrInvalidUpgradeInput, "unsupported owner identity type [%d]", typed.Type)
	}

	return in, nil
}

// UpgradeSummary is the aggregate of a list of upgrade inputs.
type UpgradeSummary struct {
	// Type is the token type shared by every input.
	Type token2.Type
	// Total is the overflow-checked sum of the input values.
	Total uint64
}

// SummarizeUpgradeInputs parses every input (see ParseUpgradeInput) and checks
// that the list is non-empty, free of duplicate ledger IDs, of a single token
// type, and that its total fits in a uint64.
func SummarizeUpgradeInputs(inputs []*IssueActionInput, precision uint64) (*UpgradeSummary, error) {
	if len(inputs) == 0 {
		return nil, errors.Wrap(ErrInvalidUpgradeInput, "no inputs")
	}
	summary := &UpgradeSummary{}
	seenIDs := make(map[token2.ID]struct{}, len(inputs))
	for i, in := range inputs {
		if in == nil || len(in.Token) == 0 || len(in.ID.TxId) == 0 {
			return nil, errors.Wrapf(ErrInvalidUpgradeInput, "input [%d] is nil or incomplete", i)
		}
		if _, ok := seenIDs[in.ID]; ok {
			return nil, errors.Wrapf(ErrDuplicateUpgradeInput, "input [%d] [%s]", i, in.ID)
		}
		seenIDs[in.ID] = struct{}{}

		parsed, err := ParseUpgradeInput(in.Token, precision)
		if err != nil {
			return nil, errors.WithMessagef(err, "input [%d]", i)
		}
		if i == 0 {
			summary.Type = parsed.Type
		} else if parsed.Type != summary.Type {
			return nil, errors.Wrapf(ErrInvalidUpgradeInput, "input [%d] has type [%s], expected [%s]", i, parsed.Type, summary.Type)
		}
		if parsed.Value > math.MaxUint64-summary.Total {
			return nil, errors.Wrapf(ErrInvalidUpgradeInput, "total value overflows at input [%d]", i)
		}
		summary.Total += parsed.Value
	}

	return summary, nil
}
