/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"runtime"
	"sync"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// maxDefaultProvers bounds the default number of presentations generated at once.
	maxDefaultProvers = 4
	// defaultIdleProvers is the default number of idle prover states a key manager keeps.
	defaultIdleProvers = 1
)

// errClosed is returned by the operations of a closed key manager.
var errClosed = errors.New("key manager closed")

// ProverLimiter bounds the number of presentations generated at the same
// time, and hence the number of prover states alive (about 10 MB each), by
// all the key managers that share it. It is safe for concurrent use.
//
// Presentations run in C: a goroutine in a cgo call does not count against
// GOMAXPROCS, so the limiter is what keeps identity generation from taking
// every core.
type ProverLimiter struct {
	slots chan struct{}
}

// NewProverLimiter returns a limiter that lets n presentations run at the same
// time. n <= 0 means DefaultProverLimit.
func NewProverLimiter(n int) *ProverLimiter {
	if n <= 0 {
		n = DefaultProverLimit()
	}

	return &ProverLimiter{slots: make(chan struct{}, n)}
}

// DefaultProverLimit returns min(GOMAXPROCS, 4). Beyond a few concurrent
// presentations the throughput grows slowly while each one costs a prover state.
func DefaultProverLimit() int {
	return min(runtime.GOMAXPROCS(0), maxDefaultProvers)
}

// acquire waits for a slot. It fails if ctx is done or done is closed first.
func (l *ProverLimiter) acquire(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return errClosed
	default:
	}
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "waiting to generate the presentation")
	case <-done:
		return errClosed
	}
}

func (l *ProverLimiter) release() { <-l.slots }

// proverPool hands out user states that can present, cloned from a template
// that holds the wallet state. A user state is mutated by every presentation,
// so each one is used by one goroutine at a time; the limiter bounds how many
// exist, the pool keeps up to maxIdle of them between calls.
//
// After close, get fails, waiters are released, the idle states and the
// template are cleared, and states still in use are cleared when put back.
type proverPool struct {
	limiter *ProverLimiter
	maxIdle int
	// done is closed by close
	done chan struct{}

	// tmplMu is held for reading while cloning and for writing to clear tmpl
	tmplMu sync.RWMutex
	tmpl   *anoncreds.UserTemplate

	// mu protects idle and closed
	mu     sync.Mutex
	idle   []*anoncreds.User
	closed bool
}

// newProverPool returns a pool cloning from tmpl, and keeping first, a state
// already cloned from it, idle. The pool owns both.
func newProverPool(tmpl *anoncreds.UserTemplate, first *anoncreds.User, limiter *ProverLimiter, maxIdle int) *proverPool {
	if limiter == nil {
		limiter = NewProverLimiter(0)
	}
	if maxIdle < 0 {
		maxIdle = defaultIdleProvers
	}
	p := &proverPool{limiter: limiter, maxIdle: maxIdle, done: make(chan struct{}), tmpl: tmpl}
	if first != nil {
		if maxIdle > 0 {
			p.idle = []*anoncreds.User{first}
		} else {
			first.Clear()
		}
	}

	return p
}

// get returns a user state for the exclusive use of the caller, which must
// give it back with put. It waits while the limiter is full.
func (p *proverPool) get(ctx context.Context) (*anoncreds.User, error) {
	if err := p.limiter.acquire(ctx, p.done); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.limiter.release()

		return nil, errClosed
	}
	if n := len(p.idle); n > 0 {
		u := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.mu.Unlock()

		return u, nil
	}
	p.mu.Unlock()

	p.tmplMu.RLock()
	u, err := p.tmpl.Clone()
	p.tmplMu.RUnlock()
	if err != nil {
		p.limiter.release()
		if p.isClosed() {
			return nil, errClosed
		}

		return nil, errors.Wrap(err, "failed to create a prover state")
	}

	return u, nil
}

// put gives back a state returned by get.
func (p *proverPool) put(u *anoncreds.User) {
	defer p.limiter.release()
	p.mu.Lock()
	if !p.closed && len(p.idle) < p.maxIdle {
		p.idle = append(p.idle, u)
		p.mu.Unlock()

		return
	}
	p.mu.Unlock()
	u.Clear()
}

func (p *proverPool) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.closed
}

// close releases the idle states and the template. It is idempotent.
func (p *proverPool) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		return
	}
	p.closed = true
	close(p.done)
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, u := range idle {
		u.Clear()
	}
	// waits for the clones in progress
	p.tmplMu.Lock()
	p.tmpl.Clear()
	p.tmplMu.Unlock()
}
