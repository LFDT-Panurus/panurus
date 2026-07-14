/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token provides token certification functionality for creating, verifying, and managing token certifications.
package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// CertificationManager manages token certifications as described by the paper
// [`Privacy-preserving auditable token payments in a permissioned blockchain system`]('https://eprint.iacr.org/2019')
type CertificationManager struct {
	c driver.CertificationService
}

// NewCertificationRequest creates a new certification request, in a serialized form, for the passed token ids.
func (c *CertificationManager) NewCertificationRequest(ids []*token2.ID) ([]byte, error) {
	return c.c.NewCertificationRequest(ids)
}

// Certify uses the passed wallet to certify the passed token ids.
// Certify takes in input the certification request and the token representations as available on the ledger.
func (c *CertificationManager) Certify(wallet *CertifierWallet, ids []*token2.ID, tokens [][]byte, request []byte) ([][]byte, error) {
	return c.c.Certify(wallet.w, ids, tokens, request)
}

// VerifyCertifications verifies the validity of the certifications of each token indexed by its token-id.
// The function returns the result of any processing of these certifications.
// In the simplest case, VerifyCertifications returns the certifications got in input
func (c *CertificationManager) VerifyCertifications(ids []*token2.ID, certifications [][]byte) ([][]byte, error) {
	return c.c.VerifyCertifications(ids, certifications)
}

// CertificationClient is the client side of the certification process
type CertificationClient struct {
	cc driver.CertificationClient
}

// IsCertified returns true if the passed token id has been already certified, otherwise false
func (c *CertificationClient) IsCertified(ctx context.Context, id *token2.ID) bool {
	return c.cc.IsCertified(ctx, id)
}

// RequestCertification requests the certification of the passed token ids
func (c *CertificationClient) RequestCertification(ctx context.Context, ids ...*token2.ID) error {
	return c.cc.RequestCertification(ctx, ids...)
}
