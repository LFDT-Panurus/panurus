/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package tcc_test

import (
	"encoding/base64"
	"os"

	chaincode2 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ccvalidator", func() {
	var (
		fakestub      *mock.ChaincodeStubInterface
		chaincode     *chaincode2.TokenChaincode
		fakeValidator *mock.Validator
		fakePPM       *mock.PublicParametersManager
		ppFile        *os.File
	)
	BeforeEach(func() {
		fakeValidator = &mock.Validator{}
		fakePPM = &mock.PublicParametersManager{}
		chaincode = &chaincode2.TokenChaincode{
			TokenServicesFactory: func(i []byte) (chaincode2.PublicParameters, chaincode2.Validator, error) {
				return fakePPM, fakeValidator, nil
			},
		}

		// Recall that the token chaincode is either build with the public parameters burnt in, or
		// loaded from a file specified by the environment variable PUBLIC_PARAMS_FILE_PATH.
		// In this test, we are using a file, so we need to create a temporary file to hold the
		// public parameters.
		pp := base64.StdEncoding.EncodeToString([]byte("public parameters"))
		var err error
		ppFile, err = os.CreateTemp("", "pp")
		Expect(err).NotTo(HaveOccurred())
		_, err = ppFile.WriteString(pp)
		Expect(err).NotTo(HaveOccurred())
		fakestub = &mock.ChaincodeStubInterface{}
		fakestub.GetTxIDReturns("txid")
		err = os.Setenv(chaincode2.PublicParamsPathVarEnv, ppFile.Name())
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.Remove(ppFile.Name())
	})

	Describe("Init", func() {
		Context("when init is called correctly", func() {
			It("Succeeds", func() {
				response := chaincode.Init(fakestub)
				Expect(response).NotTo(BeNil())
				Expect(response.Status).To(Equal(int32(200)))
			})
		})
	})

	Describe("Invoke", func() {
		Context("Invoke is called correctly with a token request", func() {
			BeforeEach(func() {
				var err error
				args := make([][]byte, 1)
				args[0] = []byte("invoke")
				Expect(err).NotTo(HaveOccurred())
				fakestub.GetArgsReturns(args)
				fakestub.GetTransientReturns(map[string][]byte{"token_request": []byte("token request")}, nil)
				fakestub.GetStateReturnsOnCall(0, []byte("pp"), nil)
				fakestub.GetStateReturnsOnCall(1, nil, nil)
				fakeValidator.UnmarshallAndVerifyWithMetadataReturns([]any{}, nil, nil)
			})
			It("succeeds", func() {
				response := chaincode.Invoke(fakestub)
				Expect(response).NotTo(BeNil())
				Expect(response.Status).To(Equal(int32(200)))
			})
		})

		Context("When VerifyTokenRequest fails", func() {
			BeforeEach(func() {
				var err error
				args := make([][]byte, 1)
				args[0] = []byte("invoke")
				Expect(err).NotTo(HaveOccurred())
				fakestub.GetArgsReturns(args)
				fakestub.GetTransientReturns(map[string][]byte{"token_request": []byte("token request")}, nil)
				fakeValidator.UnmarshallAndVerifyWithMetadataReturns(nil, nil, errors.Errorf("flying monkeys"))
			})
			It("fails", func() {
				response := chaincode.Invoke(fakestub)
				Expect(response).NotTo(BeNil())
				Expect(response.Status).To(Equal(int32(500)))
				Expect(response.Message).To(ContainSubstring("flying monkeys"))
			})
		})

	})
})
