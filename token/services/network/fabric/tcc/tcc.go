/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger/fabric-chaincode-go/v2/shim"
	pb "github.com/hyperledger/fabric-protos-go-apiv2/peer"
)

var logger = logging.MustGetLogger()

const (
	InvokeFunction            = "invoke"
	QueryPublicParamsFunction = "queryPublicParams"
	QueryTokensFunctions      = "queryTokens"
	AreTokensSpent            = "areTokensSpent"
	QueryStates               = "queryStates"

	PublicParamsPathVarEnv = "PUBLIC_PARAMS_FILE_PATH"
)

type SetupAction struct {
	SetupParameters []byte
}

func (a *SetupAction) GetSetupParameters() ([]byte, error) {
	return a.SetupParameters, nil
}

//go:generate counterfeiter -o mock/validator.go -fake-name Validator . Validator

type Validator interface {
	UnmarshallAndVerifyWithMetadata(ctx context.Context, ledger token.Ledger, anchor token.RequestAnchor, raw []byte) ([]any, map[string][]byte, error)
}

//go:generate counterfeiter -o mock/public_parameters_manager.go -fake-name PublicParametersManager . PublicParametersManager

type PublicParameters interface {
	GraphHiding() bool
}

type TokenChaincode struct {
	// initMutex serializes lazy initialization of Validator and PublicParameters.
	initMutex sync.Mutex
	// initialized reports whether a previous initialization attempt completed successfully.
	// A failed attempt leaves it false so that a later call can retry.
	initialized      bool
	Validator        Validator
	PublicParameters PublicParameters

	PPDigest             []byte
	TokenServicesFactory func([]byte) (PublicParameters, Validator, error)
}

func (cc *TokenChaincode) Init(stub shim.ChaincodeStubInterface) *pb.Response {
	logger.Debugf("init token chaincode...")

	ppRaw, err := cc.Params(Params)
	if err != nil {
		return shim.Error(fmt.Sprintf("failed to get public parameters: %s", err))
	}

	w := translator.New(stub.GetTxID(), translator.NewRWSetWrapper(&rwsWrapper{stub: stub}, "", stub.GetTxID()), &keys.Translator{})
	if err := w.Write(context.Background(), &SetupAction{SetupParameters: ppRaw}); err != nil {
		return shim.Error(err.Error())
	}

	return shim.Success(nil)
}

func (cc *TokenChaincode) Invoke(stub shim.ChaincodeStubInterface) (res *pb.Response) {
	txID := stub.GetTxID()
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[%s] invoke triggered panic: %s\n%s\n", txID, r, string(debug.Stack()))
			res = shim.Error(fmt.Sprintf("failed responding [%s]", r))
		} else {
			if res.Status == 200 {
				logger.Debugf("[%s] OK", txID)
			} else {
				logger.Errorf("[%s] %d: %s", txID, res.Status, res.Message)
			}
		}
	}()

	args := stub.GetArgs()
	switch l := len(args); l {
	case 0:
		return shim.Error("missing parameters")
	default:
		logger.Debugf("[%s] %s", txID, string(args[0]))
		switch f := string(args[0]); f {
		case InvokeFunction:
			if len(args) != 1 {
				return shim.Error("empty token request")
			}
			// extract token request from transient
			t, err := stub.GetTransient()
			if err != nil {
				return shim.Error("failed getting transient")
			}
			tokenRequest, ok := t["token_request"]
			if !ok {
				return shim.Error("failed getting token request, entry not found")
			}

			return cc.ProcessRequest(tokenRequest, stub)
		case QueryPublicParamsFunction:
			return cc.QueryPublicParams(stub)
		case QueryTokensFunctions:
			if len(args) != 2 {
				return shim.Error("request to retrieve tokens is empty")
			}

			return cc.QueryTokens(args[1], stub)
		case AreTokensSpent:
			if len(args) != 2 {
				return shim.Error("request to check if tokens are spent is empty")
			}

			return cc.AreTokensSpent(args[1], stub)
		case QueryStates:
			if len(args) != 2 {
				return shim.Error("request to query states is empty")
			}

			return cc.QueryStates(args[1], stub)
		default:
			return shim.Error(fmt.Sprintf("function [%s] not recognized", f))
		}
	}
}

