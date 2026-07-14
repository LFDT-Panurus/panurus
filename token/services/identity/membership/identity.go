/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership

import (
	"context"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
)

// GetIdentityFunc is a function that returns an Identity and its associated audit info for the given options
type GetIdentityFunc func(ctx context.Context, auditInfo []byte) (driver.Identity, []byte, error)

// LocalIdentity contains information about an identity
type LocalIdentity struct {
	Name         string
	EnrollmentID string
	Default      bool
	Anonymous    bool
	GetIdentity  GetIdentityFunc
	Remote       bool
}

func (i *LocalIdentity) String() string {
	if i.Anonymous {
		return fmt.Sprintf("{%s@%s-%v-%v-%v}", i.Name, i.EnrollmentID, i.Default, i.Anonymous, i.Remote)
	}
	id, _, err := i.GetIdentity(context.Background(), nil)
	if err != nil {
		return err.Error()
	}

	return fmt.Sprintf("{%s@%s-%v-%v-%v}[%s]", i.Name, i.EnrollmentID, i.Default, i.Anonymous, i.Remote, id)
}

// IdentityInfo implements the driver.IdentityInfo interface on top LocalIdentity
type IdentityInfo struct {
	localIdentity *LocalIdentity
	getIdentity   func(ctx context.Context) (driver.Identity, []byte, error)
}

func NewIdentityInfo(localIdentity *LocalIdentity, getIdentity func(ctx context.Context) (driver.Identity, []byte, error)) *IdentityInfo {
	return &IdentityInfo{localIdentity: localIdentity, getIdentity: getIdentity}
}

func (i *IdentityInfo) ID() string {
	return i.localIdentity.Name
}

func (i *IdentityInfo) EnrollmentID() string {
	return i.localIdentity.EnrollmentID
}

func (i *IdentityInfo) Get(ctx context.Context) (idriver.Identity, []byte, error) {
	return i.getIdentity(ctx)
}

func (i *IdentityInfo) Remote() bool {
	return i.localIdentity.Remote
}

func (i *IdentityInfo) Anonymous() bool {
	return i.localIdentity.Anonymous
}
