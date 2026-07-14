/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCounter tracks Add calls for assertions.
type testCounter struct {
	addCount int
	labels   []string
}

func (c *testCounter) With(labelValues ...string) Counter {
	c.labels = append(c.labels, labelValues...)

	return c
}

func (c *testCounter) Add(delta float64) {
	c.addCount++
}

// testHistogram tracks Observe calls for assertions.
type testHistogram struct {
	observeCount int
	lastValue    float64
	labels       []string
}

func (h *testHistogram) With(labelValues ...string) Histogram {
	h.labels = append(h.labels, labelValues...)

	return h
}

func (h *testHistogram) Observe(value float64) {
	h.observeCount++
	h.lastValue = value
}

// testProvider returns the test metrics so we can inspect them.
type testProvider struct {
	counter   *testCounter
	histogram *testHistogram
}

func (p *testProvider) NewCounter(opts CounterOpts) Counter       { return p.counter }
func (p *testProvider) NewGauge(opts GaugeOpts) Gauge             { return nil }
func (p *testProvider) NewHistogram(opts HistogramOpts) Histogram { return p.histogram }

func newTestProvider() *testProvider {
	return &testProvider{
		counter:   &testCounter{},
		histogram: &testHistogram{},
	}
}

var errTest = errors.New("test error")

// =============================================================================
// IssueService Tests
// =============================================================================