func (cc *TokenChaincode) Params(builtInParams string) ([]byte, error) {
	params := cc.ReadParamsFromFile()
	if params == "" {
		if len(builtInParams) == 0 {
			return nil, errors.New("no params provided")
		} else {
			params = builtInParams
		}
	}

	ppRaw, err := base64.StdEncoding.DecodeString(params)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed decoding params [%s]", params)
	}

	return ppRaw, nil
}

// GetValidator returns the validator, initializing the chaincode on first use.
// Initialization runs at most once successfully; if an attempt fails, the error is returned to
// the caller and a later call retries, so a transient failure (for example a momentarily
// unreadable public parameters file) does not permanently disable the chaincode. In particular,
// GetValidator never reports success with a nil validator.
func (cc *TokenChaincode) GetValidator(builtInParams string) (Validator, error) {
	cc.initMutex.Lock()
	defer cc.initMutex.Unlock()

	if !cc.initialized {
		if err := cc.Initialize(builtInParams); err != nil {
			return nil, errors.WithMessage(err, "failed initializing token chaincode")
		}
		cc.initialized = true
	}

	return cc.Validator, nil
}

func (cc *TokenChaincode) Initialize(builtInParams string) error {
	logger.Debugf("reading public parameters...")

	ppRaw, err := cc.Params(builtInParams)
	if err != nil {
		return errors.WithMessagef(err, "failed reading public parameters")
	}

	logger.Debugf("instantiate public parameter manager and validator...")
	ppm, validator, err := cc.TokenServicesFactory(ppRaw)
	logger.Debugf("instantiate public parameter manager and validator done with err [%v]", err)
	if err != nil {
		return errors.Wrap(err, "failed to instantiate public parameter manager and validator")
	}
	cc.PublicParameters = ppm
	cc.Validator = validator

	return nil
}

func (cc *TokenChaincode) ReadParamsFromFile() string {
	publicParamsPath := os.Getenv(PublicParamsPathVarEnv)
	if publicParamsPath == "" {
		logger.Errorf("no PUBLIC_PARAMS_FILE_PATH provided")

		return ""
	}

	logger.Debugf("reading %s ...", publicParamsPath)
	paramsAsBytes, err := os.ReadFile(filepath.Clean(publicParamsPath)) // #nosec G703 the user can specify any path here
	if err != nil {
		logger.Errorf(
			"unable to read file %s (%s). continue looking pub params from init args or cc\n", publicParamsPath, err.Error(),
		)

		return ""
	}

	return base64.StdEncoding.EncodeToString(paramsAsBytes)
}

func (cc *TokenChaincode) ProcessRequest(raw []byte, stub shim.ChaincodeStubInterface) *pb.Response {
	validator, err := cc.GetValidator(Params)
	if err != nil {
		return shim.Error(err.Error())
	}

	// Verify
	actions, attributes, err := validator.UnmarshallAndVerifyWithMetadata(
		context.Background(),
		&ledger{stub: stub, keyTranslator: &keys.Translator{}},
		token.RequestAnchor(stub.GetTxID()),
		raw,
	)
	if err != nil {
		return shim.Error("failed to verify token request: " + err.Error())
	}

	// Write
	w := translator.New(stub.GetTxID(), translator.NewRWSetWrapper(&rwsWrapper{stub: stub}, "", stub.GetTxID()), &keys.Translator{})
	ctx := context.Background()
	for _, action := range actions {
		err = w.Write(ctx, action)
		if err != nil {
			return shim.Error("failed to write token action: " + err.Error())
		}
	}
	err = w.AddPublicParamsDependency()
	if err != nil {
		return shim.Error("failed to add public params dependency: " + err.Error())
	}
	_, err = w.CommitTokenRequest(attributes[common.TokenRequestToSign], true)
	if err != nil {
		return shim.Error("failed to write token request: " + err.Error())
	}

	return shim.Success(nil)
}

func (cc *TokenChaincode) QueryPublicParams(stub shim.ChaincodeStubInterface) *pb.Response {
	w := translator.New(stub.GetTxID(), translator.NewRWSetWrapper(&rwsWrapper{stub: stub}, "", stub.GetTxID()), &keys.Translator{})
	raw, err := w.ReadSetupParameters()
	if err != nil {
		return shim.Error("failed to retrieve public parameters: " + err.Error())
	}
	if len(raw) == 0 {
		return shim.Error("need to initialize public parameters")
	}

	logger.Debugf("query public params, size [%d]", len(raw))

	return shim.Success(raw)
}

