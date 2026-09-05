/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// CheckPayload rejects a write whose serialised size exceeds max (when max > 0).
// op names the operation for the error message.
func CheckPayload(op string, size, max int) error {
	if max > 0 && size > max {
		return errors.Errorf("%s payload size %d bytes exceeds maximum %d bytes", op, size, max)
	}

	return nil
}

// BlobMapSize returns the total byte size of a key/blob map (keys included), the
// shape used by the token-request and validation metadata maps.
func BlobMapSize(m map[string][]byte) int {
	n := 0
	for k, v := range m {
		n += len(k) + len(v)
	}

	return n
}
