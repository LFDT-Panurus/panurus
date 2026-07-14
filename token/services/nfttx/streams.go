/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"encoding/base64"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/marshaller"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type OutputStream struct {
	*token.OutputStream
}

// Filter returns a stream of output filtered applying the passed filter
func (o *OutputStream) Filter(f func(t *token.Output) bool) *OutputStream {
	return &OutputStream{OutputStream: o.OutputStream.Filter(f)}
}

func (o *OutputStream) ByRecipient(id view.Identity) *OutputStream {
	return &OutputStream{OutputStream: o.OutputStream.ByRecipient(id)}
}

func (o *OutputStream) ByType(typ token2.Type) *OutputStream {
	return &OutputStream{OutputStream: o.OutputStream.ByType(typ)}
}

func (o *OutputStream) ByEnrollmentID(id string) *OutputStream {
	return &OutputStream{OutputStream: o.OutputStream.ByEnrollmentID(id)}
}

func (o *OutputStream) StateAt(index int, state any) error {
	output := o.At(index)
	decoded, err := base64.StdEncoding.DecodeString(string(output.Type))
	if err != nil {
		return errors.Wrap(err, "failed to decode type")
	}
	if err := marshaller.Unmarshal(decoded, state); err == nil {
		return errors.Wrap(err, "failed to unmarshal state")
	}

	return nil
}

func (o *OutputStream) Validate() error {
	// all outputs must have quantity set to 1
	one := token2.NewOneQuantity(o.Precision)
	for _, output := range o.Outputs() {
		if output.Quantity.Cmp(one) != 0 {
			return errors.New("all outputs must have quantity set to 1")
		}
	}

	return nil
}
