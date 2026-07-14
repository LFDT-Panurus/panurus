/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	txgen "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/metrics"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/user"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/sourcegraph/conc"
)

// SuiteRunner executes test suites
type SuiteRunner interface {
	// Start initializes the users and waits for new suites
	Start(ctx context.Context) error
	// PushSuites adds new suites to the queue for execution
	PushSuites(suite ...model.SuiteConfig)
	// ShutDown waits for all suites to be executed and shuts down the runner
	ShutDown() error
}

// BaseRunner runs sequentially the suites passed using PushSuites
type BaseRunner struct {
	logger          logging.Logger
	intermediary    *user.IntermediaryClient
	testCaseRunner  *TestCaseRunner
	metricsReporter metrics.Reporter
	customers       map[string]*customerState
	suites          chan model.SuiteConfig
	shutdown        chan struct{}
	done            chan struct{}
}

func NewBase(testCaseRunner *TestCaseRunner, intermediary *user.IntermediaryClient, metricsReporter metrics.Reporter, logger logging.Logger) *BaseRunner {
	return &BaseRunner{
		logger:          logger,
		intermediary:    intermediary,
		testCaseRunner:  testCaseRunner,
		metricsReporter: metricsReporter,
		customers:       make(map[string]*customerState),
		suites:          make(chan model.SuiteConfig, 100),
		shutdown:        make(chan struct{}),
		done:            make(chan struct{}),
	}
}

func (r *BaseRunner) ShutDown() error {
	select {
	case <-r.shutdown:
		return errors.New("runner already down")
	default:
		r.logger.Infof("Sending command to shut down runner...")
		close(r.shutdown)
		r.logger.Infof("Waiting for runner to shut down...")
		<-r.done
		r.logger.Infof("Runner successfully shut down")

		return nil
	}
}

func (r *BaseRunner) PushSuites(suites ...model.SuiteConfig) {
	for _, suite := range suites {
		r.suites <- suite
	}
}

// TODO collect all transactions and verify that they are equal to stored in the system

func (r *BaseRunner) Start(ctx context.Context) error {
	r.logger.Infof("Launch throughput logger")
	go r.printTPS()

	go func() {
		defer close(r.done)
		r.logger.Infof("Start suite executions")
		for {
			select {
			case suite := <-r.suites:
				r.logger.Infof("Start new suite execution: %v", suite)
				r.executeSuite(suite)
			default:
				select {
				case suite := <-r.suites:
					r.logger.Infof("Start new suite execution: %v", suite)
					r.executeSuite(suite)
				case <-ctx.Done():
					r.logger.Infof("Context canceled. Shutting down...")
					close(r.shutdown)

					return
				case <-r.shutdown:
					r.logger.Infof("Shutting down...")

					return
				}
			}
		}
	}()

	return nil
}

func (r *BaseRunner) executeSuite(suite model.SuiteConfig) {
	r.logger.Infof("Init new customer states")
	statesBefore := len(r.customers)
	if err := r.initCustomerState(suite); err != nil {
		r.logger.Errorf("error initializing customer states: %v", err)
	}
	r.logger.Infof("Found %d new customers: %v", len(r.customers)-statesBefore, r.customers)

	r.runSuite(suite)

	r.logger.Infof("Check customer balances after suite: %v", r.customers)
	r.checkCustomerBalances()
	r.logger.Infof(r.metricsReporter.Summary())
}

func (r *BaseRunner) initCustomerState(suiteConfig model.SuiteConfig) txgen.Error {
	for _, u := range collectUsers(suiteConfig) {
		balance, err := r.intermediary.GetBalance(u)
		if err != nil {
			return err
		}
		// if _, ok := r.customers[u]; !ok {
		r.customers[u] = &customerState{Name: u, StartingAmount: balance}
		// }
	}

	return nil
}

func (r *BaseRunner) runSuite(suite model.SuiteConfig) {
	wg := conc.NewWaitGroup()
	r.logger.Infof("========================== Start suite %s ==========================", suite.Name)
	for i := range suite.Iterations {
		for _, testCase := range suite.Cases {
			wg.Go(func() {
				result := r.testCaseRunner.Run(&testCase, r.customers, &TestCaseSettings{
					Iteration:        i + 1,
					CallsDelay:       suite.Delay,
					ExecuteIssuance:  true,
					PoolSize:         suite.PoolSize,
					UseExistingFunds: suite.UseExistingFunds,
				})
				if !result.Success {
					r.logger.Errorf("Test case failed: %s", result.Error.Error())
				}
			})
		}
		wg.Wait()
	}
	r.logger.Infof("========================== End suite %s ==========================", suite.Name)
}

func collectUsers(s model.SuiteConfig) []model.UserAlias {
	usernameMap := make(map[string]struct{})

	for _, c := range s.Cases {
		for _, username := range append(c.Payees, c.Payer) {
			usernameMap[username] = struct{}{}
		}
	}

	usernames := make([]model.UserAlias, 0, len(usernameMap))
	for username := range usernameMap {
		usernames = append(usernames, username)
	}

	return usernames
}

func (r *BaseRunner) printTPS() {
	activeRequestReportingInterval := time.NewTicker(5 * time.Second)

	totalReqReportingInterval := time.NewTicker(10 * time.Second)

	for {
		select {
		case <-totalReqReportingInterval.C:
			r.logger.Infof(r.metricsReporter.GetTotalRequests())
		case <-activeRequestReportingInterval.C:
			r.logger.Infof(r.metricsReporter.GetActiveRequests())
		case <-r.shutdown:
			activeRequestReportingInterval.Stop()
			totalReqReportingInterval.Stop()
			r.logger.Infof("Quitting TPS monitoring... %s", r.metricsReporter.GetActiveRequests())

			return
		}
	}
}

func (r *BaseRunner) checkCustomerBalances() {
	totalWithdrawn := txgen.Amount(0)
	totalPaid := txgen.Amount(0)
	totalReceived := txgen.Amount(0)
	for _, c := range r.customers {
		r.logger.Infof(
			"Customer: '%s', starting amount: %d, withdrawn amount %d, paid amount %d, received amount %d",
			c.Name, c.StartingAmount, c.WithdrawnAmount, c.PaidAmount, c.ReceivedAmount,
		)

		totalWithdrawn += c.WithdrawnAmount
		totalPaid += c.PaidAmount
		totalReceived += c.ReceivedAmount
	}

	msg := fmt.Sprintf("Total withdrawn: %d, total paid %d", totalWithdrawn, totalPaid)
	if totalWithdrawn == totalPaid {
		r.logger.Infof(msg)
	} else {
		r.logger.Errorf(msg)
	}

	msg = fmt.Sprintf("Total paid: %d, total received %d", totalPaid, totalReceived)
	if totalPaid == totalReceived {
		r.logger.Infof(msg)
	} else {
		r.logger.Errorf(msg)
	}

	for _, c := range r.customers {
		balanceInSystem, err := r.intermediary.GetBalance(c.Name)
		if err != nil {
			r.logger.Errorf("Can't do balance post-analysis of user '%s'", c.Name)

			continue
		}
		r.logger.Infof("Balance of user %s: [%d]", c.Name, balanceInSystem)

		expectedBalance := c.StartingAmount + c.ReceivedAmount + c.WithdrawnAmount - c.PaidAmount

		if balanceInSystem == expectedBalance {
			r.logger.Infof("User's '%s' expected balance %d matches balance in system %d", c.Name, expectedBalance, balanceInSystem)
		} else {
			r.logger.Errorf("User's '%s' expected balance %d doesn't match balance in system %d", c.Name, expectedBalance, balanceInSystem)
		}
	}
}
