/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package logging

import (
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
)

type Logger = logging.Logger

// MustGetLogger Get a logger.
func MustGetLogger(params ...string) Logger {
	return utils.MustGet(GetLogger(params...))
}

func GetLogger(params ...string) (Logger, error) {
	return logging.GetLogger(params...)
}
