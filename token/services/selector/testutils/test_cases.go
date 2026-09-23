/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	depmock "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxSpendRetries bounds deleteTokensAndStoreChange's retry loop (see its doc comment): a
// persistent UpdateTokens failure fails the test with a clear message instead of hanging
// until the surrounding go test -timeout fires with no indication of where.
const maxSpendRetries = 100

const defaultCurrency = "CHF"

var (
	logger             = logging.MustGetLogger()
	defaultWalletOwner = []byte{1, 2, 3}
	// defaultTokenFilter leaves WalletID empty: sherdlock's SQL path relies on this
	// (an empty walletID means "no filter" to its underlying store — it applies
	// ContainsToken over Owner bytes itself instead), so setting it here would make
	// sherdlock's real-DB query filter on a walletID no stored token actually has,
	// silently returning zero tokens for every sherdlock test that uses this filter.
	// The simple driver's selector requires a non-empty ownerFilter.ID() (it uses it
	// directly as the SQL walletID filter), so it cannot share this filter — see
	// SimpleDriverTokenFilter and TestHotTokenContentionNWithFilter below.
	defaultTokenFilter = &TokenFilter{Wallet: defaultWalletOwner}
	// SimpleDriverTokenFilter is defaultTokenFilter's counterpart for the simple
	// driver: it must carry a non-empty WalletID matching the identity every token
	// is stored under (see UpdateTokens' hardcoded []string{"alice"} identity list
	// below), since simple's selector.Select rejects an empty ownerFilter.ID() and
	// uses it directly as the SQL walletID filter (unlike sherdlock's ContainsToken
	// fallback). Used by TestHotTokenContentionSimpleDriver
	// (token/services/selector/simple/contention_test.go) via
	// TestHotTokenContentionNWithFilter.
	SimpleDriverTokenFilter        = &TokenFilter{Wallet: defaultWalletOwner, WalletID: "alice"}
	txId                    uint32 = 0
)

type EnhancedManager interface {
	token2.SelectorManager
	TokenSum() (token.Quantity, error)
	UpdateTokens(spentTokens []*token.ID, addedTokens []token.UnspentToken) error
}

func TestSufficientTokensOneReplica(t *testing.T, replica EnhancedManager) {
	// Create 2 tokens of value CHF1 each (total CHF2)
	item := newToken(1)
	unspentTokens := createDefaultTokens(collections.Repeat(item, 2)...)
	err := storeTokens(replica, unspentTokens)
	require.NoError(t, err)

	// The replica asks for CHF1
	errs := parallelSelect(t, []EnhancedManager{replica}, []token.Quantity{newToken(1)})
	assert.Empty(t, errs)
}

func TestSufficientTokensBigDenominationsOneReplica(t *testing.T, replica EnhancedManager) {
	// Create 1 token of value CHF100
	unspentTokens := createDefaultTokens(newToken(100))
	err := storeTokens(replica, unspentTokens)
	require.NoError(t, err)

	// The replica asks for CHF1, ..., CHF1 for 100 times (total CHF100)
	item := newToken(1)
	errs := parallelSelect(t, []EnhancedManager{replica}, collections.Repeat(item, 100))
	assert.Empty(t, errs)
}

func TestSufficientTokensBigDenominationsManyReplicas(t *testing.T, replicas []EnhancedManager) {
	// Create 2 tokens of value CHF150 each (total CHF300)
	item := newToken(150)
	unspentTokens := createDefaultTokens(collections.Repeat(item, 2)...)
	err := storeTokens(replicas[0], unspentTokens)
	require.NoError(t, err)

	// Each replica asks for CHF100 (total CHF300)
	item = newToken(1)
	errs := parallelSelect(t, replicas, collections.Repeat(item, 100))
	assert.Empty(t, errs)
}

