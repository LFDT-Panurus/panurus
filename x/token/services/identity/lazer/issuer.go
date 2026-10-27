/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"crypto/rand"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// NewIssuerKey generates a new issuer key pair.
func NewIssuerKey() (sk, pk []byte) {
	return anoncreds.IssuerKeygen()
}

// IssueSignerConfig plays both the user and the issuer to produce the signer
// configuration of a wallet. It is meant for tooling and tests; in a
// deployment masking and issuing happen on different machines.
func IssueSignerConfig(isk, ipk []byte, ou, role, eid, rh string) (*SignerConfig, error) {
	var attrs [][]byte
	for {
		usk := make([]byte, anoncreds.AttrLen)
		if _, err := rand.Read(usk); err != nil {
			return nil, errors.Wrap(err, "failed to sample the user secret")
		}
		attrs = attributes(usk, ou, eid, rh, role)
		// the issuance proof bounds the norm of the encoded attributes;
		// resample the secret if it is not satisfied
		ok, err := anoncreds.AttrsOK(attrs)
		if err != nil {
			return nil, err
		}
		if ok {
			break
		}
	}

	user, err := anoncreds.NewUser(ipk)
	if err != nil {
		return nil, err
	}
	defer user.Clear()
	issuer, err := anoncreds.NewIssuer(ipk, isk)
	if err != nil {
		return nil, err
	}
	defer issuer.Clear()

	masked, err := user.MaskAttrs(attrs, idxIssuance)
	if err != nil {
		return nil, err
	}
	cred, err := issuer.Issue(attributesRevealedToIssuer(attrs), idxIssuance, masked)
	if err != nil {
		return nil, errors.Wrap(err, "issuer rejected the masked attributes")
	}
	if err := user.CredVerify(cred); err != nil {
		return nil, errors.Wrap(err, "issued credential is invalid")
	}

	secret := append(append([]byte(nil), attrs[IdxUSK]...), user.Export()...)

	return &SignerConfig{
		Cred:             cred,
		Sk:               secret,
		OU:               ou,
		Role:             role,
		EnrollmentID:     eid,
		RevocationHandle: rh,
	}, nil
}
