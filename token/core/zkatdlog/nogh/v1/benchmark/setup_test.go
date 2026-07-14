/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/stretchr/testify/require"
)

func TestSaveTo(t *testing.T) {
	require := require.New(t)
	dir := t.TempDir()
	bits := uint64(16)
	curve := math.BN254
	pp, err := setup.Setup(bits, []byte("dummy-issuer-pk"), curve)
	require.NoError(err, "failed to create public params")

	cfg := &SetupConfigurations{
		Configurations: map[string]*SetupConfiguration{
			key(bits, curve): {
				Bits:    bits,
				CurveID: curve,
				PP:      pp,
			},
		},
	}

	require.NoError(cfg.SaveTo(dir), "SaveTo failed")

	targetDir := filepath.Join(dir, key(bits, curve))
	st, err := os.Stat(targetDir)
	require.NoError(err, "expected target dir to exist")
	require.True(st.IsDir(), "expected target to be a directory")

	filePath := filepath.Join(targetDir, "pp.json")
	data, err := os.ReadFile(filePath)
	require.NoError(err, "failed reading pp.json")

	var payload SetupConfigurationSer
	require.NoError(json.Unmarshal(data, &payload), "failed to unmarshal pp.json")

	// bits and curve_id are numbers decoded as float64
	require.Equal(bits, payload.Bits, "bits mismatch")
	require.Equal(int(curve), payload.CurveID, "curve_id mismatch")

	ppB64 := payload.PP
	decoded, err := base64.StdEncoding.DecodeString(ppB64)
	require.NoError(err, "failed to base64 decode pp")
	require.NotEmpty(decoded, "decoded pp is empty")

	// check params.txt exists and contains the same base64 string
	paramsPath := filepath.Join(targetDir, "params.txt")
	paramsData, err := os.ReadFile(paramsPath)
	require.NoError(err, "failed reading params.txt")
	require.Equal(ppB64, string(paramsData), "params.txt content mismatch")

	// try deserializing the stored public params to ensure it's valid
	pp, err = setup.NewPublicParamsFromBytes(decoded, pp.DriverName, pp.DriverVersion)
	require.NoError(err, "failed to deserialize stored public params")
	require.NoError(pp.Validate(), "invalid public params")
}
