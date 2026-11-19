/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import "github.com/LFDT-Panurus/panurus/token/services/logging"

// Logger returns the package logger. It is exported only to the external test package.
func Logger() logging.Logger {
	return logger
}
