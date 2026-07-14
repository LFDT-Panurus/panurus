/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package user

import (
	"crypto/rand"
	"io"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	txgen "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
)

type IntermediaryClient struct {
	logger                 logging.Logger
	userProvider           Provider
	delayAfterTransferInit time.Duration
}

func NewIntermediaryClient(userProvider Provider, logger logging.Logger, config model.IntermediaryConfig) *IntermediaryClient {
	return &IntermediaryClient{
		logger:                 logger,
		userProvider:           userProvider,
		delayAfterTransferInit: config.DelayAfterInitiation,
	}
}

/*
payment has two phases:
 1. Payee (receiver) should register the expected payment with a random nonce
 2. Payer should use this random nonce to execute the transfer
*/

func (ic *IntermediaryClient) ExecutePayment(payerName, payeeName model.UserAlias, amount txgen.Amount) (txgen.Amount, txgen.Error) {
	ic.logger.Infof("User [%s] executes a transfer to [%s] of [%d]", payerName, payeeName, amount)
	nonce := newUUID()

	payee := ic.userProvider.Get(payeeName)
	if err := payee.InitiateTransfer(amount, nonce); err != nil {
		return 0, err
	}

	// TODO understand why it should be so big
	if ic.delayAfterTransferInit > 0 {
		time.Sleep(ic.delayAfterTransferInit)
	}

	payer := ic.userProvider.Get(payerName)
	if err := payer.Transfer(amount, payee.Username(), nonce); err != nil {
		return 0, err
	}

	return amount, nil
}

func newUUID() txgen.UUID {
	var uuid txgen.UUID
	_, err := io.ReadFull(rand.Reader, uuid[:])
	if err != nil {
		panic(err)
	}

	return uuid
}

func (ic *IntermediaryClient) Withdraw(customer model.UserAlias, amount txgen.Amount) (txgen.Amount, txgen.Error) {
	ic.logger.Infof("User [%s] executes a withdrawal of [%d]", customer, amount)
	if err := ic.userProvider.Get(customer).Withdraw(amount); err != nil {
		return 0, err
	}

	return amount, nil
}

func (ic *IntermediaryClient) GetBalance(customer model.UserAlias) (txgen.Amount, txgen.Error) {
	ic.logger.Infof("User [%s] fetches balance", customer)

	return ic.userProvider.Get(customer).GetBalance()
}
