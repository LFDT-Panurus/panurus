/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/qe"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
)

var logger = logging.MustGetLogger()

// QueryStatesExecutor models an executor for querying states.
type QueryStatesExecutor interface {
	QueryStates(_ context.Context, namespace qe.Namespace, keys []string) ([]qe.Data, error)
	// QueryState returns the value of the given key in the given namespace.
	QueryState(ctx context.Context, namespace qe.Namespace, key string) (qe.Data, error)
}

// ledger models the FabricX ledger.
type ledger struct {
	l             *fabric.Ledger
	ch            *fabric.Channel
	network       string
	keyTranslator translator.KeyTranslator
	executor      QueryStatesExecutor
}

// NewLedger returns a new ledger instance for the specified Fabric channel.
// It uses the provided key translator for state keys and a query executor for state access.
func NewLedger(ch *fabric.Channel, network string, keyTranslator translator.KeyTranslator, executor QueryStatesExecutor) *ledger {
	return &ledger{
		ch:            ch,
		l:             ch.Ledger(),
		network:       network,
		keyTranslator: keyTranslator,
		executor:      executor,
	}
}

// Status returns the validation code of the transaction with the given ID.
// It retrieves the transaction from the Fabric ledger and maps its internal
// validation code to a driver.ValidationCode (Unknown, Valid, or Invalid).
func (l *ledger) Status(id string) (driver.ValidationCode, error) {
	tx, err := l.l.GetTransactionByID(id)
	if err != nil {
		return driver.Unknown, errors.Wrapf(err, "failed to get transaction [%s]", id)
	}
	logger.Debugf("ledger status of [%s] is [%d]", id, tx.ValidationCode())

	switch committerpb.Status(tx.ValidationCode()) {
	case committerpb.Status_STATUS_UNSPECIFIED:
		return driver.Unknown, nil
	case committerpb.Status_COMMITTED:
		return driver.Valid, nil
	default:
		return driver.Invalid, nil
	}
}

// GetTransactionStatus retrieves the current status and token request hash for a transaction.
func (l *ledger) GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error) {
	// Get the transaction from the ledger
	tx, err := l.l.GetTransactionByID(txID)
	if err != nil {
		return driver.Unknown, nil, "", errors.Wrapf(err, "failed to get transaction [%s]", txID)
	}

	logger.Debugf("ledger status of [%s] is [%d]", txID, tx.ValidationCode())

	// Map validation code to driver status
	validationCode := tx.ValidationCode()
	var txStatus driver.ValidationCode
	var statusMessage string
	switch committerpb.Status(validationCode) {
	case committerpb.Status_STATUS_UNSPECIFIED:
		txStatus = driver.Unknown
	case committerpb.Status_COMMITTED:
		txStatus = driver.Valid
	default:
		txStatus = driver.Invalid
		statusMessage = committerpb.Status(validationCode).String()
	}

	// If invalid or unknown, return early without token request hash
	if txStatus != driver.Valid {
		return txStatus, nil, statusMessage, nil
	}

	key, err := l.keyTranslator.CreateTokenRequestKey(txID)
	if err != nil {
		return driver.Unknown, nil, "", errors.Wrapf(err, "can't create for token request [%s]", txID)
	}
	tokenRequestHash, err = l.executor.QueryState(ctx, namespace, key)
	if err != nil {
		return driver.Unknown, nil, "", errors.Wrapf(err, "can't get state for token request [%s]", txID)
	}

	return txStatus, tokenRequestHash, statusMessage, nil
}

// GetStates returns the raw byte values of the given keys within the specified namespace.
// It delegates the query to the configured state executor.
func (l *ledger) GetStates(ctx context.Context, namespace string, keys ...string) ([][]byte, error) {
	return l.executor.QueryStates(ctx, namespace, keys)
}

// TransferMetadataKey returns the ledger key used to store metadata for a transfer action.
// It uses the key translator to generate the appropriate key for the given input.
func (l *ledger) TransferMetadataKey(k string) (string, error) {
	return l.keyTranslator.CreateTransferActionMetadataKey(k)
}
