/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"fmt"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type (
	Type     = driver.IdentityType
	Identity = driver.Identity
)

// TypedIdentity encodes an identity with a type.
type TypedIdentity struct {
	// Type encodes the type of the identity
	Type Type `json:"type,omitempty" protobuf:"bytes,1,opt,name=type,json=type,proto3"`
	// Identity encodes the identity itself
	Identity Identity `json:"identity,omitempty" protobuf:"bytes,2,opt,name=identity,proto3"`
}

func (i TypedIdentity) Bytes() ([]byte, error) {
	return marshal.EncodeIdentity(i.Type, i.Identity), nil
}

func UnmarshalTypedIdentity(id Identity) (*TypedIdentity, error) {
	res, err := marshal.DecodeIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}

	if res.IsInt {
		si := &TypedIdentity{
			Type:     res.Int32,
			Identity: res.Data,
		}

		return si, nil
	}

	return nil, errors.New("invalid identity, type not recognized")
}

func WrapWithType(idType Type, id Identity) (Identity, error) {
	raw, err := (&TypedIdentity{Type: idType, Identity: id}).Bytes()
	if err != nil {
		return nil, err
	}

	return raw, nil
}

// TypeToString return a string identifier for the given identity type.
// If the type is unknown, it returns the string "Type (<t>)".
func TypeToString(t driver.IdentityType) string {
	switch t {
	case tdriver.IdemixIdentityType:
		return tdriver.IdemixIdentityTypeString
	case tdriver.IdemixNymIdentityType:
		return tdriver.IdemixNymIdentityTypeString
	case tdriver.X509IdentityType:
		return tdriver.X509IdentityTypeString
	case tdriver.MultiSigIdentityType:
		return tdriver.MultiSigIdentityTypeString
	case tdriver.HTLCScriptIdentityType:
		return tdriver.HTLCScriptIdentityTypeString
	default:
		return fmt.Sprintf("Type (%d)", t)
	}
}
