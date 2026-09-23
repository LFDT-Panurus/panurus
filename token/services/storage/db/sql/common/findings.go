/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// findingFields is the column list of the findings table, in insertion order.
var findingFields = []common3.FieldName{
	"finding_key",
	"checker",
	"code",
	"severity",
	"tx_id",
	"token_id",
	"message",
	"first_seen",
	"last_seen",
	"occurrences",
	"resolved_at",
}

// getFindingsSchema returns the schema of the drift findings table.
//
// The table is keyed by the finding key rather than by a surrogate id, which is
// what makes the upsert on a repeated finding an update instead of a new row.
// It deliberately does not reference the requests table: a finding can be about
// a transaction this node never stored, which is precisely one of the cases the
// checks look for.
func (db *TransactionStore) getFindingsSchema() string {
	return fmt.Sprintf(`
		-- findings
		CREATE TABLE IF NOT EXISTS %s (
			finding_key TEXT NOT NULL PRIMARY KEY,
			checker TEXT NOT NULL,
			code TEXT NOT NULL,
			severity INT NOT NULL,
			tx_id TEXT NOT NULL,
			token_id TEXT NOT NULL,
			message TEXT NOT NULL,
			first_seen TIMESTAMP NOT NULL,
			last_seen TIMESTAMP NOT NULL,
			occurrences BIGINT NOT NULL,
			resolved_at TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_findings_open_%s ON %s ( checker, last_seen ) WHERE resolved_at IS NULL;
		CREATE INDEX IF NOT EXISTS idx_findings_severity_%s ON %s ( severity DESC, last_seen DESC );
		CREATE INDEX IF NOT EXISTS idx_findings_tx_id_%s ON %s ( tx_id );
		`,
		db.table.Findings,
		db.table.Findings, db.table.Findings,
		db.table.Findings, db.table.Findings,
		db.table.Findings, db.table.Findings,
	)
}

// findingsUpsertChunkSize caps how many finding rows one upsert statement
// writes. Each row binds len(findingFields) (11) parameters; SQLite refuses a
// statement with more than 32766 bound parameters (~2978 rows) and Postgres
// 65535 (~5957), so a single sweep large enough to exceed either exhausts the
// whole statement and persists nothing, and keeps failing identically on
// every following sweep. Comfortably under both ceilings, findings are
// upserted in chunks that all share the sweep's one seenAt, rather than in a
// single statement per sweep.
const findingsUpsertChunkSize = 1000

// UpsertFindings records the findings observed by one sweep. See the driver
// interface for the aging semantics.
func (db *TransactionStore) UpsertFindings(ctx context.Context, findings []dbdriver.FindingRecord, seenAt time.Time) error {
	if len(findings) == 0 {
		return nil
	}

	// the same key can show up twice in one sweep, for instance when two checks
	// report the same token. Inserting both rows in one statement would make the
	// database complain about a row conflicting with itself, so keep the last one.
	deduplicated := make(map[string]dbdriver.FindingRecord, len(findings))
	order := make([]string, 0, len(findings))
	for _, finding := range findings {
		if _, ok := deduplicated[finding.Key]; !ok {
			order = append(order, finding.Key)
		}
		deduplicated[finding.Key] = finding
	}

	for start := 0; start < len(order); start += findingsUpsertChunkSize {
		end := min(start+findingsUpsertChunkSize, len(order))
		if err := db.upsertFindingsChunk(ctx, deduplicated, order[start:end], seenAt); err != nil {
			return err
		}
	}

	return nil
}

