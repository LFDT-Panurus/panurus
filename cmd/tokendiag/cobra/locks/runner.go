/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locks

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// isTerminal reports whether status is a terminal status of a consuming transaction —
// i.e. one after which the lock it holds should already have been released. A lock
// still present with a terminal-status consumer is the mechanism-4 leak from #2395:
// nothing on the success path calls UnlockByTxID, so the row survives until the
// next lease-age sweep.
func isTerminal(status *driver3.TxStatus) bool {
	if status == nil {
		return false
	}

	switch *status {
	case driver3.Confirmed, driver3.Deleted, driver3.Orphan:
		return true
	default:
		return false
	}
}

// statusName renders status for display, or "unknown" if nil.
func statusName(status *driver3.TxStatus) string {
	if status == nil {
		return "unknown"
	}
	if name, ok := driver3.TxStatusMessage[*status]; ok {
		return name
	}

	return strconv.Itoa(*status)
}

// Run reads every currently held lock via TokenLockStore.ListLocks and reports:
//   - every held lock, with its age and the status of its consuming transaction;
//   - locks whose consumer has already reached a terminal status (the leak
//     mechanism-4 in #2395 describes — the lock is not released on settlement,
//     so it sits until the next lease-age sweep);
//   - the same list ordered oldest lock first, which is as close to a hot-token
//     ranking as one snapshot gets: it cannot distinguish "repeatedly
//     re-contended" from "held a long time" without comparing against an
//     earlier run;
//   - a summary line intended for scripting (total, leaked, oldest age).
func Run(ctx context.Context, w io.Writer, stores *Stores, now time.Time) error {
	records, err := stores.TokenLock.ListLocks(ctx)
	if err != nil {
		return fmt.Errorf("list locks: %w", err)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})

	if _, err := fmt.Fprintf(w, "--- Held locks (oldest first) ---\n"); err != nil {
		return err
	}
	var leaked int
	var oldest time.Duration
	for i, r := range records {
		age := now.Sub(r.CreatedAt)
		if i == 0 {
			oldest = age
		}
		terminalMark := ""
		if isTerminal(r.Status) {
			leaked++
			terminalMark = "  [LEAKED: consumer is terminal, lock should have been released]"
		}
		if _, err := fmt.Fprintf(w, "  token=%s:%d consumer_tx_id=%s age=%s status=%s%s\n",
			r.TokenID.TxId, r.TokenID.Index, r.ConsumerTxID, age.Round(time.Second), statusName(r.Status), terminalMark); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w, "\n--- Summary ---\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Total locks held         : %d\n", len(records)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Leaked (terminal consumer): %d\n", leaked); err != nil {
		return err
	}
	if len(records) > 0 {
		if _, err := fmt.Fprintf(w, "  Oldest lock age           : %s\n", oldest.Round(time.Second)); err != nil {
			return err
		}
	}

	return nil
}
