/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Marshal marshals the passed public parameters
func Marshal(pp *pp.PublicParameters) ([]byte, error) {
	if pp == nil {
		return nil, errors.New("nil public parameters")
	}

	return json.Marshal(pp)
}

// Unmarshal unmarshals the passed slice into an instance of pp.PublicParameters
func Unmarshal(raw []byte) (*pp.PublicParameters, error) {
	pp := &pp.PublicParameters{}
	if err := json.Unmarshal(raw, pp); err != nil {
		return nil, err
	}

	return pp, nil
}