// TestHotTokenContention mirrors the incident reported in #2395: a wallet
// with a few small tokens and one much larger one, and far more concurrent
// requests than tokens. Every request asks for CHF1, satisfiable either by a
// small token directly or by the large one — which recycles most of its
// value back as a freshly-minted token via deleteTokensAndStoreChange, so at
// any instant there is exactly one "big" token in the pool. That is the hot
// token every losing goroutine keeps re-targeting after a lost lock race,
// absent a per-attempt blacklist (mechanism 1 in #2395) or an anti-join
// against already-locked tokens (mechanism 3).
//
// Unlike the other cases in this file, callers are expected to also inspect
// lock-conflict counts (see sherdlock's TestHotTokenContention, which wraps
// the Locker to record them) and assert on their distribution — this
// function only asserts the functional invariant that must hold regardless
// of how contention is distributed: total demand exactly matches the wallet
// balance, so no error here can be a genuine insufficient-funds; any error
// is spurious, caused by contention.
func TestHotTokenContention(t *testing.T, replicas []EnhancedManager) {
	TestHotTokenContentionN(t, replicas, 100)
}

// TestHotTokenContentionN is TestHotTokenContention parameterized by requestsPerReplica
// (TestHotTokenContention itself is just requestsPerReplica=100), so a caller whose driver
// has different concurrency characteristics can scale the workload down to something that
// completes in a reasonable time while keeping the same token-mix shape (a handful of small
// tokens plus one much larger, rotating "hot" one, demand exactly equal to wallet balance).
// See simple/contention_test.go's TestHotTokenContentionSimpleDriver: the simple driver's
// selector re-scans its whole candidate set from scratch on every lost lock race (no
// per-attempt blacklist, unlike sherdlock), so the original 3x100 shape takes far longer to
// settle against it than against sherdlock/Postgres.
func TestHotTokenContentionN(t *testing.T, replicas []EnhancedManager, requestsPerReplica int) {
	TestHotTokenContentionNWithFilter(t, replicas, requestsPerReplica, defaultTokenFilter)
}

// TestHotTokenContentionNWithFilter is TestHotTokenContentionN parameterized by the
// OwnerFilter passed to Select, so callers whose driver requires a non-empty
// ownerFilter.ID() (e.g. the simple driver — see SimpleDriverTokenFilter) can supply one
// without affecting sherdlock's tests, which rely on defaultTokenFilter's empty WalletID.
func TestHotTokenContentionNWithFilter(t *testing.T, replicas []EnhancedManager, requestsPerReplica int, filter token2.OwnerFilter) {
	require.Len(t, replicas, 3, "token mix below assumes exactly 3 replicas x requestsPerReplica requests of CHF1 = total balance")

	totalDemand := 3 * requestsPerReplica
	require.Greater(t, totalDemand, 4, "token mix below assumes the big token absorbs totalDemand-4 > 0")

	small := newToken(1)
	big := newToken(totalDemand - 4)
	unspentTokens := createDefaultTokens(append(collections.Repeat(small, 4), big)...)
	err := storeTokens(replicas[0], unspentTokens)
	require.NoError(t, err)

	// 3 replicas x requestsPerReplica requests of CHF1 = totalDemand, exactly the total balance.
	item := newToken(1)
	errs := parallelSelectWithFilter(t, replicas, collections.Repeat(item, requestsPerReplica), filter)
	assert.Empty(t, errs, "spurious insufficient-funds under lock contention (#2395)")
}

// TestHotTokenContentionWideWindow targets the case TestHotTokenContention structurally
// cannot: a wallet made entirely of CHF1 dust, with every request costing CHF3, so
// selectInternal's covering-window loop (selector.go) always needs three ascending
// candidates to satisfy one request, never one. TestHotTokenContention's rotating big
// token means almost every claim - after the initial handful of small tokens are spent -
// is a single token that alone covers the request, so its window is size 1 for nearly the
// entire run: exactly the case where FOR UPDATE SKIP LOCKED, which only skips *other*
// candidates present in the same statement, cannot show any benefit over a plain INSERT.
// Here there is no dominant token to fall back to, so every one of the run's many
// concurrent claims genuinely contends over which three dust tokens, among many
// similarly-ranked ones, it gets to walk away with - the scenario Phase 6's skipLocked
// strategy is meant to help.
func TestHotTokenContentionWideWindow(t *testing.T, replicas []EnhancedManager) {
	require.Len(t, replicas, 3, "token mix below assumes exactly 3 replicas x 10 requests of CHF3 = CHF90 = total balance")

	dust := newToken(1)
	unspentTokens := createDefaultTokens(collections.Repeat(dust, 90)...)
	err := storeTokens(replicas[0], unspentTokens)
	require.NoError(t, err)

	// 3 replicas x 10 requests of CHF3 = CHF90, exactly the total balance; every request
	// needs exactly 3 of the CHF1 tokens, so no change is ever minted.
	item := newToken(3)
	errs := parallelSelect(t, replicas, collections.Repeat(item, 10))
	assert.Empty(t, errs, "spurious insufficient-funds under lock contention (#2395, wide window)")
}

