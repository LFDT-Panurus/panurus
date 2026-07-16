/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

var (
	// ErrTMSNotFound signals that no TMS exists yet for the requested identifier
	// (e.g. no public parameters have been set up), as opposed to some other
	// failure while trying to retrieve/build it.
	ErrTMSNotFound = errors.New("tms not found")
)