// upsertFindingsChunk upserts one chunk of at most findingsUpsertChunkSize findings.
func (db *TransactionStore) upsertFindingsChunk(ctx context.Context, deduplicated map[string]dbdriver.FindingRecord, keys []string, seenAt time.Time) error {
	rows := make([]common3.Tuple, 0, len(keys))
	for _, key := range keys {
		finding := deduplicated[key]
		rows = append(rows, common3.Tuple{
			finding.Key,
			finding.Checker,
			finding.Code,
			finding.Severity,
			finding.TxID,
			finding.TokenID,
			finding.Message,
			seenAt.UTC(),
			seenAt.UTC(),
			int64(1),
			nil,
		})
	}

	query, args := q.InsertInto(db.table.Findings).
		Fields(findingFields...).
		Rows(rows).
		OnConflict(
			[]common3.FieldName{"finding_key"},
			q.OverwriteValue("severity"),
			q.OverwriteValue("message"),
			q.OverwriteValue("last_seen"),
			q.IncrementValue(db.table.Findings, "occurrences", int64(1)),
			// a finding that is observed again was not really resolved, so reopen it
			// rather than leave a resolved row that keeps being updated
			q.SetValue("resolved_at", nil),
		).
		Format()

	logging.Debug(logger, query, args)
	if _, err := db.writeDB.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed storing [%d] findings", len(rows))
	}

	return nil
}

// ResolveFindingsNotSeenSince closes the open findings of the passed checkers that
// the latest sweep did not observe.
func (db *TransactionStore) ResolveFindingsNotSeenSince(ctx context.Context, checkers []string, seenAt time.Time) (int64, error) {
	if len(checkers) == 0 {
		// a sweep that ran no check has learned nothing, so it may not close anything
		return 0, nil
	}

	query, args := q.Update(db.table.Findings).
		Set("resolved_at", seenAt.UTC()).
		Where(cond.And(
			cond.In("checker", checkers...),
			cond.IsNil(common3.FieldName("resolved_at")),
			cond.Lt("last_seen", seenAt.UTC()),
		)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	res, err := db.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, errors.Wrapf(err, "failed resolving findings of checkers %v", checkers)
	}
	resolved, err := res.RowsAffected()
	if err != nil {
		// the statement did run, so report success with an unknown count rather than
		// make the caller believe nothing was resolved
		logger.DebugfContext(ctx, "failed counting resolved findings: %s", err)

		return 0, nil
	}

	return resolved, nil
}

// QueryFindings returns the stored findings matching params, worst first.
func (db *TransactionStore) QueryFindings(ctx context.Context, params dbdriver.QueryFindingsParams) ([]*dbdriver.FindingRecord, error) {
	conditions := []cond.Condition{cond.AlwaysTrue}
	if !params.IncludeResolved {
		conditions = append(conditions, cond.IsNil(common3.FieldName("resolved_at")))
	}
	if len(params.Checkers) > 0 {
		conditions = append(conditions, cond.In("checker", params.Checkers...))
	}
	if len(params.Codes) > 0 {
		conditions = append(conditions, cond.In("code", params.Codes...))
	}
	if params.MinSeverity > 0 {
		conditions = append(conditions, cond.Gte("severity", params.MinSeverity))
	}

	selection := q.Select().
		FieldsByName(findingFields...).
		From(q.Table(db.table.Findings)).
		Where(cond.And(conditions...)).
		OrderBy(q.Desc(common3.FieldName("severity")), q.Desc(common3.FieldName("last_seen")))

	var query string
	var args []common3.Param
	if params.Limit > 0 {
		query, args = selection.Limit(params.Limit).Format(db.ci)
	} else {
		query, args = selection.Format(db.ci)
	}

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed querying findings")
	}
	defer Close(rows)

	var findings []*dbdriver.FindingRecord
	for rows.Next() {
		finding, err := scanFinding(rows)
		if err != nil {
			return nil, errors.Wrapf(err, "failed scanning finding")
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrapf(err, "failed querying findings")
	}

	return findings, nil
}

// scanFinding reads one findings row, in the column order of findingFields.
func scanFinding(rows *sql.Rows) (*dbdriver.FindingRecord, error) {
	var finding dbdriver.FindingRecord
	var resolvedAt sql.NullTime
	if err := rows.Scan(
		&finding.Key,
		&finding.Checker,
		&finding.Code,
		&finding.Severity,
		&finding.TxID,
		&finding.TokenID,
		&finding.Message,
		&finding.FirstSeen,
		&finding.LastSeen,
		&finding.Occurrences,
		&resolvedAt,
	); err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		resolved := resolvedAt.Time
		finding.ResolvedAt = &resolved
	}

	return &finding, nil
}