func TestIssueService_Issue(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.IssueService{}
		inner.IssueReturns(nil, &driver.IssueMetadata{}, nil)
		p := newTestProvider()
		w := NewIssueService(inner, p)

		_, meta, err := w.Issue(context.Background(), nil, "TOK", []uint64{100}, [][]byte{[]byte("owner")}, nil)
		require.NoError(t, err)
		assert.NotNil(t, meta)
		assert.Equal(t, 1, inner.IssueCallCount())
		assert.GreaterOrEqual(t, p.counter.addCount, 1)
		assert.Equal(t, 1, p.histogram.observeCount)
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.IssueService{}
		inner.IssueReturns(nil, nil, errTest)
		p := newTestProvider()
		w := NewIssueService(inner, p)

		_, _, err := w.Issue(context.Background(), nil, "TOK", []uint64{100}, [][]byte{[]byte("owner")}, nil)
		require.ErrorIs(t, err, errTest)
		assert.Equal(t, 1, inner.IssueCallCount())
		// calls counter + error counter = 2 adds
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestIssueService_VerifyIssue(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.IssueService{}
		inner.VerifyIssueReturns(nil)
		p := newTestProvider()
		w := NewIssueService(inner, p)

		err := w.VerifyIssue(context.Background(), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, inner.VerifyIssueCallCount())
		assert.GreaterOrEqual(t, p.counter.addCount, 1)
		assert.Equal(t, 1, p.histogram.observeCount)
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.IssueService{}
		inner.VerifyIssueReturns(errTest)
		p := newTestProvider()
		w := NewIssueService(inner, p)

		err := w.VerifyIssue(context.Background(), nil, nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestIssueService_DeserializeIssueAction(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.IssueService{}
		inner.DeserializeIssueActionReturns(nil, nil)
		p := newTestProvider()
		w := NewIssueService(inner, p)

		_, err := w.DeserializeIssueAction([]byte("raw"))
		require.NoError(t, err)
		assert.Equal(t, 1, inner.DeserializeIssueActionCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.IssueService{}
		inner.DeserializeIssueActionReturns(nil, errTest)
		p := newTestProvider()
		w := NewIssueService(inner, p)

		_, err := w.DeserializeIssueAction([]byte("raw"))
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

// =============================================================================
// TransferService Tests
// =============================================================================

func TestTransferService_Transfer(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TransferService{}
		inner.TransferReturns(nil, &driver.TransferMetadata{}, nil)
		p := newTestProvider()
		w := NewTransferService(inner, p)

		_, meta, err := w.Transfer(context.Background(), "anchor", nil, nil, nil, nil)
		require.NoError(t, err)
		assert.NotNil(t, meta)
		assert.Equal(t, 1, inner.TransferCallCount())
		assert.GreaterOrEqual(t, p.counter.addCount, 1)
		assert.Equal(t, 1, p.histogram.observeCount)
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TransferService{}
		inner.TransferReturns(nil, nil, errTest)
		p := newTestProvider()
		w := NewTransferService(inner, p)

		_, _, err := w.Transfer(context.Background(), "anchor", nil, nil, nil, nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestTransferService_VerifyTransfer(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TransferService{}
		inner.VerifyTransferReturns(nil)
		p := newTestProvider()
		w := NewTransferService(inner, p)

		err := w.VerifyTransfer(context.Background(), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, inner.VerifyTransferCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TransferService{}
		inner.VerifyTransferReturns(errTest)
		p := newTestProvider()
		w := NewTransferService(inner, p)

		err := w.VerifyTransfer(context.Background(), nil, nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestTransferService_DeserializeTransferAction(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TransferService{}
		inner.DeserializeTransferActionReturns(nil, nil)
		p := newTestProvider()
		w := NewTransferService(inner, p)

		_, err := w.DeserializeTransferAction([]byte("raw"))
		require.NoError(t, err)
		assert.Equal(t, 1, inner.DeserializeTransferActionCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TransferService{}
		inner.DeserializeTransferActionReturns(nil, errTest)
		p := newTestProvider()
		w := NewTransferService(inner, p)

		_, err := w.DeserializeTransferAction([]byte("raw"))
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

// =============================================================================
// AuditorService Tests
// =============================================================================

func TestAuditorService_AuditorCheck(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.AuditorService{}
		inner.AuditorCheckReturns(nil)
		p := newTestProvider()
		w := NewAuditorService(inner, p)

		err := w.AuditorCheck(context.Background(), nil, nil, "anchor")
		require.NoError(t, err)
		assert.Equal(t, 1, inner.AuditorCheckCallCount())
		assert.GreaterOrEqual(t, p.counter.addCount, 1)
		assert.Equal(t, 1, p.histogram.observeCount)
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.AuditorService{}
		inner.AuditorCheckReturns(errTest)
		p := newTestProvider()
		w := NewAuditorService(inner, p)

		err := w.AuditorCheck(context.Background(), nil, nil, "anchor")
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

// =============================================================================
// TokensService Tests
// =============================================================================

func TestTokensService_SupportedTokenFormats(t *testing.T) {
	inner := &mock.TokensService{}
	inner.SupportedTokenFormatsReturns([]token.Format{"fmt1"})
	p := newTestProvider()
	w := NewTokensService(inner, p)

	formats := w.SupportedTokenFormats()
	assert.Equal(t, []token.Format{"fmt1"}, formats)
	assert.Equal(t, 1, inner.SupportedTokenFormatsCallCount())
	assert.GreaterOrEqual(t, p.counter.addCount, 1)
	assert.Equal(t, 1, p.histogram.observeCount)
}

func TestTokensService_Deobfuscate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TokensService{}
		inner.DeobfuscateReturns(&token.Token{}, nil, nil, "fmt", nil)
		p := newTestProvider()
		w := NewTokensService(inner, p)

		tok, _, _, _, err := w.Deobfuscate(context.Background(), nil, nil)
		require.NoError(t, err)
		assert.NotNil(t, tok)
		assert.Equal(t, 1, inner.DeobfuscateCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TokensService{}
		inner.DeobfuscateReturns(nil, nil, nil, "", errTest)
		p := newTestProvider()
		w := NewTokensService(inner, p)

		_, _, _, _, err := w.Deobfuscate(context.Background(), nil, nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestTokensService_Recipients(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TokensService{}
		inner.RecipientsReturns([]driver.Identity{[]byte("id1")}, nil)
		p := newTestProvider()
		w := NewTokensService(inner, p)

		ids, err := w.Recipients(nil)
		require.NoError(t, err)
		assert.Len(t, ids, 1)
		assert.Equal(t, 1, inner.RecipientsCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TokensService{}
		inner.RecipientsReturns(nil, errTest)
		p := newTestProvider()
		w := NewTokensService(inner, p)

		_, err := w.Recipients(nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

// =============================================================================
// TokensUpgradeService Tests
// =============================================================================

func TestTokensUpgradeService_NewUpgradeChallenge(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TokensUpgradeService{}
		inner.NewUpgradeChallengeReturns([]byte("challenge"), nil)
		p := newTestProvider()
		w := NewTokensUpgradeService(inner, p)

		ch, err := w.NewUpgradeChallenge()
		require.NoError(t, err)
		assert.Equal(t, []byte("challenge"), ch)
		assert.Equal(t, 1, inner.NewUpgradeChallengeCallCount())
		assert.GreaterOrEqual(t, p.counter.addCount, 1)
		assert.Equal(t, 1, p.histogram.observeCount)
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TokensUpgradeService{}
		inner.NewUpgradeChallengeReturns(nil, errTest)
		p := newTestProvider()
		w := NewTokensUpgradeService(inner, p)

		_, err := w.NewUpgradeChallenge()
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestTokensUpgradeService_GenUpgradeProof(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TokensUpgradeService{}
		inner.GenUpgradeProofReturns([]byte("proof"), nil)
		p := newTestProvider()
		w := NewTokensUpgradeService(inner, p)

		proof, err := w.GenUpgradeProof(context.Background(), []byte("ch"), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, []byte("proof"), proof)
		assert.Equal(t, 1, inner.GenUpgradeProofCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TokensUpgradeService{}
		inner.GenUpgradeProofReturns(nil, errTest)
		p := newTestProvider()
		w := NewTokensUpgradeService(inner, p)

		_, err := w.GenUpgradeProof(context.Background(), []byte("ch"), nil, nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}

func TestTokensUpgradeService_CheckUpgradeProof(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inner := &mock.TokensUpgradeService{}
		inner.CheckUpgradeProofReturns(true, nil)
		p := newTestProvider()
		w := NewTokensUpgradeService(inner, p)

		ok, err := w.CheckUpgradeProof(context.Background(), []byte("ch"), []byte("proof"), nil)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, 1, inner.CheckUpgradeProofCallCount())
	})

	t.Run("error increments error counter", func(t *testing.T) {
		inner := &mock.TokensUpgradeService{}
		inner.CheckUpgradeProofReturns(false, errTest)
		p := newTestProvider()
		w := NewTokensUpgradeService(inner, p)

		_, err := w.CheckUpgradeProof(context.Background(), []byte("ch"), []byte("proof"), nil)
		require.ErrorIs(t, err, errTest)
		assert.GreaterOrEqual(t, p.counter.addCount, 2)
	})
}
