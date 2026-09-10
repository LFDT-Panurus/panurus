/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package runner

import (
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/user"

	"github.com/sourcegraph/conc/pool"
)

type TestCaseResult struct {
	Name      string
	Iteration int
	Success   bool
	Duration  time.Duration
	Error     error
}

type TestCaseSettings struct {
	Iteration        int
	CallsDelay       time.Duration
	ExecuteIssuance  bool
	PoolSize         int
	UseExistingFunds bool
}

func NewTestCaseRunner(intermediary *user.IntermediaryClient, logger logging.Logger) *TestCaseRunner {
	return &TestCaseRunner{
		logger:       logger,
		intermediary: intermediary,
	}
}

type TestCaseRunner struct {
	logger       logging.Logger
	intermediary *user.IntermediaryClient
}

func (r *TestCaseRunner) Run(scenario *model.TestCase, customers map[string]*customerState, settings *TestCaseSettings) *TestCaseResult {
	r.logger.Infof("Starting case %s", scenario.Name)
	payer := customers[scenario.Payer]

	funds, err := r.resolveFunds(scenario, payer, settings)
	if err != nil {
		return &TestCaseResult{Success: false, Name: scenario.Name, Iteration: settings.Iteration, Error: err}
	}

	withdrawAmnts, err := scenario.Issue.Distribution.GetAmounts(funds)
	if err != nil {
		r.logger.Errorf("Can't generate withdraw amounts: %s", err.GetMessage())

		return &TestCaseResult{Success: false, Name: scenario.Name, Iteration: settings.Iteration, Error: err}
	}
	r.logger.Infof("%d withdrawal amounts: %v", len(withdrawAmnts), withdrawAmnts)

	start := time.Now()
	r.logger.Infof("============= Start test case %s, iter %d =============", scenario.Name, settings.Iteration)

	funds, err = r.executeWithdrawals(scenario, payer, funds, withdrawAmnts, settings)
	if err != nil {
		return &TestCaseResult{Success: false, Name: scenario.Name, Iteration: settings.Iteration, Error: err}
	}

	transferAmnts, err := scenario.Transfer.Distribution.GetAmounts(funds)
	if err != nil {
		r.logger.Errorf("Can't generate transfer amounts: %s", err.GetMessage())

		return &TestCaseResult{Success: false, Name: scenario.Name, Iteration: settings.Iteration, Error: err}
	}
	r.logger.Infof("%d transfer amounts: %v", len(transferAmnts), transferAmnts)

	if execErr := r.executeTransfers(scenario, payer, customers, transferAmnts, settings); execErr != nil {
		r.logger.Error(execErr)

		return &TestCaseResult{
			Name:      scenario.Name,
			Success:   false,
			Duration:  time.Since(start),
			Iteration: settings.Iteration,
			Error:     execErr,
		}
	}

	duration := time.Since(start)
	r.logger.Infof("============= Finish test case %s, iter %d, duration: %ds =============", scenario.Name, settings.Iteration, duration)

	return &TestCaseResult{
		Name:      scenario.Name,
		Success:   true,
		Duration:  duration,
		Iteration: settings.Iteration,
	}
}

// resolveFunds returns the scenario's configured issuance total, or the payer's current
// on-chain balance when the scenario is set to reuse existing funds instead of issuing new ones.
func (r *TestCaseRunner) resolveFunds(scenario *model.TestCase, payer *customerState, settings *TestCaseSettings) (api.Amount, api.Error) {
	if !settings.UseExistingFunds {
		return scenario.Issue.Total, nil
	}

	r.logger.Infof("Use existing funds enabled. Check the balance of %s", payer.Name)
	currentBalance, err := r.intermediary.GetBalance(payer.Name)
	if err != nil {
		return 0, err
	}
	r.logger.Infof("User [%s] has balance: [%d]", payer.Name, currentBalance)

	return currentBalance, nil
}

// executeWithdrawals runs the scenario's withdrawal step, if configured, and returns the funds
// to carry into the transfer step: unchanged on success, or the payer's re-fetched balance if
// some withdrawals failed and the run proceeds with only what was actually withdrawn.
func (r *TestCaseRunner) executeWithdrawals(scenario *model.TestCase, payer *customerState, funds api.Amount, withdrawAmnts []api.Amount, settings *TestCaseSettings) (api.Amount, api.Error) {
	if !scenario.Issue.Execute || settings.UseExistingFunds {
		return funds, nil
	}

	r.logger.Infof("Starting withdrawals")
	if execErr := r.doWithdrawals(payer, withdrawAmnts, settings); execErr != nil {
		r.logger.Warnf("Some withdrawals failed: %v", execErr)
		newFunds, err := r.intermediary.GetBalance(payer.Name)
		if err != nil {
			return 0, err
		}
		r.logger.Warnf("Will proceed with transfers of successfully withdrawn amount [%v]", newFunds)

		return newFunds, nil
	}

	return funds, nil
}

// executeTransfers runs the scenario's transfer step, if configured, paying transferAmnts out
// from payer to each of the scenario's resolved payees.
func (r *TestCaseRunner) executeTransfers(scenario *model.TestCase, payer *customerState, customers map[string]*customerState, transferAmnts []api.Amount, settings *TestCaseSettings) error {
	if !scenario.Transfer.Execute {
		return nil
	}

	payees := make([]*customerState, 0, len(scenario.Payees))
	for _, p := range scenario.Payees {
		// TODO introduce verification check
		payees = append(payees, customers[p])
	}

	return r.doPayments(payer, payees, transferAmnts, settings)
}

func (r *TestCaseRunner) doWithdrawals(customer *customerState, amounts []api.Amount, settings *TestCaseSettings) error {
	executorPool := pool.New().WithErrors().WithMaxGoroutines(settings.PoolSize)

	r.logger.Infof("Start withdrawals...")
	for _, amount := range amounts {
		time.Sleep(settings.CallsDelay)
		executorPool.Go(func() error {
			r.logger.Infof("Withdarwing %d for %s", amount, customer.Name)
			amount, err := r.intermediary.Withdraw(customer.Name, amount)
			if err != nil {
				return err
			}
			customer.AddWithdrawn(amount)
			balance, err := r.intermediary.GetBalance(customer.Name)
			if err != nil {
				return err
			}
			r.logger.Infof("Balance of %s is %d", customer.Name, balance)

			return nil
		})
	}

	return executorPool.Wait()
}

func (r *TestCaseRunner) doPayments(payer *customerState, payees []*customerState, amounts []api.Amount, settings *TestCaseSettings) error {
	executorPool := pool.New().WithErrors().WithMaxGoroutines(settings.PoolSize)

	r.logger.Infof("Start payments...")
	for i, amount := range amounts {
		payee := payees[i%len(payees)]
		r.logger.Infof("Paying %d from %s to %s", amount, payer.Name, payee.Name)
		time.Sleep(settings.CallsDelay)
		executorPool.Go(func() error {
			amount, err := r.intermediary.ExecutePayment(payer.Name, payee.Name, amount)
			if err != nil {
				return err
			}
			payer.AddPaidMount(amount)
			payee.AddReceivedMount(amount)

			balance, err := r.intermediary.GetBalance(payer.Name)
			if err != nil {
				return err
			}
			r.logger.Infof("Balance of %s is %d", payer.Name, balance)

			balance, err = r.intermediary.GetBalance(payee.Name)
			if err != nil {
				return err
			}
			r.logger.Infof("Balance of %s is %d", payee.Name, balance)

			return nil
		})
	}

	return executorPool.Wait()
}
