/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core"
	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	dlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	v1setup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	tk "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	// DefaultTestRoot is the default path to test data for token transfer verification
	DefaultTestRoot   = "../../token/core/zkatdlog/nogh/v1/regression/testdata/zero/32-BLS12_381_BBS_GURVY"
	defaultCasePrefix = "transfers_i2_o2_"
)

// TestRoot is the path to test data for token transfer verification, overridable
// via the -testRoot flag. It defaults to DefaultTestRoot. Because it is registered
// on the global flag set, it is parsed by both `go test` (for the benchmarks) and
// the client's flag.Parse call.
var TestRoot = flag.String("testRoot", DefaultTestRoot, "path to test data for token transfer verification")

type transferServiceParams struct {
	OutputPath string     `json:"test_root_path,omitempty"`
	TokenData  *TokenData `json:"proof,omitempty"`
}

func (p *transferServiceParams) PublicParamsRaw() ([]byte, error) {
	paramsTxt := filepath.Join(filepath.Dir(filepath.Dir(p.OutputPath)), "params.txt")
	raw, err := os.ReadFile(paramsTxt)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read params file %s", paramsTxt)
	}
	ppRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to base64-decode params file")
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

	testdataPath := filepath.Join(TestRootPath, "testdata.json")
	testdataRaw, err := os.ReadFile(testdataPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read testdata file %s", testdataPath)
	}

	var allCases map[string]struct {
		ReqRaw string `json:"req_raw"`
		TXID   string `json:"txid"`
	}
	if err := json.Unmarshal(testdataRaw, &allCases); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal testdata file")
	}

	keys := make([]string, 0, len(allCases))
	for key := range allCases {
		if strings.HasPrefix(key, defaultCasePrefix) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, errors.Errorf("no test cases with prefix %q found in %s", defaultCasePrefix, testdataPath)
	}
	sort.Strings(keys)

	caseFamily := strings.TrimSuffix(defaultCasePrefix, "_")
	ret := make([]*transferServiceParams, 0, len(keys))
	for _, key := range keys {
		tokenCase := allCases[key]
		reqRaw, err := base64.StdEncoding.DecodeString(tokenCase.ReqRaw)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to base64-decode req_raw for %s", key)
		}
		ret = append(ret, &transferServiceParams{
			// Synthetic path: <configDir>/<caseFamily>/<idx> for path-based helpers.
			OutputPath: filepath.Join(TestRootPath, caseFamily, strings.TrimPrefix(key, defaultCasePrefix)),
			TokenData: &TokenData{
				TokenRequestRaw: reqRaw,
				TxID:            tokenCase.TXID,
			},
		})
	}

	return ret, nil
}

type fakeLedger struct{}

func (*fakeLedger) GetState(_ tk.ID) ([]byte, error) {
	return nil, errors.New("fakeLedger.GetState is not implemented")
}

// validatorCache memoizes one validator across views. Not sync.Once: the init
// error must reach every caller, and a failure must not be memoized.
type validatorCache struct {
	mu        sync.Mutex
	validator *token.Validator
}

// get returns the memoized validator, building it on first use.
func (c *validatorCache) get(params *transferServiceParams) (*token.Validator, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.validator != nil {
		return c.validator, nil
	}

	ppRaw, err := params.PublicParamsRaw()
	if err != nil {
		return nil, err
	}
	validator, err := newTokenValidator(ppRaw)
	if err != nil {
		return nil, err
	}
	c.validator = validator

	return validator, nil
}

func newTokenValidator(ppRaw []byte) (*token.Validator, error) {
	is := core.NewValidatorDriverService(driver.DefaultResourceLimits(), fabtoken.NewValidatorDriver(), dlog.NewValidatorDriver())
	ppm, err := is.PublicParametersFromBytes(ppRaw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to deserialize public parameters")
	}
	v, err := is.NewValidator(ppm)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create validator")
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
		return nil, errors.Wrap(err, "failed to verify token request")
	}

	return nil, nil
}

// TokenValidationServiceViewFactory builds verification views sharing one
// lazily-built validator. The zero value is ready to use; use it by pointer.
type TokenValidationServiceViewFactory struct {
	validators validatorCache
}

// NewView builds a verification view.
// Wire proof embedded in the JSON params (remote/gRPC path)
func (c *TokenValidationServiceViewFactory) NewView(in []byte) (view.View, error) {
	f := &TokenValidationServiceView{}

	if err := json.Unmarshal(in, &f.params); err != nil {
		return nil, err
	}
	if f.params.TokenData != nil {
		f.tokenData = f.params.TokenData

		validator, err := c.validators.get(&f.params)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create token validator")
		}
		f.validator = validator
	}

	return f, nil
}
