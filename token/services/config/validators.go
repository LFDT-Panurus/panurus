/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"github.com/LFDT-Panurus/panurus/token"
)

// ValidateConfiguration provides functions to access the configuration of a given TMS
type ValidateConfiguration interface {
	// ID identities the TMS this configuration refers to.
	ID() token.TMSID
	// IsSet checks to see if the key has been set in any of the data locations
	IsSet(key string) bool
	// UnmarshalKey takes a single key and unmarshals it into a struct.
	// The key must be relative to the TMS this configuration refers to.
	UnmarshalKey(key string, rawVal any) error
	// GetString returns the value associated with the key as a string
	GetString(key string) string
	// GetBool returns the value associated with the key as a bool
	GetBool(key string) bool
	// TranslatePath translates the passed path relative to the config path
	TranslatePath(path string) string
}

// Validator is used to validate a TMS configuration
type Validator interface {
	// Validate returns nil if the passed configuration is valid, an error otherwise.
	Validate(ValidateConfiguration) error
}
