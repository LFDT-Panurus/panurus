/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package translator

import (
	"context"
	"crypto/sha256"
	"slices"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	logger   = logging.MustGetLogger()
	NotEmpty = []byte{1}
)

// Translator validates token requests and generates the corresponding RWSets
type Translator struct {
	RWSet         ExRWSet
	KeyTranslator KeyTranslator
	TxID          string
	// SpentIDs the spent IDs added so far
	SpentIDs []string
	counter  uint64
}

func New(txID string, rws ExRWSet, keyTranslator KeyTranslator) *Translator {
	w := &Translator{
		RWSet:         rws,
		TxID:          txID,
		counter:       0,
		KeyTranslator: keyTranslator,
	}

	return w
}

// Write checks that transactions are correct wrt. the most recent rwset state.
// Write checks are ones that shall be done sequentially, since transactions within a block may introduce dependencies.
func (t *Translator) Write(ctx context.Context, action any) error {
	logger.DebugfContext(ctx, "checking transaction with txID '%s'", t.TxID)

	err := t.checkProcess(action)
	if err != nil {
		return err
	}

	logger.DebugfContext(ctx, "committing transaction with txID '%s'", t.TxID)
	err = t.commitProcess(ctx, action)
	if err != nil {
		logger.Errorf("error committing transaction with txID '%s': %s", t.TxID, err)

		return err
	}
	logger.DebugfContext(ctx, "successfully processed transaction with txID '%s'", t.TxID)

	return nil
}

func (t *Translator) CommitTokenRequest(raw []byte, storeHash bool) ([]byte, error) {
	key, err := t.KeyTranslator.CreateTokenRequestKey(t.TxID)
	if err != nil {
		return nil, errors.Errorf("can't create for token request '%s'", t.TxID)
	}
	if err := t.RWSet.StateMustNotExist(key); err != nil {
		return nil, errors.Wrapf(err, "failed to read token request")
	}
	var h []byte
	if storeHash {
		hash := sha256.New()
		n, err := hash.Write(raw)
		if n != len(raw) {
			return nil, errors.Errorf("failed to write token request, hash failure '%s'", t.TxID)
		}
		if err != nil {
			return nil, errors.Wrapf(err, "failed to write token request, hash failure '%s'", t.TxID)
		}
		raw = hash.Sum(nil)
		h = raw
	}
	err = t.RWSet.SetState(key, raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to write token request'%s'", t.TxID)
	}

	return h, nil
}

func (t *Translator) ReadTokenRequest() ([]byte, error) {
	key, err := t.KeyTranslator.CreateTokenRequestKey(t.TxID)
	if err != nil {
		return nil, errors.Errorf("can't create for token request '%s'", t.TxID)
	}
	tr, err := t.RWSet.GetState(key)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read token request'%s'", t.TxID)
	}

	return tr, nil
}

func (t *Translator) ReadSetupParameters() ([]byte, error) {
	setupKey, err := t.KeyTranslator.CreateSetupKey()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create setup key")
	}
	raw, err := t.RWSet.GetState(setupKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get setup parameters")
	}

	return raw, nil
}

func (t *Translator) AddPublicParamsDependency() error {
	setupKey, err := t.KeyTranslator.CreateSetupHashKey()
	if err != nil {
		return errors.Wrapf(err, "failed to create setup key")
	}
	if err := t.RWSet.StateMustExist(setupKey, Latest); err != nil {
		return errors.Wrapf(err, "failed to add public params dependency")
	}

	return nil
}

func (t *Translator) QueryTokens(ctx context.Context, ids []*token.ID) ([][]byte, error) {
	var res [][]byte
	var errs []error
	for _, id := range ids {
		outputID, err := t.KeyTranslator.CreateOutputKey(id.TxId, id.Index)
		if err != nil {
			errs = append(errs, errors.Errorf("error creating output ID: %s", err))

			continue
			// return nil, errors.Errorf("error creating output ID: %s", err)
		}
		logger.DebugfContext(ctx, "query state [%s:%s]", id, outputID)
		bytes, err := t.RWSet.GetState(outputID)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed getting output for [%s]", outputID))

			continue
		}
		if len(bytes) == 0 {
			errs = append(errs, errors.Errorf("output for key [%s] does not exist", outputID))

			continue
		}
		res = append(res, bytes)
	}
	if len(errs) != 0 {
		return nil, errors.Errorf("failed quering tokens [%v] with errs [%d][%v]", ids, len(errs), errs)
	}

	return res, nil
}

func (t *Translator) GetTransferMetadataSubKey(k string) (string, error) {
	return t.KeyTranslator.GetTransferMetadataSubKey(k)
}

func (t *Translator) AreTokensSpent(ctx context.Context, ids []string, graphHiding bool) ([]bool, error) {
	res := make([]bool, len(ids))
	if graphHiding {
		for i, id := range ids {
			logger.DebugfContext(ctx, "check serial number %s\n", id)
			k, err := t.KeyTranslator.CreateInputSNKey(id)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to generate key for id [%s]", id)
			}
			v, err := t.RWSet.GetState(k)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get serial number %s", id)
			}
			res[i] = len(v) != 0
		}
	} else {
		for i, id := range ids {
			logger.DebugfContext(ctx, "check state %s\n", id)
			v, err := t.RWSet.GetState(id)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get output %s", id)
			}
			res[i] = len(v) == 0
		}
	}

	return res, nil
}

