/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
)

const (
	// defaultLedgerInfoAttempts is how many times ScanBlock asks the ledger for
	// its current height before giving up. A transient RPC hiccup must not
	// decide the starting block, so the call is retried rather than trusted once.
	//
	// The budget is sized to outlast a peer restart, not just a dropped packet:
	// nothing retries ScanBlock (see the ledgerHeight comment), so a failure that
	// escapes here disables block-based finality until the process restarts.
	defaultLedgerInfoAttempts = 7
	// defaultLedgerInfoRetryDelay is the pause before the first retry. It doubles
	// on each further attempt, so the defaults wait ~31.5s in total (0.5s + 1s +
	// 2s + 4s + 8s + 16s over six retries).
	defaultLedgerInfoRetryDelay = 500 * time.Millisecond
	// defaultLedgerInfoMaxRetryDelay caps the doubling. Without a ceiling a large
	// configured attempt budget grows the pause without bound — past ~35 doublings
	// of the default delay a single wait exceeds a human lifetime, and the int64
	// nanosecond duration eventually wraps negative — so the scan would never
	// resume and never fail either. The cap only limits growth: a configured delay
	// larger than this is honoured as-is (see ledgerHeight).
	defaultLedgerInfoMaxRetryDelay = 30 * time.Second
	// ledgerInfoBackoffMultiplier doubles the delay on each further attempt.
	ledgerInfoBackoffMultiplier = 2.0
)

var (
	// ErrNoLedgerInfo reports that the ledger returned neither info nor an error,
	// so no height could be read. It signals a driver contract violation rather
	// than a transient failure: retrying it is not expected to help.
	ErrNoLedgerInfo = errors.New("ledger returned no info")
	// ErrLedgerHeightUnavailable reports that the current ledger height could not
	// be read, so the block scan was refused instead of restarted from genesis.
	// Every failure to resolve the starting block carries it — whether the attempt
	// budget ran out or the context ended the retries — so it is the single test
	// for "no scan started"; an error without it comes from the scan. Every cause
	// observed along the way remains inspectable with errors.Is.
	ErrLedgerHeightUnavailable = errors.New("ledger height unavailable")
)

// blockFromScanner is the subset of fabric.Delivery used by Delivery: the block
// scan, parameterized by the block to start from.
type blockFromScanner interface {
	ScanBlockFrom(ctx context.Context, block uint64, callback fabric.BlockCallback) error
}

// ledgerHeightProvider is the subset of fabric.Ledger used by Delivery: the
// current ledger height, which is where a fresh block scan resumes.
type ledgerHeightProvider interface {
	GetLedgerInfo() (*fabric.LedgerInfo, error)
}

// LedgerInfoRetry is the retry budget for the ledger-height read that decides
// where a block scan starts, as carried from configuration to a Delivery. A zero
// value selects the defaults.
type LedgerInfoRetry struct {
	// Attempts bounds the number of GetLedgerInfo calls.
	Attempts int
	// Delay is the pause before the first retry; it doubles on each further
	// attempt, up to defaultLedgerInfoMaxRetryDelay or Delay itself, whichever is
	// larger.
	Delay time.Duration
}

type Delivery struct {
	Delivery blockFromScanner
	Ledger   ledgerHeightProvider
	Logger   logging.Logger

	// LedgerInfoAttempts bounds the number of GetLedgerInfo attempts made by
	// ScanBlock. Zero or negative selects defaultLedgerInfoAttempts.
	LedgerInfoAttempts int
	// LedgerInfoRetryDelay is the pause before the first GetLedgerInfo retry; it
	// doubles on each further attempt, bounded as described on
	// defaultLedgerInfoMaxRetryDelay. Zero or negative selects
	// defaultLedgerInfoRetryDelay.
	LedgerInfoRetryDelay time.Duration
}

