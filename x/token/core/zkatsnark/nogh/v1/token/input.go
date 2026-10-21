/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import "github.com/LFDT-Panurus/panurus/token/driver"

// Compile-time assertions: ensure our types satisfy the driver interfaces.
var (
	_ driver.TransferAction = (*TransferAction)(nil)
	_ driver.IssueAction    = (*IssueAction)(nil)
	_ driver.Output         = (*OutputDescription)(nil)
	_ driver.Input          = (*Input)(nil)
)

// Input is a minimal type satisfying driver.Input. the
// "owner" concept is represented by the recipient identity on outputs
// inputs are identified by their commitments, not by a ledger-stored
// owner field. This type exists solely to satisfy the common.Validator
// type parameter constraint.
type Input struct {
	Owner []byte
}

// GetOwner returns the owner identity.
func (i *Input) GetOwner() []byte {
	return i.Owner
}