func (t *Translator) checkProcess(action any) error {
	if err := t.checkAction(action); err != nil {
		return err
	}

	return nil
}

func (t *Translator) checkAction(tokenAction any) error {
	switch action := tokenAction.(type) {
	case IssueAction:
		return t.checkIssue(action)
	case TransferAction:
		return t.checkTransfer(action)
	case SetupAction:
		return nil
	default:
		return errors.Errorf("unknown token action: %T", action)
	}
}

func (t *Translator) checkIssue(issueAction IssueAction) error {
	// check inputs
	if err := t.checkInputs(issueAction); err != nil {
		return err
	}

	// check outputs
	// as long as the transaction id is unique, there is nothing to check here

	return nil
}

func (t *Translator) checkTransfer(transferAction TransferAction) error {
	// check inputs
	if err := t.checkInputs(transferAction); err != nil {
		return err
	}

	// check outputs
	// as long as the transaction id is unique, there is nothing to check here

	return nil
}

func (t *Translator) commitProcess(ctx context.Context, action any) error {
	logger.DebugfContext(ctx, "committing action with txID '%s'", t.TxID)
	err := t.commitAction(ctx, action)
	if err != nil {
		logger.Errorf("error committing action with txID '%s': %s", t.TxID, err)

		return err
	}

	logger.DebugfContext(ctx, "action with txID '%s' committed successfully", t.TxID)

	return nil
}

func (t *Translator) commitAction(ctx context.Context, tokenAction any) (err error) {
	switch action := tokenAction.(type) {
	case IssueAction:
		err = t.commitIssueAction(ctx, action)
	case TransferAction:
		err = t.commitTransferAction(ctx, action)
	case SetupAction:
		err = t.commitSetupAction(action)
	}

	return
}

func (t *Translator) commitSetupAction(setup SetupAction) error {
	raw, err := setup.GetSetupParameters()
	if err != nil {
		return err
	}
	setupKey, err := t.KeyTranslator.CreateSetupKey()
	if err != nil {
		return err
	}
	err = t.RWSet.SetState(setupKey, raw)
	if err != nil {
		return err
	}

	setupHashKey, err := t.KeyTranslator.CreateSetupHashKey()
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := hash.Write(raw)
	if n != len(raw) {
		panic("hash failure")
	}
	if err != nil {
		panic(err)
	}
	digest := hash.Sum(nil)

	err = t.RWSet.SetState(setupHashKey, digest)
	if err != nil {
		return err
	}

	return nil
}

func (t *Translator) commitIssueAction(ctx context.Context, issueAction IssueAction) error {
	base := t.counter
	graphNonHiding := !issueAction.IsGraphHiding()

	// store outputs
	outputs, err := issueAction.GetSerializedOutputs()
	if err != nil {
		return err
	}
	for i, output := range outputs {
		// store output
		outputID, err := t.KeyTranslator.CreateOutputKey(t.TxID, base+uint64(i)) // #nosec G115
		if err != nil {
			return errors.Errorf("error creating output ID: %s", err)
		}
		if err := t.RWSet.SetState(outputID, output); err != nil {
			return err
		}
		if graphNonHiding {
			// store also the serial number of this output.
			// the serial number is used to check that the token exists at time of spending
			sn, err := t.KeyTranslator.CreateOutputSNKey(t.TxID, base+uint64(i), output) // #nosec G115
			if err != nil {
				return errors.Errorf("error creating output ID: %s", err)
			}
			if err := t.RWSet.SetState(sn, NotEmpty); err != nil {
				return err
			}
		}
	}

	// spend inputs
	err = t.spendInputs(ctx, issueAction)
	if err != nil {
		return err
	}

	// store metadata
	metadata := issueAction.GetMetadata()
	for key, value := range metadata {
		k, err := t.KeyTranslator.CreateIssueActionMetadataKey(key)
		if err != nil {
			return errors.Wrapf(err, "failed constructing metadata key")
		}
		if err := t.RWSet.StateMustNotExist(k); err != nil {
			return errors.Errorf("entry with issue metadata key [%s] is already occupied", key)
		}
		if err := t.RWSet.SetState(k, value); err != nil {
			return err
		}
	}

	t.counter = t.counter + uint64(len(outputs))

	return nil
}

