/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"time"
)

// FindingRecord is one drift finding as it is stored.
//
// A finding is identified by its Key, which the checker computes so that the
// same underlying problem produces the same key on every sweep. That is what
// lets a repeated problem be aged (FirstSeen, LastSeen, Occurrences) instead of
// piling up a new row every time the sweep runs.
type FindingRecord struct {
	// Key is the stable identity of the finding, and the primary key of the row.
	Key string
	// Checker is the name of the check that produced the finding.
	Checker string
	// Code classifies the finding.
	Code string
	// Severity is how urgently the finding needs attention. Higher is worse; the
	// values are defined by the checks layer.
	Severity int
	// TxID is the transaction the finding is about, empty if it is not about one.
	TxID string
	// TokenID is the token the finding is about in its string form, empty if it is
	// not about one.
	TokenID string
	// Message is the human-readable description, as of the last sighting.
	Message string
	// FirstSeen is when the finding was first recorded.
	FirstSeen time.Time
	// LastSeen is when the finding was last observed.
	LastSeen time.Time
	// Occurrences is how many sweeps have observed the finding.
	Occurrences int64
	// ResolvedAt is when the finding stopped being observed, nil while it is open.
	ResolvedAt *time.Time
}

// QueryFindingsParams filters a findings query. A zero value returns the open
// findings of every checker.
type QueryFindingsParams struct {
	// Checkers restricts the result to findings produced by these checks. Empty
	// accepts any check.
	Checkers []string
	// Codes restricts the result to these finding codes. Empty accepts any code.
	Codes []string
	// MinSeverity drops findings below this severity.
	MinSeverity int
	// IncludeResolved also returns findings that are no longer observed.
	IncludeResolved bool
	// Limit caps the number of returned findings. Zero or negative means no cap.
	Limit int
}

// FindingsStore stores the findings of the ledger drift checks.
type FindingsStore interface {
	// UpsertFindings records the findings observed by one sweep, all with the same
	// seenAt timestamp.
	//
	// A finding whose key is not stored yet is inserted with FirstSeen and LastSeen
	// set to seenAt and one occurrence. A finding whose key is already stored has
	// its LastSeen advanced, its occurrence count raised, its message and severity
	// refreshed, and is reopened if it had been resolved. An empty slice is a no-op.
	UpsertFindings(ctx context.Context, findings []FindingRecord, seenAt time.Time) error

	// ResolveFindingsNotSeenSince marks as resolved every open finding of the passed
	// checkers that was not observed at or after seenAt, and returns how many it
	// resolved.
	//
	// Only findings of checkers that actually ran may be resolved: a sweep that
	// skipped a check has learned nothing about that check's findings, and closing
	// them would silently drop a real problem. Passing no checker resolves nothing.
	ResolveFindingsNotSeenSince(ctx context.Context, checkers []string, seenAt time.Time) (int64, error)

	// QueryFindings returns the stored findings matching params, worst first.
	QueryFindings(ctx context.Context, params QueryFindingsParams) ([]*FindingRecord, error)
}
