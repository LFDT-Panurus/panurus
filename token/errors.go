/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrFailedToGetTMS is when an error occurs when getting an instance of a given TMS
	ErrFailedToGetTMS = errors.New("failed to get token manager")
	// ErrTMSNotFound signals that no TMS exists yet for the requested identifier
	// (e.g. no public parameters have been set up), as opposed to some other
	// failure while trying to retrieve/build it.
	ErrTMSNotFound = core.ErrTMSNotFound
)
