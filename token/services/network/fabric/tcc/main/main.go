/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime/coverage"
	"strconv"
	"syscall"
	"time"

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

	queryLimits, err := tcc.NewEnvQueryLimitsProvider().QueryLimits()
	assertNoError(err, "cannot resolve query limits")

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
				QueryLimits: queryLimits,
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
			tlsKeyRaw, err := os.ReadFile(config.TLSKey)
			assertNoError(err, "cannot read tls key at [%s]", config.TLSKey)
			tlsCertRaw, err := os.ReadFile(config.TLSCert)
			assertNoError(err, "cannot read tls cert at [%s]", config.TLSKey)
			tlsCACertsRaw, err := os.ReadFile(config.TLSCACertsFilePath)
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
				QueryLimits: queryLimits,
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

// init makes -cover instrumented builds of this binary keep flushing their
// coverage counters to GOCOVERDIR while running, instead of only on exit.
// shim.Start/server.Start never return during normal operation, and the peer
// tears this process down at the end of a test run by killing it outright
// (observed empirically: no SIGTERM/error return reaches this process, and
// Go's coverage runtime only persists counters on a normal return from main
// or an explicit write, never on an unhandled signal) -- so without periodic
// flushing here, the chaincode's own packages would never show up in
// integration test coverage.
func init() {
	dir := os.Getenv("GOCOVERDIR")
	if dir == "" {
		return
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		flushCoverage()
		// main never returns (it blocks in server.Start()), so this goroutine has
		// to terminate the process itself once the counters are safely written.
		// Exiting from init() rather than from a helper keeps revive's deep-exit
		// rule satisfied without a suppression directive.
		os.Exit(0)
	}()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			flushCoverage()
		}
	}()
}

// assertNoError panics on err, but first flushes any accumulated -cover
// counters: shim.Start/server.Start return with an error as soon as the
// peer closes its chaincode support connection during shutdown, which is
// the normal way this process ends -- if we panicked straight away the
// counters gathered during the test run would never reach GOCOVERDIR.
func assertNoError(err error, s string, args ...string) {
	if err != nil {
		flushCoverage()
		panic(fmt.Sprintf(s+": [%s]", append(args, err.Error())))
	}
}

func flushCoverage() {
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		_ = coverage.WriteCountersDir(dir)
	}
}