// TestHotTokenContentionWithSettlement is TestHotTokenContention with a settlement step
// spliced in after each successful Select+spend: it releases the winning transaction's
// locks through a *real* finality.SelectorManagerProvider, wired via
// depmock.TokenManagementServiceProvider/TokenManagementServiceWithExtensions to resolve to
// replica itself (every EnhancedManager already satisfies token2.SelectorManager) - the same
// provider chain finality.Listener.releaseLocks uses on tx confirmation
// (finality/listener.go:186-208). TestHotTokenContention's Close-only harness never touches
// the lock table (Close just evicts the selector from cache, sherdlock/manager.go:78-84), so
// it can only simulate mechanism 4's leak, never prove its fix. lockDB is the TokenLockStore
// backing every replica's Locker - callers construct their replicas over one shared table,
// so any one of them will do - used only for the final assertion: after every request has
// settled, ListLocks must report nothing at all, regardless of which replica won or how many
// lock conflicts it took.
func TestHotTokenContentionWithSettlement(t *testing.T, replicas []EnhancedManager, lockDB driver.TokenLockStore) {
	require.Len(t, replicas, 3, "token mix below assumes exactly 3 replicas x 100 requests of CHF1 = CHF300 = total balance")

	small := newToken(1)
	big := newToken(296)
	unspentTokens := createDefaultTokens(append(collections.Repeat(small, 4), big)...)
	err := storeTokens(replicas[0], unspentTokens)
	require.NoError(t, err)

	item := newToken(1)
	quantities := collections.Repeat(item, 100)
	errs := parallelSelectWithSettlement(t, replicas, quantities)

	locks, err := lockDB.ListLocks(t.Context())
	require.NoError(t, err)
	t.Logf(
		"#2395 contention [settlement]: requests=%d, spurious errors=%d, locks remaining after settlement=%d",
		len(quantities)*len(replicas), len(errs), len(locks),
	)
	assert.Empty(t, errs, "spurious insufficient-funds under lock contention (#2395)")
	assert.Empty(t, locks, "no lock should survive settlement of every request (#2395 mechanism 4)")
}

