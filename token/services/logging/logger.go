/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package logging

import (
	"slices"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	"go.uber.org/zap/zapcore"
)

const loggerNameSeparator = "."

// Logger provides logging API
type (
	Logger = logging.Logger
	Config = logging.Config
)

func Init(config logging.Config) {
	logging.Init(config)
}

func MustGetLogger(params ...string) Logger {
	return utils.MustGet(GetLogger(params...))
}

func GetLogger(params ...string) (Logger, error) {
	return logging.GetLoggerWithReplacements(map[string]string{"github.com.LFDT-Panurus.panurus.token": "panurus"}, params)
}

func DriverLogger(prefix string, networkID string, channel string, namespace string) Logger {
	return MustGetLogger(loggerName(prefix, networkID, channel, namespace))
}

func DeriveDriverLogger(logger Logger, prefix string, networkID string, channel string, namespace string) Logger {
	return logger.Named(loggerName(prefix, networkID, channel, namespace))
}

func DriverLoggerFromPP(prefix string, id string) Logger {
	return MustGetLogger(loggerName(prefix, id))
}

func isEmptyString(s string) bool { return len(s) == 0 }

func loggerName(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, isEmptyString), loggerNameSeparator)
}

// Debug is a workaround to reduce memory allocation when debug is disabled.
// Indeed, the fabric logger performs an operation that allocate memory before checking the log level.
func Debug(logger Logger, params ...any) {
	if logger.IsEnabledFor(zapcore.DebugLevel) {
		logger.Debug(params...)
	}
}
