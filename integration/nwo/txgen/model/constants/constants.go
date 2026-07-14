/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package constants

import (
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
)

// intermediary specific configuration

const IntermediaryRequestTimeout = 10 * time.Second
const PayerAccessTokenExpMin = 60 * time.Minute
const ApplicationJson = "application/json"
const ApplicationUrlEncoded = "application/x-www-form-urlencoded"
const HeaderContentType = "Content-Type"
const HeaderAuthorization = "Authorization"

// Logging levels.
const (
	SILENT model.LogLevel = "SILENT"
	DEBUG  model.LogLevel = "DEBUG"
	INFO   model.LogLevel = "INFO"
	WARN   model.LogLevel = "WARN"
	ERROR  model.LogLevel = "ERROR"
	FATAL  model.LogLevel = "FATAL"
)

// DefaultLogLevel Used by the app in case no log level is specified.
const DefaultLogLevel = DEBUG

// Log formats.
const (
	LogFormatJson model.LogFormat = "json"
	LogFormatCons model.LogFormat = "%{color}%{time:2006-01-02 15:04:05 MST} [%{module}] %{shortfunc} -> %{level:.4s} %{color:reset} %{message}"
)

// DefaultLogFormat Used by the app in case no format is specified.
const DefaultLogFormat = LogFormatCons

// ENV variables
const (
	UseDefaultConfigEnv = "CBDC_E2E_TX_USE_DEFAULT_CONFIG"
	ConfigFileEnv       = "CBDC_E2E_TX_CONFIG_FILE"
)

type ApiRequestType int

const (
	BalanceRequest ApiRequestType = iota
	WithdrawRequest
	PaymentInitiationRequest
	PaymentTransferRequest
)
