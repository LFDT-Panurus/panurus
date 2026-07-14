/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package user

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	txgen "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
)

type Provider interface {
	Get(alias model.UserAlias) User
}

type User interface {
	Username() model.Username
	InitiateTransfer(value txgen.Amount, nonce txgen.UUID) txgen.Error
	Transfer(value txgen.Amount, recipient model.Username, nonce txgen.UUID) txgen.Error
	Withdraw(value txgen.Amount) txgen.Error
	GetBalance() (txgen.Amount, txgen.Error)
}