// TestStaticHotTokenContentionPareto targets the shape TestHotTokenContentionWithSettlement's
// rotating hot token structurally cannot: CERT's actual incident had a small, static set of
// tokens - never spent, only locked and released - absorbing the overwhelming majority of lock
// conflicts (one of them contested well over a thousand times in a few minutes). A rotating hot
// token (see TestHotTokenContention's doc comment) dilutes any single ID's share by
// construction, since deleteTokensAndStoreChange mints a fresh ID every time it is spent. Here,
// every winning request releases its lock via the real SelectorManager.Unlock path (like
// parallelSelectWithSettlement) but never spends the token, so the same handful of token IDs
// stay in the pool, and stay the top candidates, for the entire run.
//
// The wallet mix is the fixed hot set plus a "cold" pool stored under a different token type:
// Select's query is scoped by (walletID, tokenType), and defaultTokenFilter deliberately leaves
// WalletID empty (see its doc comment - sherdlock's SQL path treats that as "no owner filter"),
// so tokenType is the only scope this harness can actually rely on to keep the cold pool out of
// the candidate set entirely. An earlier version of this scenario instead relied on a much
// larger cold denomination to fall outside nextCandidate's maxSufficiencyRatio lookahead window
// (selector.go) - but that only stops cold from being pulled into a window anchored on a hot
// candidate; once every hot token is momentarily locked by other requests (unsurprising at this
// concurrency, against only numHotTokens candidates) the cold pool becomes the next visible
// individually-sufficient candidate in its own right and gets attempted anyway, diluting the hot
// set's measured share well below any believable concentration floor. Scoping by tokenType
// instead is enforced by the query itself, so the concentration this test asserts on is
// deterministic and not a share of one: the wallet genuinely holds other tokens, they are simply
// outside the scope of the requests under test, exactly as another currency's tokens in the same
// production DB would be.
//
// Callers (see sherdlock's TestStaticHotTokenContentionPareto, which wraps the Locker to record
// per-token attempt/conflict counts) are expected to assert on the concentration of conflicts
// among the returned hot token IDs. This function only asserts the invariants that must hold
// regardless of how contention is distributed: no error here can be a genuine insufficient-
// funds (every request's amount is well within a single hot token, and nothing is ever spent),
// and after the run every lock is released. Unlike TestHotTokenContention, it cannot also
// assert "no spurious insufficient-funds under spend" as a proxy for correctness, since nothing
// is ever spent here - it is testing contention shape, not the spend path.
func TestStaticHotTokenContentionPareto(t *testing.T, replicas []EnhancedManager, lockDB driver.TokenLockStore, requestsPerReplica int) []token.ID {
	require.Len(t, replicas, 3, "token mix below assumes exactly 3 replicas")

	const numHotTokens, numColdTokens = 5, 10
	const coldCurrency = defaultCurrency + "_COLD"
	hotValue := newToken(50)
	coldValue := newToken(10000)

	hotTokens := createDefaultTokens(collections.Repeat(hotValue, numHotTokens)...)
	coldTokens := createTokensWithType(coldCurrency, collections.Repeat(coldValue, numColdTokens)...)
	err := storeTokens(replicas[0], append(hotTokens, coldTokens...))
	require.NoError(t, err)

	hotIDs := make([]token.ID, 0, numHotTokens)
	for _, tk := range hotTokens {
		hotIDs = append(hotIDs, tk.Id)
	}

	// Well below hotValue, so every hot token alone is individually sufficient.
	item := newToken(1)
	errs := parallelSelectNoSpend(t, replicas, collections.Repeat(item, requestsPerReplica))

	locks, err := lockDB.ListLocks(t.Context())
	require.NoError(t, err)
	assert.Empty(t, errs, "spurious insufficient-funds under lock contention (#2395, static hot tokens)")
	assert.Empty(t, locks, "no lock should survive settlement of every request (#2395 mechanism 4)")

	return hotIDs
}

func TestInsufficientTokensOneReplica(t *testing.T, replica EnhancedManager) {
	// Create 2 tokens of value CHF1 each (total CHF2)
	item := newToken(1)
	unspentTokens := createDefaultTokens(collections.Repeat(item, 2)...)
	err := storeTokens(replica, unspentTokens)
	require.NoError(t, err)

	// The replica asks for CHF1, CHF1 (total CHF3)
	item = newToken(1)
	errs := parallelSelect(t, []EnhancedManager{replica}, collections.Repeat(item, 3))
	assert.Len(t, errs, 1)
}

func TestSufficientTokensManyReplicas(t *testing.T, replicas []EnhancedManager) {
	// Create 100 tokens of value CHF1 each (total CHF100)
	item := newToken(1)
	unspentTokens := createDefaultTokens(collections.Repeat(item, 100)...)
	err := storeTokens(replicas[0], unspentTokens)
	require.NoError(t, err)

	// Each replica asks for CHF5 (total CHF100)
	errs := parallelSelect(t, replicas, []token.Quantity{newToken(5)})
	assert.Empty(t, errs)
}

func TestInsufficientTokensManyReplicas(t *testing.T, replicas []EnhancedManager) {
	// Create 100 tokens of value CHF2 each (total CHF200)
	item := newToken(2)
	unspentTokens := createDefaultTokens(collections.Repeat(item, 50)...)
	err := storeTokens(replicas[0], unspentTokens)
	require.NoError(t, err)

	// Each replica asks for CHF3, and CHF3 (total CHF 240)
	item = newToken(3)
	errs := parallelSelect(t, replicas, collections.Repeat(item, 4))
	assert.NotEmpty(t, errs)
	sum, err := replicas[0].TokenSum()
	require.NoError(t, err)
	assert.Equal(t, 0, sum.Cmp(newToken(1)))
}

// Enhanced manager

