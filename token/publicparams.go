/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
)

type PPHash = driver.PPHash

type PublicParameters struct {
	driver.PublicParameters
}

// PublicParamsFetcher models the public parameters fetcher
type PublicParamsFetcher interface {
	// Fetch fetches the public parameters from the backend
	Fetch() ([]byte, error)
}

// PublicParametersManager exposes methods to manage the public parameters
type PublicParametersManager struct {
	ppm driver.PublicParamsManager
	pp  *PublicParameters
}

// PublicParameters returns the public parameters, nil if not set yet.
func (c *PublicParametersManager) PublicParameters() *PublicParameters {
	return c.pp
}

func (c *PublicParametersManager) PublicParamsHash() PPHash {
	return c.ppm.PublicParamsHash()
}
