/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core"
	fabtoken "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	dlog "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	"github.com/hyperledger/fabric-chaincode-go/v2/shim"
)

type serverConfig struct {
	CCID               string
	CCaddress          string
	TLS                string
	LogLevel           string
	LogFormat          string
	TLSKey             string
	TLSCert            string
	TLSCACertsFilePath string
}

func main() {
	config := serverConfig{
		CCID:               os.Getenv("CHAINCODE_ID"),
		CCaddress:          os.Getenv("CHAINCODE_SERVER_ADDRESS"),
		LogLevel:           os.Getenv("CHAINCODE_LOG_LEVEL"),
		LogFormat:          os.Getenv("CHAINCODE_LOG_FORMAT"),
		TLS:                os.Getenv("CHAINCODE_TLS"),
		TLSKey:             os.Getenv("CHAINCODE_TLS_KEY"),
		TLSCert:            os.Getenv("CHAINCODE_TLS_CERT"),
		TLSCACertsFilePath: os.Getenv("CHAINCODE_TLS_CA_CERTS"),
	}

	if len(config.LogLevel) == 0 {
		config.LogLevel = "info"
	}
	if len(config.TLS) == 0 && len(config.TLSKey) > 0 {
		config.TLS = "true"
	}
	if len(config.LogFormat) == 0 {
		config.LogFormat = "%{color}%{time:2006-01-02 15:04:05.000 MST} [%{module}] %{shortfunc} -> %{level:.4s} %{id:03x}%{color:reset} %{message}"
	}

	logging.Init(logging.Config{
		Format:  config.LogFormat,
		LogSpec: config.LogLevel,
		Writer:  os.Stderr,
	})

	limits, err := tcc.NewEnvResourceLimitsProvider().ResourceLimits()
	assertNoError(err, "cannot resolve validation resource limits")

	is := core.NewValidatorDriverService(
		limits,
		fabtoken.NewValidatorDriver(),
		dlog.NewValidatorDriver(),
	)
	if config.CCID == "" || config.CCaddress == "" {
		fmt.Println("CC ID or CC address is empty... Running as usual...")
		if os.Getenv("DEVMODE_ENABLED") != "" {
			fmt.Println("starting up in devmode...")
		}
		err := shim.Start(
			&tcc.TokenChaincode{
				TokenServicesFactory: func(bytes []byte) (tcc.PublicParameters, tcc.Validator, error) {
					ppm, err := is.PublicParametersFromBytes(bytes)
					if err != nil {
						return nil, nil, err
					}
					v, err := is.NewValidator(ppm)
					if err != nil {
						return nil, nil, err
					}

					return ppm, token.NewValidator(v), nil
				},
			},
		)
		assertNoError(err, "cannot start chaincode")
	} else {
		fmt.Println("Token Chaincode CCID : " + config.CCID)
		fmt.Println("Token Chaincode address : " + config.CCaddress)
		fmt.Println("Running Token Chaincode as service ...")

		// prepare TLS properties
		tlsProps := shim.TLSProperties{
			Disabled: false,
		}
		enabled, err := strconv.ParseBool(config.TLS)
		assertNoError(err, "cannot parse [%s]", config.TLS)
		if enabled {
			tlsKeyRaw, err := os.ReadFile(config.TLSKey) //nolint:gosec
			assertNoError(err, "cannot read tls key at [%s]", config.TLSKey)
			tlsCertRaw, err := os.ReadFile(config.TLSCert) //nolint:gosec
			assertNoError(err, "cannot read tls cert at [%s]", config.TLSKey)
			tlsCACertsRaw, err := os.ReadFile(config.TLSCACertsFilePath) //nolint:gosec
			assertNoError(err, "cannot read tls ca certs at [%s]", config.TLSCACertsFilePath)

			tlsProps.Key = tlsKeyRaw
			tlsProps.Cert = tlsCertRaw
			tlsProps.ClientCACerts = tlsCACertsRaw
		} else {
			tlsProps.Disabled = true
		}

		server := &shim.ChaincodeServer{
			CCID:    config.CCID,
			Address: config.CCaddress,
			CC: &tcc.TokenChaincode{
				TokenServicesFactory: func(bytes []byte) (tcc.PublicParameters, tcc.Validator, error) {
					ppm, err := is.PublicParametersFromBytes(bytes)
					if err != nil {
						return nil, nil, err
					}
					v, err := is.NewValidator(ppm)
					if err != nil {
						return nil, nil, err
					}

					return ppm, token.NewValidator(v), nil
				},
			},
			TLSProps: tlsProps,
		}
		err = server.Start()
		assertNoError(err, "Error starting Token Chaincode")
	}
}

func assertNoError(err error, s string, args ...string) {
	if err != nil {
		panic(fmt.Sprintf(s+": [%s]", append(args, err.Error())))
	}
}
