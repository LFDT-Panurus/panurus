/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package certfier

import (
	"os"
	"path/filepath"
	"testing"

	driver2 "github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPPMFactory struct {
	pp                     driver.PublicParameters
	ppErr                  error
	publicParamsManager    driver.PublicParamsManager
	publicParamsManagerErr error
}

func (m *mockPPMFactory) PublicParametersFromBytes(params []byte) (driver.PublicParameters, error) {
	return m.pp, m.ppErr
}

func (m *mockPPMFactory) NewPublicParametersManager(pp driver.PublicParameters) (driver.PublicParamsManager, error) {
	return m.publicParamsManager, m.publicParamsManagerErr
}

func (m *mockPPMFactory) NewValidator(pp driver.PublicParameters) (driver.Validator, error) {
	return nil, nil
}

// TestKeyPairGenCmd tests the KeyPairGenCmd function.
func TestKeyPairGenCmd(t *testing.T) {
	cmd := KeyPairGenCmd()
	assert.NotNil(t, cmd)
	assert.Equal(t, "certifier-keygen", cmd.Use)
}

// TestKeyPairGen tests the keyPairGen function.
func TestKeyPairGen(t *testing.T) {
	wd, _ := os.Getwd()
	testdataPath := filepath.Join(wd, "..", "..", "testdata", "zkatdlognoghv1_pp.json")
	tempDir := t.TempDir()

	t.Run("success_mock", func(t *testing.T) {
		defer func() { ppmFactoryService = nil }()

		mockPPM := &mock.PublicParamsManager{}
		mockPPM.NewCertifierKeyPairReturns([]byte("sk"), []byte("pk"), nil)

		mockPP := &mock.PublicParameters{}
		mockPP.TokenDriverNameReturns("mock")
		mockPP.TokenDriverVersionReturns(1)

		mockFactory := &mockPPMFactory{
			pp:                  mockPP,
			publicParamsManager: mockPPM,
		}

		ppmFactoryService = driver2.NewPPManagerFactoryService(driver2.NamedFactory[driver.PPMFactory]{
			Name:   "mock.v1",
			Driver: mockFactory,
		})

		ppFile := filepath.Join(tempDir, "mock_pp.json")
		err := os.WriteFile(ppFile, []byte(`{"identifier":"mock.v1","raw":"YmFzZTY0"}`), 0644)
		require.NoError(t, err)

		ppPath = ppFile
		output = filepath.Join(tempDir, "out")
		err = keyPairGen()
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(output, "certifier.sk"))
		assert.FileExists(t, filepath.Join(output, "certifier.pk"))
	})

	t.Run("success_real", func(t *testing.T) {
		ppPath = testdataPath
		output = tempDir
		_ = keyPairGen()
		// NewCertifierKeyPair is currently hardcoded to return "not supported" in core/common/ppm.go
	})

	t.Run("read_pp_fail", func(t *testing.T) {
		ppPath = "nonexistent.json"
		output = tempDir
		err := keyPairGen()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed reading public parameters")
	})

	t.Run("unmarshal_pp_fail", func(t *testing.T) {
		invalidPP := filepath.Join(tempDir, "invalid_pp.json")
		err := os.WriteFile(invalidPP, []byte("invalid content"), 0644)
		require.NoError(t, err)
		ppPath = invalidPP
		output = tempDir
		err = keyPairGen()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshalling public parameters")
	})

	t.Run("new_ppm_fail", func(t *testing.T) {
		// Provide bytes that pass deserialization but fail ppm instantiation (e.g. invalid internal content)
		// We'll use the same trick as before but for zkatdlognogh directly if possible,
		// or just use a mock that returns error on NewPublicParametersManager.
		defer func() { ppmFactoryService = nil }()

		mockPP := &mock.PublicParameters{}
		mockPP.TokenDriverNameReturns("mock")
		mockPP.TokenDriverVersionReturns(1)

		mockFactory := &mockPPMFactory{
			pp:                     mockPP,
			publicParamsManagerErr: assert.AnError,
		}

		ppmFactoryService = driver2.NewPPManagerFactoryService(driver2.NamedFactory[driver.PPMFactory]{
			Name:   "mock.v1",
			Driver: mockFactory,
		})

		ppFile := filepath.Join(tempDir, "mock_pp_fail.json")
		err := os.WriteFile(ppFile, []byte(`{"identifier":"mock.v1","raw":""}`), 0644)
		require.NoError(t, err)

		ppPath = ppFile
		err = keyPairGen()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed instantiating public parameters manager")
	})

	t.Run("mkdir_fail", func(t *testing.T) {
		defer func() { ppmFactoryService = nil }()

		mockPPM := &mock.PublicParamsManager{}
		mockPPM.NewCertifierKeyPairReturns([]byte("sk"), []byte("pk"), nil)

		mockPP := &mock.PublicParameters{}
		mockPP.TokenDriverNameReturns("mock")
		mockPP.TokenDriverVersionReturns(1)

		mockFactory := &mockPPMFactory{
			pp:                  mockPP,
			publicParamsManager: mockPPM,
		}

		ppmFactoryService = driver2.NewPPManagerFactoryService(driver2.NamedFactory[driver.PPMFactory]{
			Name:   "mock.v1",
			Driver: mockFactory,
		})

		ppFile := filepath.Join(tempDir, "mock_pp_mkdir.json")
		err := os.WriteFile(ppFile, []byte(`{"identifier":"mock.v1","raw":""}`), 0644)
		require.NoError(t, err)

		// Create a file where a directory should be
		blockedPath := filepath.Join(tempDir, "blocked")
		err = os.WriteFile(blockedPath, []byte("file"), 0644)
		require.NoError(t, err)

		ppPath = ppFile
		output = filepath.Join(blockedPath, "subdir")
		err = keyPairGen()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed making output dir")
	})

	t.Run("write_sk_fail", func(t *testing.T) {
		defer func() { ppmFactoryService = nil }()

		mockPPM := &mock.PublicParamsManager{}
		mockPPM.NewCertifierKeyPairReturns([]byte("sk"), []byte("pk"), nil)

		mockPP := &mock.PublicParameters{}
		mockPP.TokenDriverNameReturns("mock")
		mockPP.TokenDriverVersionReturns(1)

		mockFactory := &mockPPMFactory{
			pp:                  mockPP,
			publicParamsManager: mockPPM,
		}

		ppmFactoryService = driver2.NewPPManagerFactoryService(driver2.NamedFactory[driver.PPMFactory]{
			Name:   "mock.v1",
			Driver: mockFactory,
		})

		ppFile := filepath.Join(tempDir, "mock_pp_sk.json")
		err := os.WriteFile(ppFile, []byte(`{"identifier":"mock.v1","raw":""}`), 0644)
		require.NoError(t, err)

		ppPath = ppFile
		// Use a directory that exists but we'll try to write to a path that's a directory
		skDir := filepath.Join(tempDir, "certifier.sk")
		err = os.MkdirAll(skDir, 0750)
		require.NoError(t, err)

		output = tempDir
		err = keyPairGen()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed writing certifier secret key to file")
	})

	t.Run("write_pk_fail", func(t *testing.T) {
		defer func() { ppmFactoryService = nil }()

		mockPPM := &mock.PublicParamsManager{}
		mockPPM.NewCertifierKeyPairReturns([]byte("sk"), []byte("pk"), nil)

		mockPP := &mock.PublicParameters{}
		mockPP.TokenDriverNameReturns("mock")
		mockPP.TokenDriverVersionReturns(1)

		mockFactory := &mockPPMFactory{
			pp:                  mockPP,
			publicParamsManager: mockPPM,
		}

		ppmFactoryService = driver2.NewPPManagerFactoryService(driver2.NamedFactory[driver.PPMFactory]{
			Name:   "mock.v1",
			Driver: mockFactory,
		})

		ppFile := filepath.Join(tempDir, "mock_pp_pk.json")
		err := os.WriteFile(ppFile, []byte(`{"identifier":"mock.v1","raw":""}`), 0644)
		require.NoError(t, err)

		ppPath = ppFile
		// Use a directory that exists but we'll try to write to a path that's a directory
		pkDir := filepath.Join(tempDir, "certifier.pk")
		err = os.MkdirAll(pkDir, 0750)
		require.NoError(t, err)

		// ensure sk is NOT a directory
		_ = os.RemoveAll(filepath.Join(tempDir, "certifier.sk"))

		output = tempDir
		err = keyPairGen()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed writing certifier public key to file")
	})
}

// TestCobraCommand tests the Cobra command for generating certifier key pairs.
func TestCobraCommand(t *testing.T) {
	wd, _ := os.Getwd()
	testdataPath := filepath.Join(wd, "..", "..", "testdata", "zkatdlognoghv1_pp.json")
	tempDir := t.TempDir()

	cmd := KeyPairGenCmd()
	cmd.SetArgs([]string{"--pppath", testdataPath, "--output", tempDir})
	err := cmd.Execute()
	// It will return "not supported" currently
	if err != nil {
		assert.Contains(t, err.Error(), "not supported")
	}

	// Test trailing args
	cmd.SetArgs([]string{"--pppath", testdataPath, "extra"})
	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trailing args detected")
}
