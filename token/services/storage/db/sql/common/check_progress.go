/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// getCheckProgressSchema returns the schema of the table where the history-walking
// drift checks record how far they have verified the transaction history.
//
// It holds one row per check. Like the findings table it lives next to the
// transactions it describes, so an owner store and an audit store each keep
// their own progress, and so does every TMS.
//
// CREATE TABLE IF NOT EXISTS is the whole migration: a node upgraded from a
// version without this table creates it empty at startup, and a check with no
// row simply walks the full history once, as it always did, before recording
// where it got to.
func (db *TransactionStore) getCheckProgressSchema() string {
	return fmt.Sprintf(`
		-- check progress
		CREATE TABLE IF NOT EXISTS %s (
			checker TEXT NOT NULL PRIMARY KEY,
			resume_from TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		`,
		db.table.CheckProgress,
	)
}

// GetCheckProgress returns the resume point checker last recorded, and false if
// it has not recorded one.
func (db *TransactionStore) GetCheckProgress(ctx context.Context, checker string) (time.Time, bool, error) {
	query, args := q.Select().
		FieldsByName("resume_from").
		From(q.Table(db.table.CheckProgress)).
		Where(cond.Eq("checker", checker)).
		Format(db.ci)

	logging.Debug(logger, query, args)
	rows, err := db.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return time.Time{}, false, errors.Wrapf(err, "failed reading the progress of check [%s]", checker)
	}
	defer Close(rows)

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return time.Time{}, false, errors.Wrapf(err, "failed reading the progress of check [%s]", checker)
		}

		return time.Time{}, false, nil
	}
	var resumeFrom time.Time
	if err := rows.Scan(&resumeFrom); err != nil {
		return time.Time{}, false, errors.Wrapf(err, "failed scanning the progress of check [%s]", checker)
	}

	return resumeFrom.UTC(), true, nil
}

// SetCheckProgress records the resume point of checker, replacing any previous one.
func (db *TransactionStore) SetCheckProgress(ctx context.Context, checker string, resumeFrom time.Time) error {
	query, args := q.InsertInto(db.table.CheckProgress).
		Fields("checker", "resume_from", "updated_at").
		Row(checker, resumeFrom.UTC(), time.Now().UTC()).
		OnConflict(
			[]common3.FieldName{"checker"},
			q.OverwriteValue("resume_from"),
			q.OverwriteValue("updated_at"),
		).
		Format()

	logging.Debug(logger, query, args)
	if _, err := db.writeDB.ExecContext(ctx, query, args...); err != nil {
		return errors.Wrapf(err, "failed recording the progress of check [%s]", checker)
	}

	return nil
}
