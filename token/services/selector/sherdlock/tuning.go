/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

const (
	// This way we avoid deadlocks, e.g. We have 2 tokens of value 10CHF each (20 CHF in total).
	// We also have two processes that both ask for 15CHF. If both of them concurrently lock one token each,
	// they will retry maxRetry times to see if the other process in the meantime unlocked the token.
	// If not, to avoid locking these tokens forever, we roll back and unlock the tokens.
	defaultMaxImmediateRetries = 5

	// defaultSufficiencyWindow bounds how many ascending-by-amount candidates
	// nextCandidate considers together once it finds one that, on its own,
	// already covers the remaining requested amount. bucketedIterator's
	// shuffle (fetcher.go) only randomizes tokens whose Quantity is
	// byte-equal, so with realistic wallets every such bucket has size 1 and the
	// ascending scan is fully deterministic: any request smaller than the
	// smallest token always targets that single token, exactly the hot-spot
	// #2395 warned against. The sufficiency window widens the randomization to
	// "all individually-sufficient candidates within a bounded lookahead",
	// not just byte-equal ones. The size is a tradeoff: too small (1) is the
	// old fully-deterministic behavior; too large risks locking a much
	// bigger token than needed for a small payment - its own complaint in
	// #2395 ("a 1 CHF request grabbed a 200 CHF token"). 4 was chosen as a
	// small constant that still gives real statistical spread across a
	// handful of similarly-sized candidates while keeping the selected
	// token close to the smallest sufficient one.
	defaultSufficiencyWindow = 4

	// defaultMaxSufficiencyRatio additionally bounds the sufficiency window by magnitude, not
	// just count: a candidate only joins the window if its quantity is at most this many
	// times the *anchor's* quantity - the anchor being the first individually-sufficient
	// candidate, i.e. the smallest token the ascending scan would have locked anyway. Count
	// alone is not enough - a wallet with very few distinct amounts (e.g. exactly one small
	// token and one huge one, as in TestSizeOrderedSelection_SmallestFit) would otherwise have
	// the sufficiency window trivially swallow the huge token just because nothing else was in
	// between, defeating the smallest-fit bias for the smallest wallets, which are also the
	// ones a hot-token incident hurts the most. 5x keeps the window meaningful while still
	// refusing to lock, say, a 200 EUR token when a 1 EUR one would do.
	//
	// The bound is deliberately anchor-relative and not remaining-relative: with the remaining
	// amount as the basis, any wallet whose smallest token already exceeds 5x the request would
	// see its very first lookahead candidate rejected, leaving
	// a window of size 1 and fully deterministic selection: the mechanism would be inert in
	// exactly the regime it exists for. Anchoring on the smallest sufficient token keeps the
	// selected token within 5x of what would have been locked regardless, in every regime.
	defaultMaxSufficiencyRatio = 5

	// defaultMaxBatchWindow caps how many candidates a single batch claim may cover. The window
	// otherwise grows until it covers the remaining amount, which is unbounded in the number
	// of tokens: a dust-heavy wallet paying a large amount can need tens of thousands of
	// inputs. The Postgres claim binds 2n+5 parameters (claimCandidates) and the wire protocol
	// refuses a statement with more than 65535, so an uncapped window would surface as a store
	// error - and the store-error branch refetches and rebuilds the same oversized window on
	// every attempt, so the selection burns its whole retry budget and fails where the
	// single-token path would have succeeded. The store refuses such a batch itself
	// (postgres.MaxLockBatchCandidates), but only this cap keeps a selection from ever
	// building one: 256 is two orders of magnitude inside that bound, and is chosen for the
	// statement size a claim should have rather than for the largest one it could have.
	//
	// The cap costs nothing in the common case and does not change what gets selected: the
	// outer loop simply claims another window when one is not enough, exactly as it already
	// does when the cache runs out mid-window.
	defaultMaxBatchWindow = 256

	// MaxBatchWindowLimit is the largest MaxBatchWindow a Tuning accepts; a configured value
	// above it is clamped down to it (with a warning), rather than let through to the store.
	//
	// It intentionally does not reference postgres.MaxLockBatchCandidates - importing the
	// postgres storage driver from the selector package would pull its SQL driver dependency
	// into every consumer of sherdlock, for a limit that is purely defensive here: the store
	// already refuses an oversized batch outright with an actionable error (see
	// postgres.TokenLockStore.LockBatch), so this clamp only keeps a misconfigured
	// maxBatchWindow from ever reaching that refusal and burning the retry budget on it (see
	// defaultMaxBatchWindow above). The two bounds are independently declared and pinned
	// against each other by TestMaxBatchWindowLimit_FitsMaxLockBatchCandidates
	// (tuning_bound_test.go, package sherdlock_test), which imports the postgres package to
	// compare the two constants - a test-only dependency in the direction the selector
	// already depends on storage, not the reverse, and one that does not leak into
	// production code.
	//
	// 4096 is comfortably inside postgres.MaxLockBatchCandidates (32765) while still well
	// above any value an operator would plausibly configure on purpose.
	MaxBatchWindowLimit = 4096
)

