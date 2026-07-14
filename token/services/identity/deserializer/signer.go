/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deserializer

import (
	"context"
	errors2 "errors"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type TypedSignerDeserializer = idriver.TypedSignerDeserializer

type TypedSignerDeserializerMultiplex struct {
	deserializers map[idriver.IdentityType][]TypedSignerDeserializer
}

func NewTypedSignerDeserializerMultiplex() *TypedSignerDeserializerMultiplex {
	return &TypedSignerDeserializerMultiplex{deserializers: map[idriver.IdentityType][]TypedSignerDeserializer{}}
}

func (v *TypedSignerDeserializerMultiplex) AddTypedSignerDeserializer(typ idriver.IdentityType, d idriver.TypedSignerDeserializer) {
	_, ok := v.deserializers[typ]
	if !ok {
		v.deserializers[typ] = []TypedSignerDeserializer{d}

		return
	}
	v.deserializers[typ] = append(v.deserializers[typ], d)
}

func (v *TypedSignerDeserializerMultiplex) DeserializeSigner(ctx context.Context, id []byte) (driver.Signer, error) {
	si, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}
	dess, ok := v.deserializers[si.Type]
	if !ok {
		return nil, errors.Errorf("no deserializer found for [%v]", si.Type)
	}
	logger.DebugfContext(ctx, "deserializing [%s] with type [%v]", logging.Base64(id), si.Type)
	var errs []error
	for _, deserializer := range dess {
		signer, err := deserializer.DeserializeSigner(ctx, si.Type, si.Identity)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return signer, nil
	}

	return nil, errors.Wrapf(errors2.Join(errs...), "failed to deserialize verifier for [%v]", si.Type)
}