// ScanBlock scans blocks starting from the current ledger height.
//
// The height is read from the ledger, retried a bounded number of times so a
// transient RPC failure does not decide where the scan starts. If the height
// remains unavailable, the error is returned instead of falling back to block 0:
// on a chain with history that fallback silently turns a passing RPC hiccup into
// a full rescan from genesis, with duplicate finality notifications and no error
// for the caller to distinguish it from a genuinely fresh chain.
//
// A nil Ledger is the one case that still starts at block 0 — there is no height
// to read, so the whole chain is the intended range.
//
// The returned error is classifiable without inspecting its message. Every
// failure to resolve the starting block matches ErrLedgerHeightUnavailable, and
// no scan has started in that case; an error that does not match it came from the
// scan. Two further sentinels refine the height failure:
//
//   - ErrNoLedgerInfo, when some attempt saw a ledger return neither info nor an
//     error. Every attempt's failure is reported, so this is present whenever the
//     contract was violated at least once, even intermittently.
//   - context.Canceled or context.DeadlineExceeded, when the context ended the
//     retries. This one is not a discriminator on its own: the same context
//     governs the scan, so a context error alone does not say which of the two
//     ended. Pair it with ErrLedgerHeightUnavailable to tell a cancelled height
//     read from a cancelled scan.
func (d *Delivery) ScanBlock(background context.Context, callback fabric.BlockCallback) error {
	startingBlock := uint64(0)
	if d.Ledger != nil {
		height, err := d.ledgerHeight(background)
		if err != nil {
			return err
		}
		startingBlock = height
	}

	return d.Delivery.ScanBlockFrom(background, startingBlock, callback)
}

// ledgerHeight returns the current ledger height, retrying transient failures
// with an exponentially growing but capped delay. It gives up as soon as the
// context is cancelled, and reports every observed failure once the attempts run
// out.
//
// The budget is generous because nothing retries the caller: FSC's
// events.ListenerManager calls ScanBlock once from a goroutine that only logs the
// result, so an error escaping here leaves the channel without block-based
// finality until the process restarts. Absorbing a peer restart here is therefore
// worth more than failing fast.
//
// The backoff is delegated to utils.RetryRunner rather than hand-rolled, so the
// growth is bounded by its maximum delay instead of doubling indefinitely. The
// cap is raised to the configured delay when that is larger, so it limits growth
// without silently shortening a pause an operator asked for.
//
// Failures are joined with a sentinel rather than described only in the message,
// so callers classify them with errors.Is: ErrLedgerHeightUnavailable on both
// exits, additionally ErrNoLedgerInfo for a driver returning (nil, nil) and the
// context error for a cancelled wait. The underlying ledger errors are preserved
// in every case.
func (d *Delivery) ledgerHeight(ctx context.Context) (uint64, error) {
	attempts := d.LedgerInfoAttempts
	if attempts <= 0 {
		attempts = defaultLedgerInfoAttempts
	}
	delay := d.LedgerInfoRetryDelay
	if delay <= 0 {
		delay = defaultLedgerInfoRetryDelay
	}
	maxDelay := max(defaultLedgerInfoMaxRetryDelay, delay)

	var (
		height   uint64
		failures []error
		attempt  int
	)
	runner := utils.NewRetryRunnerWithJitter(d.Logger, attempts, delay, maxDelay, ledgerInfoBackoffMultiplier, 0)
	err := runner.RunWithErrorsContext(ctx, func() (bool, error) {
		attempt++
		info, err := d.Ledger.GetLedgerInfo()
		switch {
		case err != nil:
			failures = append(failures, err)
		case info == nil:
			// A driver that reports neither info nor error would otherwise nil
			// deref below; treat it as a failure to read the height.
			failures = append(failures, ErrNoLedgerInfo)
		default:
			height = info.Height

			return true, nil
		}

		lastErr := failures[len(failures)-1]
		d.Logger.ErrorfContext(ctx, "failed to get ledger info (attempt %d/%d): %s", attempt, attempts, lastErr)

		// Terminate on the last attempt instead of letting the runner exhaust its
		// budget: it sleeps at the end of every iteration, so falling out of the
		// loop would add one final pause that no further attempt follows.
		return attempt == attempts, lastErr
	})
	if err == nil {
		return height, nil
	}

	// ctx.Err() is nil unless the retries were cancelled, and Join drops nils, so
	// the same causes serve both exits.
	causes := errors.Join(append([]error{ErrLedgerHeightUnavailable, ctx.Err()}, failures...)...)
	if ctx.Err() != nil {
		return 0, errors.Wrapf(causes, "cancelled while getting ledger info after %d attempt(s)", attempt)
	}

	return 0, errors.Wrapf(causes, "failed to get ledger info after %d attempt(s), refusing to rescan from genesis", attempts)
}