type enhancedManager struct {
	token2.SelectorManager
	tokenDB driver.TokenStore
	t       *testing.T
}

func NewEnhancedManager(t *testing.T, manager token2.SelectorManager, tokenDB driver.TokenStore) *enhancedManager {
	t.Helper()

	return &enhancedManager{
		SelectorManager: manager,
		tokenDB:         tokenDB,
		t:               t,
	}
}

func (m *enhancedManager) TokenSum() (token.Quantity, error) {
	unspent, err := m.tokenDB.ListUnspentTokens(context.Background())
	if err != nil {
		return nil, err
	}
	sum := unspent.Sum(TokenQuantityPrecision)

	return sum, nil
}

func (m *enhancedManager) UpdateTokens(deleted []*token.ID, added []token.UnspentToken) error {
	tx, err := m.tokenDB.NewTokenDBTransaction()
	if err != nil {
		return err
	}
	if len(deleted) > 0 {
		for _, t := range deleted {
			if err := tx.Delete(m.t.Context(), *t, "me"); err != nil {
				err2 := tx.Rollback()

				return errors.Wrapf(err, "failed to delete - while rolling back: %v", err2)
			}
		}
	}
	if len(added) > 0 {
		for _, t := range added {
			quantity, err := token.ToQuantity(t.Quantity, TokenQuantityPrecision)
			if err != nil {
				err2 := tx.Rollback()

				return errors.Wrapf(err, "failed to parse quantity - while rolling back: %v", err2)
			}
			if err := tx.StoreToken(m.t.Context(), driver.TokenRecord{
				TxID:           t.Id.TxId,
				Index:          t.Id.Index,
				IssuerRaw:      []byte{},
				OwnerRaw:       t.Owner,
				OwnerType:      "idemix",
				OwnerIdentity:  []byte{},
				Ledger:         []byte("ledger"),
				LedgerMetadata: []byte{},
				Quantity:       t.Quantity,
				Type:           t.Type,
				Amount:         quantity.ToBigInt(),
				Owner:          true,
				Auditor:        false,
				Issuer:         false,
			}, []string{"alice"}); err != nil {
				err2 := tx.Rollback()

				return errors.Wrapf(err, "failed to insert - while rolling back: %v", err2)
			}
		}
	}

	return tx.Commit()
}

// Utils

func newTxID() string {
	return fmt.Sprintf("tx%d", atomic.AddUint32(&txId, 1))
}

func parallelSelect(t *testing.T, replicas []EnhancedManager, quantities []token.Quantity) []error {
	t.Helper()

	return parallelSelectWithFilter(t, replicas, quantities, defaultTokenFilter)
}

