/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"fmt"
	"strings"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// Severity classifies how urgently a Finding needs an operator's attention.
type Severity uint8

const (
	// SeverityInfo marks a divergence that is expected to resolve on its own,
	// for example a transaction the ledger has not caught up with yet.
	SeverityInfo Severity = iota
	// SeverityWarning marks a divergence that will not resolve on its own but
	// does not cost the node anything it cannot recover.
	SeverityWarning
	// SeverityCritical marks a divergence that loses the node money or makes it
	// unable to spend what it owns.
	SeverityCritical
)

// String returns the lowercase name of the severity, as it is persisted and reported.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(s))
	}
}

// Finding codes emitted by the default checkers. They are stable identifiers:
// they are persisted, and an operator filters and suppresses on them, so they
// must not change meaning between releases.
const (
	// CodeTxStatusMismatch is reported when the local status of a transaction
	// disagrees with the ledger's.
	CodeTxStatusMismatch = "tx_status_mismatch"
	// CodeTxStatusUnavailable is reported when the ledger status of a
	// transaction could not be retrieved at all.
	CodeTxStatusUnavailable = "tx_status_unavailable"
	// CodeTxRequestMissing is reported when a transaction record has no token
	// request stored alongside it, so nothing about it can be verified.
	CodeTxRequestMissing = "tx_request_missing"
	// CodeTxRequestUnparsable is reported when a stored token request cannot be
	// parsed back with the current TMS.
	CodeTxRequestUnparsable = "tx_request_unparsable"
	// CodeTokenContentMismatch is reported when a token held locally does not
	// match the ledger's copy of it.
	CodeTokenContentMismatch = "token_content_mismatch"
	// CodeTokenMissingOnLedger is reported when a token the node holds as
	// unspent is not on the ledger at all.
	CodeTokenMissingOnLedger = "token_missing_on_ledger"
	// CodeTokenMissingLocally is reported when the ledger holds a token for this
	// node that never landed in the local database. This is the direction that
	// costs the owner money, so it is always critical.
	CodeTokenMissingLocally = "token_missing_locally"
	// CodeTokenFormatUnsupported is reported when an unspent token's format is
	// not among the formats the current TMS supports.
	CodeTokenFormatUnsupported = "token_format_unsupported" //nolint:gosec // a finding code, not a credential
	// CodeTokenNotDeobfuscatable is reported when an unspent token cannot be
	// deobfuscated with the current TMS.
	CodeTokenNotDeobfuscatable = "token_not_deobfuscatable" //nolint:gosec // a finding code, not a credential
	// CodeTokenNoRecipients is reported when a token deobfuscates to an empty
	// recipient list.
	CodeTokenNoRecipients = "token_no_recipients" //nolint:gosec // a finding code, not a credential
	// CodeTokenRecipientUnverifiable is reported when no owner verifier can be
	// obtained for one of a token's recipients.
	CodeTokenRecipientUnverifiable = "token_recipient_unverifiable"
	// CodeCheckFailed is reported when a checker could not complete, so its part
	// of the sweep proved nothing. It is distinct from a clean sweep.
	CodeCheckFailed = "check_failed"
	// CodeUnclassified carries a message from a checker that predates structured
	// findings. See FindingFromMessage.
	CodeUnclassified = "unclassified"
)

// inconclusiveCodes are the codes that mean a check learned nothing about the
// specific transaction or token it was looking at, rather than confirming it
// is fine. A checker that reports one of these must not be treated as having
// completed a full pass: closing its older findings on the strength of a
// sweep that could not actually re-verify them would turn a ledger outage
// into a false report that the earlier problem resolved itself.
var inconclusiveCodes = map[string]struct{}{
	CodeCheckFailed:         {},
	CodeTxStatusUnavailable: {},
}

// IsInconclusive reports whether code means a check could not reach a real
// verdict about what it was asked to verify.
func IsInconclusive(code string) bool {
	_, ok := inconclusiveCodes[code]

	return ok
}

// Finding is one divergence between the local databases and the ledger.
//
// A Finding is identified by its Key, which is stable across sweeps: the same
// underlying problem seen in two consecutive sweeps produces the same key, which
// is what lets the store age a finding rather than record it twice.
type Finding struct {
	// Checker names the check that produced this finding.
	Checker string
	// Code classifies the finding. It is one of the Code* constants.
	Code string
	// Severity is how urgently this needs attention.
	Severity Severity
	// TxID is the transaction this finding is about, empty if it is not about one.
	TxID string
	// TokenID is the token this finding is about, nil if it is not about one.
	TokenID *token2.ID
	// Message is the human-readable description.
	Message string
}

// Key returns the stable identity of the finding.
//
// It deliberately excludes Message and Severity: the message may carry a
// changing detail (a ledger status code that moved on, an error string) and
// re-keying on it would make the same problem look new on every sweep.
//
// An unclassified finding has neither a code nor ids to key on, so it falls
// back to the message. See FindingFromMessage for what that costs.
func (f Finding) Key() string {
	var sb strings.Builder
	sb.WriteString(f.Checker)
	sb.WriteByte('|')
	sb.WriteString(f.Code)
	sb.WriteByte('|')
	if f.Code == CodeUnclassified {
		sb.WriteString(f.Message)

		return sb.String()
	}
	sb.WriteString(f.TxID)
	sb.WriteByte('|')
	if f.TokenID != nil {
		sb.WriteString(f.TokenID.String())
	}

	return sb.String()
}

// String renders the finding as a single line, the form the legacy
// []string-returning Check API reports.
func (f Finding) String() string {
	var sb strings.Builder
	sb.WriteByte('[')
	sb.WriteString(f.Severity.String())
	sb.WriteString("][")
	sb.WriteString(f.Code)
	sb.WriteString("] ")
	sb.WriteString(f.Message)

	return sb.String()
}

// FindingFromMessage lifts a message from a checker that still returns plain
// strings into a Finding.
//
// Such a finding has no code and no ids to key on, so its Key falls back to the
// message itself. That is weaker than a structured key: a checker that embeds a
// timestamp or a changing counter in its message will look like a new finding on
// every sweep. Custom checkers that want to be aged should return Findings.
func FindingFromMessage(checker string, message string) Finding {
	return Finding{
		Checker:  checker,
		Code:     CodeUnclassified,
		Severity: SeverityWarning,
		Message:  message,
	}
}
