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

// loadServerConfig reads the chaincode server configuration from the environment, applying
// defaults for LogLevel, TLS, and LogFormat when unset.
func loadServerConfig() serverConfig {
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

	return config
}

// tokenServicesFactory builds the tcc.TokenChaincode.TokenServicesFactory closure for the given
// validator driver service.
func tokenServicesFactory(is *core.ValidatorDriverService) func([]byte) (tcc.PublicParameters, tcc.Validator, error) {
	return func(bytes []byte) (tcc.PublicParameters, tcc.Validator, error) {
		ppm, err := is.PublicParametersFromBytes(bytes)
		if err != nil {
			return nil, nil, err
		}
		v, err := is.NewValidator(ppm)
		if err != nil {
			return nil, nil, err
		}

		return ppm, token.NewValidator(v), nil
	}
}

// runAsChaincode starts the token chaincode as a normal (non-service) shim chaincode.
func runAsChaincode(is *core.ValidatorDriverService, queryLimits tcc.QueryLimits) {
	fmt.Println("CC ID or CC address is empty... Running as usual...")
	if os.Getenv("DEVMODE_ENABLED") != "" {
		fmt.Println("starting up in devmode...")
	}
	err := shim.Start(
		&tcc.TokenChaincode{
			QueryLimits:          queryLimits,
			TokenServicesFactory: tokenServicesFactory(is),
		},
	)
	assertNoError(err, "cannot start chaincode")
}

// loadTLSProps builds the shim TLS properties for config, reading key/cert/CA files when TLS is
// enabled.
func loadTLSProps(config serverConfig) shim.TLSProperties {
	tlsProps := shim.TLSProperties{
		Disabled: false,
	}
	enabled, err := strconv.ParseBool(config.TLS)
	assertNoError(err, "cannot parse [%s]", config.TLS)
	if !enabled {
		tlsProps.Disabled = true

		return tlsProps
	}

	tlsKeyRaw, err := os.ReadFile(config.TLSKey)
	assertNoError(err, "cannot read tls key at [%s]", config.TLSKey)
	tlsCertRaw, err := os.ReadFile(config.TLSCert)
	assertNoError(err, "cannot read tls cert at [%s]", config.TLSKey)
	tlsCACertsRaw, err := os.ReadFile(config.TLSCACertsFilePath)
	assertNoError(err, "cannot read tls ca certs at [%s]", config.TLSCACertsFilePath)

	tlsProps.Key = tlsKeyRaw
	tlsProps.Cert = tlsCertRaw
	tlsProps.ClientCACerts = tlsCACertsRaw

	return tlsProps
}

// runAsService starts the token chaincode as a chaincode server.
func runAsService(is *core.ValidatorDriverService, config serverConfig, queryLimits tcc.QueryLimits) {
	fmt.Println("Token Chaincode CCID : " + config.CCID)
	fmt.Println("Token Chaincode address : " + config.CCaddress)
	fmt.Println("Running Token Chaincode as service ...")

	server := &shim.ChaincodeServer{
		CCID:    config.CCID,
		Address: config.CCaddress,
		CC: &tcc.TokenChaincode{
			QueryLimits:          queryLimits,
			TokenServicesFactory: tokenServicesFactory(is),
		},
		TLSProps: loadTLSProps(config),
	}
	assertNoError(server.Start(), "Error starting Token Chaincode")
}

func main() {
	config := loadServerConfig()

	logging.Init(logging.Config{
		Format:  config.LogFormat,
		LogSpec: config.LogLevel,
		Writer:  os.Stderr,
	})

	limits, err := tcc.NewEnvResourceLimitsProvider().ResourceLimits()
	assertNoError(err, "cannot resolve validation resource limits")

	queryLimits, err := tcc.NewEnvQueryLimitsProvider().QueryLimits()
	assertNoError(err, "cannot resolve query limits")

	is := core.NewValidatorDriverService(
		limits,
		fabtoken.NewValidatorDriver(),
		dlog.NewValidatorDriver(),
	)
	if config.CCID == "" || config.CCaddress == "" {
		runAsChaincode(is, queryLimits)
	} else {
		runAsService(is, config, queryLimits)
	}
}

func assertNoError(err error, s string, args ...string) {
	if err != nil {
		panic(fmt.Sprintf(s+": [%s]", append(args, err.Error())))
	}
}