// parallelSelectWithFilter is parallelSelect parameterized by the OwnerFilter passed to
// Select (see TestHotTokenContentionNWithFilter's doc comment for why this is needed).
func parallelSelectWithFilter(t *testing.T, replicas []EnhancedManager, quantities []token.Quantity, filter token2.OwnerFilter) []error {
	t.Helper()
	errCh := make(chan error, 100)
	errs := make([]error, 0)
	var errMu sync.Mutex
	go func() {
		errMu.Lock()
		defer errMu.Unlock()
		for err := range errCh {
			errs = append(errs, err)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(len(quantities) * len(replicas))
	for _, replica := range replicas {
		for _, quantity := range quantities {
			txID := newTxID()
			sel, err := replica.NewSelector(txID)
			require.NoError(t, err)
			go func() {
				defer utils.IgnoreErrorWithOneArg(replica.Close, txID)
				tokens, sum, err := sel.Select(t.Context(), filter, quantity.Hex(), defaultCurrency)
				if err != nil {
					errCh <- err
				} else {
					assert.NotNil(t, sum)
					change, subErr := sum.Sub(quantity)
					assert.NoError(t, subErr)
					assert.GreaterOrEqual(t, change.ToBigInt().Int64(), int64(0))
					assert.NotEmpty(t, tokens)
					assert.NoError(t, deleteTokensAndStoreChange(replica, tokens, change))
				}
				// if tokenSum, err := replica.TokenSum(); err == nil {
				// 	logger.Infof("Current sum of tokens in the DB: %s", tokenSum.Decimal())
				// }
				wg.Done()
			}()
		}
	}
	wg.Wait()
	close(errCh)
	errMu.Lock()
	defer errMu.Unlock()

	return errs
}

// parallelSelectWithSettlement is parallelSelect with a settlement step added after each
// successful Select+spend: it releases the winning transaction's locks through a real
// finality.SelectorManagerProvider chain (see TestHotTokenContentionWithSettlement's doc
// comment), rather than parallelSelect's Close, which never touches the lock table.
func parallelSelectWithSettlement(t *testing.T, replicas []EnhancedManager, quantities []token.Quantity) []error {
	t.Helper()
	errCh := make(chan error, 100)
	errs := make([]error, 0)
	var errMu sync.Mutex
	go func() {
		errMu.Lock()
		defer errMu.Unlock()
		for err := range errCh {
			errs = append(errs, err)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(len(quantities) * len(replicas))
	for _, replica := range replicas {
		sp := newRealSelectorManagerProvider(replica)
		for _, quantity := range quantities {
			txID := newTxID()
			sel, err := replica.NewSelector(txID)
			require.NoError(t, err)
			go func() {
				defer utils.IgnoreErrorWithOneArg(replica.Close, txID)
				tokens, sum, err := sel.Select(t.Context(), defaultTokenFilter, quantity.Hex(), defaultCurrency)
				if err != nil {
					errCh <- err
				} else {
					assert.NotNil(t, sum)
					change, subErr := sum.Sub(quantity)
					assert.NoError(t, subErr)
					assert.GreaterOrEqual(t, change.ToBigInt().Int64(), int64(0))
					assert.NotEmpty(t, tokens)
					assert.NoError(t, deleteTokensAndStoreChange(replica, tokens, change))
					releaseViaProvider(t, sp, txID)
				}
				wg.Done()
			}()
		}
	}
	wg.Wait()
	close(errCh)
	errMu.Lock()
	defer errMu.Unlock()

	return errs
}

// parallelSelectNoSpend is parallelSelectWithSettlement without the spend step: every winning
// request releases its lock through the real SelectorManager.Unlock path (releaseViaProvider),
// exactly as parallelSelectWithSettlement does, but never deletes the selected tokens or mints
// change - so the same token IDs remain in the pool, available to be re-locked, for the rest of
// the run. See TestStaticHotTokenContentionPareto, the only caller.
func parallelSelectNoSpend(t *testing.T, replicas []EnhancedManager, quantities []token.Quantity) []error {
	t.Helper()
	errCh := make(chan error, 100)
	errs := make([]error, 0)
	var errMu sync.Mutex
	go func() {
		errMu.Lock()
		defer errMu.Unlock()
		for err := range errCh {
			errs = append(errs, err)
		}
	}()
	var wg sync.WaitGroup
	wg.Add(len(quantities) * len(replicas))
	for _, replica := range replicas {
		sp := newRealSelectorManagerProvider(replica)
		for _, quantity := range quantities {
			txID := newTxID()
			sel, err := replica.NewSelector(txID)
			require.NoError(t, err)
			go func() {
				defer utils.IgnoreErrorWithOneArg(replica.Close, txID)
				_, _, selErr := sel.Select(t.Context(), defaultTokenFilter, quantity.Hex(), defaultCurrency)
				if selErr != nil {
					errCh <- selErr
				} else {
					releaseViaProvider(t, sp, txID)
				}
				wg.Done()
			}()
		}
	}
	wg.Wait()
	close(errCh)
	errMu.Lock()
	defer errMu.Unlock()

	return errs
}

// newRealSelectorManagerProvider wires a finality.SelectorManagerProvider whose bound TMS
// resolves to replica itself as the token2.SelectorManager - replica already satisfies that
// interface, being an EnhancedManager - so SelectorManager() returns the exact manager
// Select acquired the locks against. This exercises the real provider chain
// finality.Listener uses (finality/selector_manager.go), not a hand-rolled substitute for it.
// The bound TMSID is irrelevant: the mocked TokenManagementServiceProvider ignores its
// arguments and always returns the same tms.
func newRealSelectorManagerProvider(replica EnhancedManager) *finality.SelectorManagerProvider {
	tms := &depmock.TokenManagementServiceWithExtensions{}
	tms.SelectorManagerReturns(replica, nil)

	tmsProvider := &depmock.TokenManagementServiceProvider{}
	tmsProvider.TokenManagementServiceReturns(tms, nil)

	return finality.NewSelectorManagerProvider(tmsProvider, token2.TMSID{})
}

// releaseViaProvider resolves sp's SelectorManager and unlocks txID, mirroring
// finality.Listener's releaseLocks (finality/listener.go:195-208) - except that a failure here
// fails the test loudly, rather than being logged and swallowed the way the production listener
// deliberately does, so a regression cannot slip past this harness silently. Called from a
// goroutine spawned by parallelSelectWithSettlement (not the test's own goroutine), so it uses
// assert rather than require: require calls runtime.Goexit() on failure, which would only ever
// exit this spawned goroutine and could hang wg.Wait() instead of actually failing the test.
func releaseViaProvider(t *testing.T, sp *finality.SelectorManagerProvider, txID transaction.ID) {
	t.Helper()

	sm, err := sp.SelectorManager()
	//nolint:testifylint // require-error would conflict with go-require here: this runs on a
	// goroutine spawned by parallelSelectWithSettlement, not the test's own goroutine, so
	// require's runtime.Goexit() on failure would only exit this goroutine instead of failing
	// the test - assert is the correct choice, not an oversight.
	if !assert.NoError(t, err) || !assert.NotNil(t, sm) {
		return
	}
	assert.NoError(t, sm.Unlock(t.Context(), txID))
}

func storeTokens(m EnhancedManager, added []token.UnspentToken) error {
	return m.UpdateTokens(nil, added)
}

func deleteTokensAndStoreChange(m EnhancedManager, spentTokens []*token.ID, change token.Quantity) error {
	logger.Debugf("Deleting [%d] tokens [%s] and creating a new one with quantity %s", len(spentTokens), spentTokens, change.Decimal())
	var changeTokens []token.UnspentToken
	if change.ToBigInt().Int64() > 0 {
		changeTokens = createTokens(map[transaction.ID][]token.Quantity{
			newTxID(): {change},
		})
	}
	var lastErr error
	for range maxSpendRetries {
		if err := m.UpdateTokens(spentTokens, changeTokens); err == nil {
			return nil
		} else {
			lastErr = err
			logger.Warnf("Failed to delete tokens: %v. Retrying", err)
		}
	}

	return errors.Wrapf(lastErr, "failed to delete tokens [%s] and store change after %d retries", spentTokens, maxSpendRetries)
}

func createDefaultTokens(quantities ...token.Quantity) []token.UnspentToken {
	return createTokens(map[transaction.ID][]token.Quantity{newTxID(): quantities})
}

// createTokensWithType is createDefaultTokens, but stored under tokType instead of
// defaultCurrency - used by TestStaticHotTokenContentionPareto to make its cold pool
// structurally invisible to a Select scoped to defaultCurrency (see that function's doc
// comment for why type, not owner, is what this harness's query path actually enforces).
func createTokensWithType(tokType token.Type, quantities ...token.Quantity) []token.UnspentToken {
	txID := newTxID()
	unspentTokens := make([]token.UnspentToken, 0, len(quantities))
	for i, quantity := range quantities {
		unspentTokens = append(unspentTokens, token.UnspentToken{
			Id:       token.ID{TxId: txID, Index: uint64(i)}, // #nosec G115
			Owner:    defaultWalletOwner,
			Type:     tokType,
			Quantity: quantity.Hex(),
		})
	}

	return unspentTokens
}

func createTokens(txs map[transaction.ID][]token.Quantity) []token.UnspentToken {
	unspentTokens := make([]token.UnspentToken, 0)
	for txID, quantities := range txs {
		for i, quantity := range quantities {
			unspentTokens = append(unspentTokens, token.UnspentToken{
				Id:       token.ID{TxId: txID, Index: uint64(i)}, // #nosec G115
				Owner:    defaultWalletOwner,
				Type:     defaultCurrency,
				Quantity: quantity.Hex(),
			})
		}
	}

	return unspentTokens
}

func newToken(quantity int) token.Quantity {
	return token.NewQuantityFromUInt64(uint64(quantity)) // #nosec G115
}
