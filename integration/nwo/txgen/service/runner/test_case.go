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

	funds, failed := r.resolveFunds(scenario, payer, settings)
	if failed != nil {
		return failed
	}

	withdrawAmnts, apiErr := scenario.Issue.Distribution.GetAmounts(funds)
	if apiErr != nil {
		r.logger.Errorf("Can't generate withdraw amounts: %s", apiErr.GetMessage())

		return &TestCaseResult{
			Success:   false,
			Name:      scenario.Name,
			Iteration: settings.Iteration,
			Error:     apiErr,
		}
	}
	r.logger.Infof("%d withdrawal amounts: %v", len(withdrawAmnts), withdrawAmnts)

	start := time.Now()
	r.logger.Infof("============= Start test case %s, iter %d =============", scenario.Name, settings.Iteration)

	funds, failed = r.executeWithdrawals(scenario, payer, withdrawAmnts, funds, settings)
	if failed != nil {
		return failed
	}

	transferAmnts, apiErr := scenario.Transfer.Distribution.GetAmounts(funds)
	if apiErr != nil {
		r.logger.Errorf("Can't generate transfer amounts: %s", apiErr.GetMessage())

		return &TestCaseResult{
			Success:   false,
			Name:      scenario.Name,
			Iteration: settings.Iteration,
			Error:     apiErr,
		}
	}
	r.logger.Infof("%d transfer amounts: %v", len(transferAmnts), transferAmnts)

	if scenario.Transfer.Execute {
		if failed := r.executeTransfers(scenario, customers, payer, transferAmnts, start, settings); failed != nil {
			return failed
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

// resolveFunds determines the funds available for the case's withdraw/transfer
// phases. When UseExistingFunds is set, it queries the payer's current balance
// instead of the scenario's configured issue total. A non-nil TestCaseResult means
// the case has already failed and Run should return it immediately.
func (r *TestCaseRunner) resolveFunds(scenario *model.TestCase, payer *customerState, settings *TestCaseSettings) (api.Amount, *TestCaseResult) {
	if !settings.UseExistingFunds {
		return scenario.Issue.Total, nil
	}

	r.logger.Infof("Use existing funds enabled. Check the balance of %s", payer.Name)
	currentBalance, err := r.intermediary.GetBalance(payer.Name)
	if err != nil {
		return 0, &TestCaseResult{
			Success:   false,
			Name:      scenario.Name,
			Iteration: settings.Iteration,
			Error:     err,
		}
	}
	r.logger.Infof("User [%s] has balance: [%d]", payer.Name, currentBalance)

	return currentBalance, nil
}

// executeWithdrawals runs the scenario's withdrawal phase, unless existing funds are
// being reused. On a partial failure it falls back to the payer's actual balance so
// the transfer phase only spends what was successfully withdrawn. A non-nil
// TestCaseResult means the case has already failed and Run should return it immediately.
func (r *TestCaseRunner) executeWithdrawals(scenario *model.TestCase, payer *customerState, withdrawAmnts []api.Amount, funds api.Amount, settings *TestCaseSettings) (api.Amount, *TestCaseResult) {
	if !scenario.Issue.Execute || settings.UseExistingFunds {
		return funds, nil
	}

	r.logger.Infof("Starting withdrawals")
	execErr := r.doWithdrawals(payer, withdrawAmnts, settings)
	if execErr == nil {
		return funds, nil
	}

	r.logger.Warnf("Some withdrawals failed: %v", execErr)
	funds, err := r.intermediary.GetBalance(payer.Name)
	if err != nil {
		return 0, &TestCaseResult{
			Success:   false,
			Name:      scenario.Name,
			Iteration: settings.Iteration,
			Error:     err,
		}
	}
	r.logger.Warnf("Will proceed with transfers of successfully withdrawn amount [%v]", funds)

	return funds, nil
}

// executeTransfers runs the scenario's transfer phase. A non-nil TestCaseResult means
// the case has failed and Run should return it immediately.
func (r *TestCaseRunner) executeTransfers(scenario *model.TestCase, customers map[string]*customerState, payer *customerState, transferAmnts []api.Amount, start time.Time, settings *TestCaseSettings) *TestCaseResult {
	payees := make([]*customerState, 0, len(scenario.Payees))
	for _, p := range scenario.Payees {
		// TODO introduce verification check
		payees = append(payees, customers[p])
	}

	execErr := r.doPayments(payer, payees, transferAmnts, settings)
	if execErr != nil {
		r.logger.Error(execErr)

		return &TestCaseResult{
			Name:      scenario.Name,
			Success:   false,
			Duration:  time.Since(start),
			Iteration: settings.Iteration,
			Error:     execErr,
		}
	}

	return nil
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