// commitTransferAction is called for both transfer and redeem transactions
// Check the owner of each output to determine how to generate the key
func (t *Translator) commitTransferAction(ctx context.Context, transferAction TransferAction) error {
	base := t.counter
	graphNonHiding := !transferAction.IsGraphHiding()

	// store outputs
	for i := range transferAction.NumOutputs() {
		if !transferAction.IsRedeemAt(i) {
			// store output
			output, err := transferAction.SerializeOutputAt(i)
			if err != nil {
				return errors.Wrapf(err, "error serializing transfer output at index [%d]", i)
			}
			outputID, err := t.KeyTranslator.CreateOutputKey(t.TxID, base+uint64(i)) // #nosec G115
			if err != nil {
				return errors.Errorf("error creating output ID: %s", err)
			}
			err = t.RWSet.SetState(outputID, output)
			if err != nil {
				return err
			}
			if graphNonHiding {
				// store also the serial number of this output.
				// the serial number is used to check that the token exists at time of spending
				sn, err := t.KeyTranslator.CreateOutputSNKey(t.TxID, base+uint64(i), output) // #nosec G115
				if err != nil {
					return errors.Errorf("error creating output ID: %s", err)
				}
				if err := t.RWSet.SetState(sn, NotEmpty); err != nil {
					return err
				}
			}
		}
	}

	// spend inputs
	err := t.spendInputs(ctx, transferAction)
	if err != nil {
		return err
	}

	// store metadata
	metadata := transferAction.GetMetadata()
	for key, value := range metadata {
		k, err := t.KeyTranslator.CreateTransferActionMetadataKey(key)
		if err != nil {
			return errors.Wrapf(err, "failed constructing metadata key")
		}
		if err := t.RWSet.StateMustNotExist(k); err != nil {
			return errors.Errorf("entry with transfer metadata key [%s] is already occupied", key)
		}
		if err := t.RWSet.SetState(k, value); err != nil {
			return err
		}
	}

	t.counter = t.counter + uint64(transferAction.NumOutputs()) // #nosec G115

	return nil
}

func (t *Translator) checkInputs(action ActionWithInputs) error {
	// we must check that the serial number does not exist, if any are in the action
	for _, key := range action.GetSerialNumbers() {
		if err := t.RWSet.StateMustNotExist(key); err != nil {
			return errors.Wrapf(err, "invalid transfer: serial number must not exist")
		}
	}

	// we must check that the serial number for serialized inputs must exist, if any are in the action
	inputs := action.GetInputs()
	serializedInputs, err := action.GetSerializedInputs()
	if err != nil {
		return errors.Wrapf(err, "failed to get serialized inputs")
	}
	if len(serializedInputs) != len(inputs) {
		return errors.Errorf("inputs and serialized inputs length mismatch")
	}
	for i, input := range inputs {
		key, err := t.KeyTranslator.CreateOutputSNKey(input.TxId, input.Index, serializedInputs[i])
		if err != nil {
			return errors.Wrapf(err, "invalid transfer: failed creating output ID [%v]", input)
		}
		if err := t.RWSet.StateMustExist(key, VersionZero); err != nil {
			return errors.Wrapf(err, "invalid transfer: input must exist")
		}
	}

	return nil
}

func (t *Translator) spendInputs(ctx context.Context, action ActionWithInputs) error {
	// we need to delete the serial numbers and the outputs, if any
	// recall that the read dependencies are added during the checking phase
	ids := action.GetInputs()
	if len(ids) != 0 {
		serializedInputs, err := action.GetSerializedInputs()
		if err != nil {
			return errors.Wrap(err, "error serializing transfer inputs")
		}
		for i, input := range ids {
			// delete serial number
			id, err := t.KeyTranslator.CreateOutputSNKey(input.TxId, input.Index, serializedInputs[i])
			if err != nil {
				return errors.Wrapf(err, "invalid transfer: failed creating output ID [%v]", input)
			}
			logger.DebugfContext(ctx, "delete serial number [%s]\n", id)
			if err := t.RWSet.DeleteState(id); err != nil {
				return errors.Wrapf(err, "failed to delete output %s", id)
			}
			// delete token
			id, err = t.KeyTranslator.CreateOutputKey(input.TxId, input.Index)
			if err != nil {
				return errors.Wrapf(err, "invalid transfer: failed creating output ID [%v]", input)
			}
			logger.DebugfContext(ctx, "delete serial number [%s]\n", id)
			if err := t.RWSet.DeleteState(id); err != nil {
				return errors.Wrapf(err, "failed to delete output %s", id)
			}

			// finalize
			if err := t.appendSpentID(id); err != nil {
				return errors.Wrapf(err, "failed to append spent id [%s]", id)
			}
		}
	}

	// we must also write any serial number
	sns := action.GetSerialNumbers()
	for _, id := range sns {
		logger.DebugfContext(ctx, "add serial number %s\n", id)
		k, err := t.KeyTranslator.CreateInputSNKey(id)
		if err != nil {
			return errors.Wrapf(err, "failed to generate key for id [%s]", id)
		}
		if err := t.RWSet.SetState(k, NotEmpty); err != nil {
			return errors.Wrapf(err, "failed to add serial number %s", id)
		}
		if err := t.appendSpentID(id); err != nil {
			return errors.Wrapf(err, "failed to append spent id [%s]", id)
		}
	}

	return nil
}

func (t *Translator) appendSpentID(id string) error {
	// check first it is already in the list
	if slices.Contains(t.SpentIDs, id) {
		return errors.Errorf("[%s] already spent", id)
	}
	t.SpentIDs = append(t.SpentIDs, id)

	return nil
}