// Tuning collects the sherdlock selector knobs that affect lock contention: how aggressively
// nextCandidate randomizes among sufficient candidates, how many times a Select call refetches
// before giving up, and how large a single batch lock claim may grow. It is normalized (see
// normalize) before use, so a zero Tuning is the default configuration.
type Tuning struct {
	// SufficiencyWindow bounds how many ascending-by-amount candidates nextCandidate
	// considers together once it finds one that, on its own, already covers the remaining
	// requested amount. <= 0 selects defaultSufficiencyWindow.
	SufficiencyWindow int
	// MaxSufficiencyRatio additionally bounds the sufficiency window by magnitude: a
	// candidate only joins the window if its quantity is at most this many times the
	// anchor's quantity. <= 0 selects defaultMaxSufficiencyRatio.
	MaxSufficiencyRatio int
	// MaxImmediateRetries bounds how many times a Select call refetches its candidate cache
	// before giving up with token.SelectorSufficientButLockedFunds. <= 0 selects
	// defaultMaxImmediateRetries.
	MaxImmediateRetries int
	// MaxBatchWindow caps how many candidates a single batch lock claim may cover. <= 0
	// selects defaultMaxBatchWindow; a value above MaxBatchWindowLimit is clamped to it.
	MaxBatchWindow int
}

// normalize substitutes the package default for every non-positive field, and clamps
// MaxBatchWindow to MaxBatchWindowLimit, warning once when it does. It is the single
// chokepoint every Tuning must pass through before reaching a Selector: every invariant the
// selector relies on (nextCandidate never calls rand.IntN on an empty window, the pending
// lookahead buffer stays bounded by the window size, a batch claim never exceeds what the
// store will accept) assumes a normalized Tuning, not a raw, possibly-zero one.
func (t Tuning) normalize() Tuning {
	if t.SufficiencyWindow <= 0 {
		t.SufficiencyWindow = defaultSufficiencyWindow
	}
	if t.MaxSufficiencyRatio <= 0 {
		t.MaxSufficiencyRatio = defaultMaxSufficiencyRatio
	}
	if t.MaxImmediateRetries <= 0 {
		t.MaxImmediateRetries = defaultMaxImmediateRetries
	}
	if t.MaxBatchWindow <= 0 {
		t.MaxBatchWindow = defaultMaxBatchWindow
	} else if t.MaxBatchWindow > MaxBatchWindowLimit {
		logger.Warnf(
			"configured maxBatchWindow [%d] exceeds the limit of [%d]; clamping to it. A batch "+
				"window this large risks a store error on the batch-lock claim, which would then "+
				"burn the selector's whole immediate-retry budget rebuilding it",
			t.MaxBatchWindow, MaxBatchWindowLimit,
		)
		t.MaxBatchWindow = MaxBatchWindowLimit
	}

	return t
}

// Option customizes the Tuning a sherdlock selector uses. Options are applied in order, so a
// later option overrides an earlier one for any field it sets.
type Option func(*Tuning)

// WithSufficiencyWindow overrides Tuning.SufficiencyWindow.
func WithSufficiencyWindow(window int) Option {
	return func(t *Tuning) { t.SufficiencyWindow = window }
}

// WithMaxSufficiencyRatio overrides Tuning.MaxSufficiencyRatio.
func WithMaxSufficiencyRatio(ratio int) Option {
	return func(t *Tuning) { t.MaxSufficiencyRatio = ratio }
}

// WithMaxImmediateRetries overrides Tuning.MaxImmediateRetries.
func WithMaxImmediateRetries(retries int) Option {
	return func(t *Tuning) { t.MaxImmediateRetries = retries }
}

// WithMaxBatchWindow overrides Tuning.MaxBatchWindow.
func WithMaxBatchWindow(window int) Option {
	return func(t *Tuning) { t.MaxBatchWindow = window }
}

// WithTuning overrides every Tuning field at once, so a whole Tuning value (for example one
// derived from configuration) can be passed where an Option is expected.
func WithTuning(tuning Tuning) Option {
	return func(t *Tuning) { *t = tuning }
}

// CompileOptions applies opts over the zero Tuning, in order, and normalizes the result. The
// zero value of Tuning therefore already is the default configuration.
func CompileOptions(opts ...Option) Tuning {
	var t Tuning
	for _, opt := range opts {
		if opt != nil {
			opt(&t)
		}
	}

	return t.normalize()
}