// QueryTokens returns the raw state of the tokens whose ids are JSON-encoded in idsRaw.
// idsRaw is caller-controlled: it must be a JSON array of token ids, each of them non-null
// and carrying a non-empty tx id, otherwise a validation error is returned.
func (cc *TokenChaincode) QueryTokens(idsRaw []byte, stub shim.ChaincodeStubInterface) *pb.Response {
	var ids []*token2.ID
	if err := json.Unmarshal(idsRaw, &ids); err != nil {
		logger.Errorf("failed unmarshalling tokens ids: [%s]", err)

		return shim.Error(err.Error())
	}

	// A JSON array can carry null elements, which decode into nil *token2.ID pointers without
	// error. The translator dereferences every element, so reject them here with a plain
	// validation error instead of relying on Invoke's top-level recover. An empty tx id is
	// never a valid token id either: it does not panic, but it costs a pointless state read
	// and reports itself as a missing composite key, so reject it up front too.
	// Invoke logs every non-200 response, so these do not log on their own.
	for i, id := range ids {
		switch {
		case id == nil:
			return shim.Error(fmt.Sprintf("invalid token id at position [%d]: null", i))
		case len(id.TxId) == 0:
			return shim.Error(fmt.Sprintf("invalid token id at position [%d]: empty tx id", i))
		}
	}

	logger.Debugf("query tokens [%v]...", ids)

	w := translator.New(
		stub.GetTxID(),
		translator.NewRWSetWrapper(&rwsWrapper{stub: stub}, "", stub.GetTxID()),
		&keys.Translator{},
	)
	res, err := w.QueryTokens(context.Background(), ids)
	if err != nil {
		logger.Errorf("failed query tokens [%v]: [%s]", ids, err)

		return shim.Error(fmt.Sprintf("failed query tokens [%v]: [%s]", ids, err))
	}
	raw, err := json.Marshal(res)
	if err != nil {
		logger.Errorf("failed marshalling tokens: [%s]", err)

		return shim.Error(fmt.Sprintf("failed marshalling tokens: [%s]", err))
	}

	return shim.Success(raw)
}

func (cc *TokenChaincode) AreTokensSpent(idsRaw []byte, stub shim.ChaincodeStubInterface) *pb.Response {
	_, err := cc.GetValidator(Params)
	if err != nil {
		return shim.Error(err.Error())
	}

	var ids []string
	if err := json.Unmarshal(idsRaw, &ids); err != nil {
		logger.Errorf("failed unmarshalling tokens ids: [%s]", err)

		return shim.Error(err.Error())
	}

	logger.Debugf("check if tokens are spent [%v]...", ids)

	w := translator.New(stub.GetTxID(), translator.NewRWSetWrapper(&rwsWrapper{stub: stub}, "", stub.GetTxID()), &keys.Translator{})
	res, err := w.AreTokensSpent(context.Background(), ids, cc.PublicParameters.GraphHiding())
	if err != nil {
		logger.Errorf("failed to check if tokens are spent [%v]: [%s]", ids, err)

		return shim.Error(fmt.Sprintf("failed to check if tokens are spent [%v]: [%s]", ids, err))
	}
	raw, err := json.Marshal(res)
	if err != nil {
		logger.Errorf("failed marshalling spent flags: [%s]", err)

		return shim.Error(fmt.Sprintf("failed marshalling spent flags: [%s]", err))
	}

	return shim.Success(raw)
}

func (cc *TokenChaincode) QueryStates(idsRaw []byte, stub shim.ChaincodeStubInterface) *pb.Response {
	var keys []string
	if err := json.Unmarshal(idsRaw, &keys); err != nil {
		logger.Errorf("failed unmarshalling tokens ids: [%s]", err)

		return shim.Error(err.Error())
	}

	logger.Debugf("query state for keys [%v]...", keys)
	values := make([][]byte, 0, len(keys))
	for _, key := range keys {
		value, err := stub.GetState(key)
		if err != nil {
			logger.Debugf("failed querying state [%s]: [%s]", key, err)
		}
		values = append(values, value)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		logger.Errorf("failed marshalling values: [%s]", err)

		return shim.Error(fmt.Sprintf("failed marshalling values: [%s]", err))
	}

	return shim.Success(raw)
}

type ledger struct {
	stub          shim.ChaincodeStubInterface
	keyTranslator translator.KeyTranslator
}

func (l *ledger) GetState(id token2.ID) ([]byte, error) {
	key, err := l.keyTranslator.CreateOutputKey(id.TxId, id.Index)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting token key for [%v]", id)
	}

	return l.stub.GetState(key)
}
