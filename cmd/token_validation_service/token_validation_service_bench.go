/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core"
	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	dlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	v1setup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	tk "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	// DefaultTestRoot is the default path to test data for token transfer verification
	DefaultTestRoot = "../../token/core/zkatdlog/nogh/v1/validator/regression/testdata/32-BLS12_381_BBS_GURVY/transfers_i2_o2"
)

var (
	once            sync.Once
	cachedValidator *token.Validator
)

type transferServiceParams struct {
	OutputPath string     `json:"test_root_path,omitempty"`
	TokenData  *TokenData `json:"proof,omitempty"`
}

func (p *transferServiceParams) PublicParamsRaw() ([]byte, error) {
	paramsTxt := filepath.Join(filepath.Dir(filepath.Dir(p.OutputPath)), "params.txt")
	raw, err := os.ReadFile(paramsTxt)
	if err != nil {
		return nil, fmt.Errorf("failed to read params file %s: %w", paramsTxt, err)
	}
	ppRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode params file: %w", err)
	}

	return ppRaw, nil
}

func (p *transferServiceParams) PublicParams() (*v1setup.PublicParams, error) {
	ppRaw, err := p.PublicParamsRaw()
	if err != nil {
		return nil, err
	}

	return v1setup.NewPublicParamsFromBytes(ppRaw, v1setup.DLogNoGHDriverName, v1setup.ProtocolV1)
}

func (p *transferServiceParams) NumInputs() int {
	subDir := filepath.Base(filepath.Dir(p.OutputPath))
	if m := regexp.MustCompile(`_i(\d+)_o(\d+)$`).FindStringSubmatch(subDir); len(m) == 3 {
		n, _ := strconv.Atoi(m[1])

		return n
	}

	return -1
}

func (p *transferServiceParams) NumOutputs() int {
	subDir := filepath.Base(filepath.Dir(p.OutputPath))
	if m := regexp.MustCompile(`_i(\d+)_o(\d+)$`).FindStringSubmatch(subDir); len(m) == 3 {
		n, _ := strconv.Atoi(m[2])

		return n
	}

	return -1
}

func (p *transferServiceParams) CurveID() string {
	dirName := filepath.Base(filepath.Dir(filepath.Dir(p.OutputPath)))
	if parts := strings.SplitN(dirName, "-", 2); len(parts) == 2 {
		return parts[1]
	}

	return ""
}

func NewTokenValidationParamsSlice(TestRootPath string) ([]*transferServiceParams, error) {
	if TestRootPath == "" {
		return nil, errors.New("TestRootPath cannot be empty")
	}
	paramsTxt := filepath.Join(filepath.Dir(TestRootPath), "params.txt")

	paramsRaw, err := os.ReadFile(paramsTxt)
	if err != nil {
		return nil, fmt.Errorf("failed to read params file %s: %w", paramsTxt, err)
	}

	ppRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(paramsRaw)))
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode params.txt: %w", err)
	}

	outPaths, err := os.ReadDir(TestRootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", TestRootPath, err)
	}
	ret := make([]*transferServiceParams, len(outPaths))
	for i, outPath := range outPaths {
		params, err := newTokenValidationParams(filepath.Join(TestRootPath, outPath.Name()), ppRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to create params for %s: %w", outPath.Name(), err)
		}
		ret[i] = params
	}

	return ret, nil
}

func newTokenValidationParams(outputPath string, ppRaw []byte) (*transferServiceParams, error) {
	outputRaw, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", outputPath, err)
	}

	var tokenData struct {
		ReqRaw []byte `json:"req_raw"`
		TXID   string `json:"txid"`
	}
	if err := json.Unmarshal(outputRaw, &tokenData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal output file: %w", err)
	}

	return &transferServiceParams{
		OutputPath: outputPath,
		TokenData: &TokenData{
			TokenRequestRaw: tokenData.ReqRaw,
			TxID:            tokenData.TXID,
		},
	}, nil
}

type fakeLedger struct{}

func (*fakeLedger) GetState(_ tk.ID) ([]byte, error) {
	return nil, errors.New("fakeLedger.GetState is not implemented")
}

func newTokenValidator(ppRaw []byte) (*token.Validator, error) {
	is := core.NewValidatorDriverService(fabtoken.NewValidatorDriver(), dlog.NewValidatorDriver())
	ppm, err := is.PublicParametersFromBytes(ppRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize public parameters: %w", err)
	}
	v, err := is.NewValidator(ppm)
	if err != nil {
		return nil, fmt.Errorf("failed to create validator: %w", err)
	}

	return token.NewValidator(v), nil
}

type TokenValidationServiceView struct {
	params    transferServiceParams
	tokenData *TokenData
	validator *token.Validator
}

// Call runs the full token request validation pipeline matching the
// regression test's UnmarshallAndVerifyWithMetadata path: auditing,
// signatures, ZK proofs, HTLC, upgrade witnesses, and metadata checks.
// Call Chain:
//   1. token.Validator.UnmarshallAndVerifyWithMetadata -> driver.Validator.VerifyTokenRequestFromRaw
//   2. VerifyTokenRequestFromRaw (from token/core/common/validator.go)
//     - deserializes the raw bytes into a TokenRequest, prepares signed message + signatures
// 	   - calls VerifyTokenRequest
//   3. VerifyTokenRequest runs three stages:
//     - Auditing validation (VerifyAuditing) [verifies auditor signatures]
//     - Issue validation (verifyIssues) [verifies issue actions]
//     - Transfer validation (verifyTransfers):
//       a. TransferActionValidate [action.Validate()]
//       b. TransferSignatureValidate [verifies sender signatures (deserializes owner identity, checks signature)]
//       c. TransferUpgradeWitnessValidate
//       d. TransferZKProofValidate [transfer.NewVerifier(in, outputCommitments, pp).Verify(proof)]
//       e. TransferHTLCValidate
//       f. TransferApplicationDataValidate [validates metadata]
//   4. After all validators pass, it checks that all metadata have been validated

func (q *TokenValidationServiceView) Call(viewCtx view.Context) (any, error) {
	if q.tokenData == nil {
		return nil, errors.New("proof data is nil")
	}

	_, _, err := q.validator.UnmarshallAndVerifyWithMetadata(
		context.Background(),
		&fakeLedger{},
		token.RequestAnchor(q.tokenData.TxID),
		q.tokenData.TokenRequestRaw,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token request: %w", err)
	}

	return nil, nil
}

type TokenValidationServiceViewFactory struct{}

// NewView builds a verification view.
// Wire proof embedded in the JSON params (remote/gRPC path)
func (c *TokenValidationServiceViewFactory) NewView(in []byte) (view.View, error) {
	f := &TokenValidationServiceView{}

	if err := json.Unmarshal(in, &f.params); err != nil {
		return nil, err
	}
	if f.params.TokenData != nil {
		f.tokenData = f.params.TokenData

		var initErr error
		once.Do(func() {
			ppRaw, err := f.params.PublicParamsRaw()
			if err != nil {
				initErr = err
			} else {
				cachedValidator, initErr = newTokenValidator(ppRaw)
			}
		})
		if initErr != nil {
			return nil, fmt.Errorf("failed to create token validator: %w", initErr)
		}
		f.validator = cachedValidator
	}

	return f, nil
}
