/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

var logger = logging.MustGetLogger()

// NewSelectorManager decorates inner so that every selection request it hands out is
// metered by limiter, keyed by the (TMS, wallet) pair. Passing a nil limiter returns
// inner unchanged (no throttling).
//
// tmsID is the string form of the TMS identifier (e.g. tms.ID().String()). It is
// prepended to the wallet ID so that wallets with the same name in different TMSs do not
// share a bucket and cannot interfere with each other.
//
// The decoration sits at the selection-request level on purpose: a single Select call
// may attempt to lock a candidate token many times (both drivers re-walk the candidate
// list on every internal retry), and those attempts are driven by contention, not by
// the caller. Charging per lock attempt would let one legitimate request consume the
// whole per-wallet budget, so the limiter is consulted exactly once per Select.
func NewSelectorManager(inner token.SelectorManager, limiter Limiter, tmsID string) token.SelectorManager {
	if limiter == nil {
		return inner
	}

	return &selectorManager{SelectorManager: inner, limiter: limiter, tmsID: tmsID}
}

// selectorManager wraps every selector produced by the embedded manager with the
// per-wallet limiter. Unlock and Close are inherited unchanged: releasing tokens must
// never be throttled.
type selectorManager struct {
	token.SelectorManager
	limiter Limiter
	tmsID   string
}

// NewSelector returns a selector whose Select is metered by the limiter.
func (m *selectorManager) NewSelector(id string) (token.Selector, error) {
	s, err := m.SelectorManager.NewSelector(id)
	if err != nil {
		return nil, err
	}

	return &selector{Selector: s, limiter: m.limiter, tmsID: m.tmsID}, nil
}

// selector meters Select calls for the wallet the tokens are selected for.
type selector struct {
	token.Selector
	limiter Limiter
	tmsID   string
}

// Select consults the limiter for the selecting wallet and, if the request is allowed,
// delegates to the wrapped selector. On denial it returns the limiter's error (which
// wraps token.SelectorRateLimited) without touching the wrapped selector, so a throttled
// request queries no storage and locks no tokens.
func (s *selector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	// A nil owner filter carries no wallet id to key on; let the wrapped selector
	// report the error it already reports for this case.
	if ownerFilter != nil {
		// Key on (tmsID, walletID) so wallets with the same name in different TMSs do
		// not share a bucket and cannot throttle each other. An empty wallet ID carries
		// no per-wallet policy (same rule as in Allow itself), so the limiter is not
		// consulted in that case.
		if id := ownerFilter.ID(); id != "" {
			if err := s.limiter.Allow(s.tmsID + "\x00" + id); err != nil {
				// A throttled selection never reaches the wrapped selector, so it is absent
				// from the selector's own metrics: without this line the only symptom
				// visible on the node is selection volume quietly dropping.
				logger.Debugf("selection for wallet [%s] on [%s] throttled: %s", id, s.tmsID, err)

				return nil, nil, err
			}
		}
	}

	return s.Selector.Select(ctx, ownerFilter, q, tokenType)
}
