/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"bytes"
	"embed"
	"fmt"
	"os"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
	c "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/constants"
	"github.com/spf13/viper"
)

//go:embed resources/config.yaml
var embeddedFiles embed.FS

// Load Read the configuration file from the file system and return it.
//
// returns:
//
//	config model.Configuration The application config.
func Load() (*model.Configuration, api.Error) {
	useDefaultConfig := os.Getenv(c.UseDefaultConfigEnv)
	if useDefaultConfig != "false" {
		// load the default config file
		appErr := loadDefaultConfig()
		if appErr != nil {
			return nil, appErr
		}
	}

	// load extra config file
	configFile := os.Getenv(c.ConfigFileEnv)
	if configFile != "" {
		appErr := loadConfig(configFile)
		if appErr != nil {
			return nil, appErr
		}
	}

	// marshal config
	var config model.Configuration
	err := viper.Unmarshal(&config)
	if err != nil {
		appErr := api.NewInternalServerError(
			fmt.Errorf("cannot unmarshal the configuration file: %w", err),
			"Internal Server Error",
		)

		return nil, appErr
	}

	return &config, nil
}

// loadConfig Read the configuration file passed by environment variable.
//
//	configFile string The configuration file path.
func loadConfig(configFile string) api.Error {
	viper.SetConfigFile(configFile)

	// Read the config file
	err := viper.MergeInConfig()
	if err != nil {
		return api.NewInternalServerError(
			fmt.Errorf("couldn't read the config file '%s': %w", configFile, err),
			"Internal Server Error",
		)
	}

	return nil
}

// loadDefaultConfig Read the default configuration file.
func loadDefaultConfig() api.Error {
	configuration, err := embeddedFiles.ReadFile("config.yaml")
	if err != nil {
		return api.NewInternalServerError(
			fmt.Errorf("couldn't find the default config file 'config.yaml': %w", err),
			"Internal Server Error",
		)
	}

	// Read the config file
	viper.SetConfigType("yaml")
	err = viper.ReadConfig(bytes.NewReader(configuration))
	if err != nil {
		return api.NewInternalServerError(
			fmt.Errorf("couldn't read the default config file 'config.yaml': %w", err),
			"Internal Server Error",
		)
	}

	return nil
}
