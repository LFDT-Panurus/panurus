/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
)

type Configuration struct {
	App          AppConfig          `yaml:"app"`
	Suites       []SuiteConfig      `yaml:"suites"`
	UserProvider UserProviderConfig `yaml:"userProvider"`
	Intermediary IntermediaryConfig `yaml:"intermediary"`
	Server       ServerConfig       `yaml:"server"`
}

type AppConfig struct {
	Logging   LogLevel  `yaml:"logging"`
	LogFormat LogFormat `yaml:"logFormat"`
}

type UserProviderConfig struct {
	Users      []UserConfig     `yaml:"users"`
	HttpClient HttpClientConfig `yaml:"httpClient"`
}

type HttpClientConfig struct {
	Timeout             time.Duration `yaml:"timeout"`
	MaxConnsPerHost     int           `yaml:"maxConnsPerHost"`
	MaxIdleConnsPerHost int           `yaml:"maxIdleConnsPerHost"`
}

type Username = string
type UserAlias = string

type UserConfig struct {
	Name     UserAlias `json:"name"     yaml:"name"`
	Username Username  `json:"username" yaml:"username"`
	Password string    `json:"password" yaml:"password"`
	Endpoint string    `json:"endpoint" yaml:"endpoint"`
}

type SuiteConfig struct {
	Name             string        `json:"name"             yaml:"name"`
	PoolSize         int           `json:"poolSize"         yaml:"poolSize"`
	Iterations       int           `json:"iterations"       yaml:"iterations"`
	Delay            time.Duration `json:"delay"            yaml:"delay"`
	Cases            []TestCase    `json:"cases"            yaml:"cases"`
	UseExistingFunds bool          `yaml:"useExistingFunds"`
}

type IntermediaryConfig struct {
	DelayAfterInitiation time.Duration `yaml:"delayAfterInitiation"`
}

type ServerConfig struct {
	Endpoint string
}

type IssueConfig struct {
	Total        api.Amount   `yaml:"total"`
	Distribution Distribution `yaml:"distribution"`
	Execute      bool         `yaml:"execute"`
}

type TransferConfig struct {
	Distribution Distribution `yaml:"distribution"`
	Execute      bool         `yaml:"execute"`
}

type TestCase struct {
	Name     string         `json:"name"     yaml:"name"`
	Payer    UserAlias      `json:"payer"    yaml:"payer"`
	Payees   []UserAlias    `json:"payees"   yaml:"payees"`
	Issue    IssueConfig    `json:"issue"    yaml:"issue"`
	Transfer TransferConfig `json:"transfer" yaml:"transfer"`
}

// LogLevel String defining a log level.
type LogLevel string

// LogFormat String defining a log format.
type LogFormat string
