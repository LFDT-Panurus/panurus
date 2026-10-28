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

// maxAttrSamples bounds the number of user secrets IssueSignerConfig samples.
// A random secret fails the norm bound with small probability, so reaching it
// means that the public attributes alone exceed the bound.
const maxAttrSamples = 1000

// sampleAttributes samples a user secret such that the attributes satisfy the
// norm bound of the issuance proof, trying at most maxSamples secrets.
func sampleAttributes(ou, role, eid, rh string, maxSamples int, ok func([][]byte) (bool, error)) ([][]byte, error) {
	for range maxSamples {
		usk := make([]byte, anoncreds.AttrLen)
		if _, err := rand.Read(usk); err != nil {
			return nil, errors.Wrap(err, "failed to sample the user secret")
		}
		attrs := attributes(usk, ou, eid, rh, role)
		// the issuance proof bounds the norm of the encoded attributes;
		// resample the secret if it is not satisfied
		good, err := ok(attrs)
		if err != nil {
			return nil, err
		}
		if good {
			return attrs, nil
		}
	}

	return nil, errors.Errorf("no user secret satisfies the norm bound after %d attempts: the encodings of the OU, role, enrollment id and revocation handle are too large, change one of them", maxSamples)
}

// exportUserState returns the masking randomness of user, the part of the
// wallet secret after usk.
func exportUserState(user *anoncreds.User) ([]byte, error) {
	return user.Export()
}

// IssueSignerConfig plays both the user and the issuer to produce the signer
// configuration of a wallet. It is meant for tooling and tests; in a
// deployment masking and issuing happen on different machines.
func IssueSignerConfig(isk, ipk []byte, ou, role, eid, rh string) (*SignerConfig, error) {
	attrs, err := sampleAttributes(ou, role, eid, rh, maxAttrSamples, anoncreds.AttrsOK)
	if err != nil {
		return nil, err
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

	state, err := exportUserState(user)
	if err != nil {
		return nil, errors.Wrap(err, "failed to export the user state")
	}
	secret := append(append([]byte(nil), attrs[IdxUSK]...), state...)

	return &SignerConfig{
		Cred:             cred,
		Sk:               secret,
		OU:               ou,
		Role:             role,
		EnrollmentID:     eid,
		RevocationHandle: rh,
	}, nil
}
