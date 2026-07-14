/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dummy

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

type CertificationClient struct{}

func (c *CertificationClient) IsCertified(ctx context.Context, id *token2.ID) bool {
	return true
}

func (c *CertificationClient) RequestCertification(ctx context.Context, ids ...*token2.ID) error {
	return nil
}

func (c *CertificationClient) Start() error {
	return nil
}

type CertificationService struct{}

func (c *CertificationService) Start() error {
	return nil
}

type Driver struct{}

func NewDriver() *Driver {
	return &Driver{}
}

func (d *Driver) NewCertificationClient(ctx context.Context, tms *token.ManagementService) (driver.CertificationClient, error) {
	return &CertificationClient{}, nil
}

func (d *Driver) NewCertificationService(tms *token.ManagementService, wallet string) (driver.CertificationService, error) {
	return &CertificationService{}, nil
}
