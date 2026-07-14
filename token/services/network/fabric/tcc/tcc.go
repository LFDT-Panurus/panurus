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

type Agent interface {
	EmitKey(val float32, event ...string)
}

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
	initOnce         sync.Once
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

func (cc *TokenChaincode) GetValidator(builtInParams string) (Validator, error) {
	var firstInitError error
	cc.initOnce.Do(func() {
		if err := cc.Initialize(builtInParams); err != nil {
			firstInitError = err
		}
	})

	if firstInitError != nil {
		return nil, firstInitError
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

func (cc *TokenChaincode) QueryTokens(idsRaw []byte, stub shim.ChaincodeStubInterface) *pb.Response {
	var ids []*token2.ID
	if err := json.Unmarshal(idsRaw, &ids); err != nil {
		logger.Errorf("failed unmarshalling tokens ids: [%s]", err)

		return shim.Error(err.Error())
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
