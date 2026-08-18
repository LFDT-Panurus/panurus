/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writePublicParamsFile points PUBLIC_PARAMS_FILE_PATH at a file holding base64-encoded public
// parameters, so that TokenChaincode.Params succeeds and initialization reaches the factory.
func writePublicParamsFile(t *testing.T) {
	t.Helper()
	t.Setenv(tcc.PublicParamsPathVarEnv, publicParamsFile(t))
}

// TestGetValidatorRetriesAfterFailedInitialization checks that a failed initialization attempt is
// reported to the caller and does not consume the only chance to initialize: the next call retries
// and, if the underlying cause is gone, succeeds.
func TestGetValidatorRetriesAfterFailedInitialization(t *testing.T) {
	writePublicParamsFile(t)

	validator := &mock.Validator{}
	calls := 0
	cc := &tcc.TokenChaincode{
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			calls++
			if calls == 1 {
				return nil, nil, errors.New("transient failure")
			}

			return &mock.PublicParametersManager{}, validator, nil
		},
	}

	v, err := cc.GetValidator(tcc.Params)
	require.Error(t, err)
	require.ErrorContains(t, err, "transient failure")
	assert.Nil(t, v)

	v, err = cc.GetValidator(tcc.Params)
	require.NoError(t, err)
	assert.Equal(t, validator, v)
	assert.Equal(t, 2, calls)
}

// TestGetValidatorKeepsReportingPersistentFailure checks that every call fails while
// initialization keeps failing, instead of reporting success with a nil validator.
func TestGetValidatorKeepsReportingPersistentFailure(t *testing.T) {
	writePublicParamsFile(t)

	calls := 0
	cc := &tcc.TokenChaincode{
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			calls++

			return nil, nil, errors.New("broken public parameters")
		},
	}

	for i := range 3 {
		v, err := cc.GetValidator(tcc.Params)
		require.Errorf(t, err, "call %d must report the initialization failure", i+1)
		require.ErrorContains(t, err, "broken public parameters")
		assert.Nil(t, v)
	}
	assert.Equal(t, 3, calls)
	assert.Nil(t, cc.Validator)
	assert.Nil(t, cc.PublicParameters)
}

// TestGetValidatorInitializesOnlyOnceOnSuccess checks that a successful initialization is cached.
func TestGetValidatorInitializesOnlyOnceOnSuccess(t *testing.T) {
	writePublicParamsFile(t)

	validator := &mock.Validator{}
	ppm := &mock.PublicParametersManager{}
	calls := 0
	cc := &tcc.TokenChaincode{
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			calls++

			return ppm, validator, nil
		},
	}

	for range 3 {
		v, err := cc.GetValidator(tcc.Params)
		require.NoError(t, err)
		assert.Equal(t, validator, v)
	}
	assert.Equal(t, 1, calls)
	assert.Equal(t, ppm, cc.PublicParameters)
}

// TestInvokeReportsInitializationErrorOnEveryCall checks the end-to-end symptom: a chaincode whose
// initialization fails answers every invocation with that error, rather than panicking on a nil
// validator or nil public parameters from the second invocation onwards.
func TestInvokeReportsInitializationErrorOnEveryCall(t *testing.T) {
	writePublicParamsFile(t)

	cc := &tcc.TokenChaincode{
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			return nil, nil, errors.New("broken public parameters")
		},
	}

	// areTokensSpent takes the token ids as second argument, invoke takes the token request from
	// the transient field and therefore expects a single argument.
	for function, args := range map[string][][]byte{
		tcc.AreTokensSpent: {[]byte(tcc.AreTokensSpent), []byte("[]")},
		tcc.InvokeFunction: {[]byte(tcc.InvokeFunction)},
	} {
		for i := range 2 {
			stub := &mock.ChaincodeStubInterface{}
			stub.GetTxIDReturns("txid")
			stub.GetArgsReturns(args)
			stub.GetTransientReturns(map[string][]byte{"token_request": []byte("token request")}, nil)

			response := cc.Invoke(stub)
			require.NotNil(t, response)
			assert.Equal(t, int32(500), response.Status, "%s call %d", function, i+1)
			assert.Contains(t, response.Message, "broken public parameters", "%s call %d", function, i+1)
		}
	}
}

// TestAreTokensSpentFailsGracefullyOnNilPublicParameters checks that AreTokensSpent refuses to
// dereference a nil PublicParameters: a TokenServicesFactory may report a successful initialization
// (non-nil validator, nil error) while handing back a nil PublicParameters, and the GraphHiding()
// call must not dereference it. Instead of panicking, it answers with an error response.
func TestAreTokensSpentFailsGracefullyOnNilPublicParameters(t *testing.T) {
	writePublicParamsFile(t)

	cc := &tcc.TokenChaincode{
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			return nil, &mock.Validator{}, nil // "successful" init, nil PublicParameters
		},
	}

	stub := &mock.ChaincodeStubInterface{}
	stub.GetTxIDReturns("txid")

	require.NotPanics(t, func() {
		resp := cc.AreTokensSpent([]byte(`["id1"]`), stub)
		require.Equal(t, int32(500), resp.Status)
		require.Contains(t, resp.Message, "public parameters not initialized")
	})
}

// TestGetValidatorConcurrentInitialization checks that concurrent callers initialize once and all
// observe the same validator.
func TestGetValidatorConcurrentInitialization(t *testing.T) {
	writePublicParamsFile(t)

	validator := &mock.Validator{}
	var calls atomic.Int32
	cc := &tcc.TokenChaincode{
		TokenServicesFactory: func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
			calls.Add(1)

			return &mock.PublicParametersManager{}, validator, nil
		},
	}

	const goroutines = 20
	var wg sync.WaitGroup
	results := make([]tcc.Validator, goroutines)
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			results[i], errs[i] = cc.GetValidator(tcc.Params)
		}()
	}
	wg.Wait()

	for i := range goroutines {
		require.NoError(t, errs[i])
		assert.Equal(t, validator, results[i])
	}
	assert.Equal(t, int32(1), calls.Load())
}
