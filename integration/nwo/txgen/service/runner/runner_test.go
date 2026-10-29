/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package runner

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	txgen "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/metrics"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUser is a user.User whose balance fetch can be made to fail.
type fakeUser struct {
	name           model.Username
	balance        txgen.Amount
	balanceFailure bool
}

func (u *fakeUser) Username() model.Username { return u.name }

func (u *fakeUser) InitiateTransfer(txgen.Amount, txgen.UUID) txgen.Error { return nil }

func (u *fakeUser) Transfer(txgen.Amount, model.Username, txgen.UUID) txgen.Error { return nil }

func (u *fakeUser) Withdraw(txgen.Amount) txgen.Error { return nil }

func (u *fakeUser) GetBalance() (txgen.Amount, txgen.Error) {
	if u.balanceFailure {
		return 0, txgen.NewInternalServerError(nil, "balance not available")
	}

	return u.balance, nil
}

type fakeProvider struct {
	users map[model.UserAlias]*fakeUser
}

func (p *fakeProvider) Get(alias model.UserAlias) user.User {
	if u, ok := p.users[alias]; ok {
		return u
	}

	return &fakeUser{name: alias}
}

type testContext struct {
	runner   *BaseRunner
	provider *fakeProvider
}

func newTestContext(t *testing.T, users map[model.UserAlias]*fakeUser) *testContext {
	t.Helper()

	logger, err := logging.GetLogger("runner-test")
	require.NoError(t, err)

	provider := &fakeProvider{users: users}
	intermediary := user.NewIntermediaryClient(provider, logger, model.IntermediaryConfig{})
	reporter := metrics.NewReporter(metrics.NewMetrics(metrics.NewProvider()))

	return &testContext{
		runner:   NewBase(NewTestCaseRunner(intermediary, logger), intermediary, reporter, logger),
		provider: provider,
	}
}

func suiteWith(cases ...model.TestCase) model.SuiteConfig {
	return model.SuiteConfig{Name: "suite", PoolSize: 1, Iterations: 1, Cases: cases}
}

func testCase(payer model.UserAlias, payees ...model.UserAlias) model.TestCase {
	return model.TestCase{
		Name:     "case",
		Payer:    payer,
		Payees:   payees,
		Issue:    model.IssueConfig{Total: 100, Distribution: "const:50", Execute: true},
		Transfer: model.TransferConfig{Distribution: "const:50", Execute: true},
	}
}

func TestInitCustomerStateSkipsUsersWithUnavailableBalance(t *testing.T) {
	ctx := newTestContext(t, map[model.UserAlias]*fakeUser{
		"alice": {name: "alice", balanceFailure: true},
		"bob":   {name: "bob", balance: 42},
	})

	err := ctx.runner.initCustomerState(suiteWith(testCase("alice", "bob")))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "alice")
	assert.NotContains(t, ctx.runner.customers, model.UserAlias("alice"))
	require.Contains(t, ctx.runner.customers, model.UserAlias("bob"))
	assert.Equal(t, txgen.Amount(42), ctx.runner.customers["bob"].StartingAmount)
}

func TestInitCustomerStateDropsStaleState(t *testing.T) {
	alice := &fakeUser{name: "alice", balance: 100}
	ctx := newTestContext(t, map[model.UserAlias]*fakeUser{"alice": alice, "bob": {name: "bob"}})
	suite := suiteWith(testCase("alice", "bob"))

	ctx.runner.executeSuite(suite)
	require.Contains(t, ctx.runner.customers, model.UserAlias("alice"))

	// a later suite must not reuse the amounts accumulated by the first one
	alice.balanceFailure = true
	require.Error(t, ctx.runner.initCustomerState(suite))
	assert.NotContains(t, ctx.runner.customers, model.UserAlias("alice"))
}

func TestExecuteSuiteWithUnavailablePayerBalance(t *testing.T) {
	ctx := newTestContext(t, map[model.UserAlias]*fakeUser{
		"alice": {name: "alice", balanceFailure: true},
		"bob":   {name: "bob", balance: 42},
	})

	// the payer has no state: the suite must report a failure, not panic
	assert.NotPanics(t, func() { ctx.runner.executeSuite(suiteWith(testCase("alice", "bob"))) })
}

func TestRunWithMissingCustomers(t *testing.T) {
	customers := map[string]*customerState{
		"alice": {Name: "alice"},
		"bob":   {Name: "bob"},
	}

	for _, tc := range []struct {
		name     string
		scenario model.TestCase
		errMsg   string
	}{
		{name: "missing payer", scenario: testCase("carol", "bob"), errMsg: "no state for payer [carol]"},
		{name: "missing payee", scenario: testCase("alice", "carol"), errMsg: "no state for payees [carol]"},
		{name: "no payee", scenario: testCase("alice"), errMsg: "no payee defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestContext(t, nil)

			var result *TestCaseResult
			require.NotPanics(t, func() {
				result = ctx.runner.testCaseRunner.Run(&tc.scenario, customers, &TestCaseSettings{Iteration: 1, PoolSize: 1})
			})

			require.NotNil(t, result)
			assert.False(t, result.Success)
			require.Error(t, result.Error)
			assert.Contains(t, result.Error.Error(), tc.errMsg)
		})
	}
}

func TestRunSucceedsWithKnownCustomers(t *testing.T) {
	ctx := newTestContext(t, map[model.UserAlias]*fakeUser{
		"alice": {name: "alice", balance: 100},
		"bob":   {name: "bob"},
	})
	customers := map[string]*customerState{
		"alice": {Name: "alice"},
		"bob":   {Name: "bob"},
	}

	scenario := testCase("alice", "bob")
	result := ctx.runner.testCaseRunner.Run(&scenario, customers, &TestCaseSettings{Iteration: 1, PoolSize: 1})

	require.NotNil(t, result)
	assert.True(t, result.Success)
	require.NoError(t, result.Error)
	assert.Equal(t, txgen.Amount(100), customers["alice"].WithdrawnAmount)
	assert.Equal(t, txgen.Amount(100), customers["alice"].PaidAmount)
	assert.Equal(t, txgen.Amount(100), customers["bob"].ReceivedAmount)
}
