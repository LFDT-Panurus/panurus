/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// UnmarshalAuditInfo runs decode, converting a panic raised while decoding into
// an ordinary error.
//
// Audit info holds mathlib curve elements, whose UnmarshalJSON indexes mathlib's
// internal curve table with the "curve" field taken straight from the payload,
// without bounds-checking it (see marshaler.go in github.com/IBM/mathlib) — so an
// out-of-range curve ID panics instead of being rejected. mathlib v0.3.0 is the
// newest published version, so this has to be contained here. The same defect is
// contained the same way in FromG1Proto, in
// token/core/zkatdlog/nogh/protos-go/utils.
//
// The recover deliberately wraps the real decode rather than pre-validating the
// payload: mathlib runs during encoding/json's traversal, and a separate
// validation pass would have to reproduce that traversal exactly to see every
// curve element it decodes — including ones a later duplicate key overwrites,
// which never appear in the decoded result at all.
func UnmarshalAuditInfo(decode func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Errorf("failed to unmarshal audit info: caught panic [%v]", r)
		}
	}()

	return decode()
}
