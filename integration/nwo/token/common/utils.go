/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"os"
	"path/filepath"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

var logger = logging.MustGetLogger()

const (
	FabricBinsPathEnvKey = "FAB_BINS"
	fabricCaClientCMD    = "fabric-ca-client"
	fabricCaServerCMD    = "fabric-ca-server"
)

func pathExists(path string) bool {
	if _, err := os.Stat(filepath.Clean(path)); os.IsNotExist(err) {
		return false
	}

	return true
}

// findCmdAtEnv tries to find cmd at the path specified via FabricBinsPathEnvKey
// Returns the full path of cmd if exists; otherwise an empty string
// Example:
//
//	export FAB_BINS=/tmp/fabric/bin/
//	findCmdAtEnv("peer") will return "/tmp/fabric/bin/peer" if exists
func findCmdAtEnv(cmd string) string {
	cmdPath := filepath.Join(os.Getenv(FabricBinsPathEnvKey), cmd)
	if !pathExists(cmdPath) {
		// cmd does not exist in folder provided via FabricBinsPathEnvKey
		return ""
	}

	return cmdPath
}
