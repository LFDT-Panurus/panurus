/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package dlogpp builds ZKAT-DLOG public parameters for zkatsnark tests of
// the ZKAT-DLOG upgrade path.
package dlogpp

import (
	mathlib "github.com/IBM/mathlib"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	dlogsetup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	dlogtoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// DefaultCurve is the ZKAT-DLOG curve the tests use: NWO runs ZKAT-DLOG on
// BLS12-381 with this curve, whose hash-to-curve, and therefore Pedersen
// generators, differ from the plain BLS12_381 ones.
const DefaultCurve = mathlib.BLS12_381_BBS_GURVY

// New returns ZKAT-DLOG public parameters with 64-bit precision on curve,
// and their serialization.
func New(curve mathlib.CurveID) (*dlogsetup.PublicParams, []byte, error) {
	legacy, err := dlogsetup.Setup(64, []byte("dummy-idemix-pk"), curve)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to set up ZKAT-DLOG public parameters")
	}
	raw, err := legacy.Serialize()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to serialize ZKAT-DLOG public parameters")
	}

	return legacy, raw, nil
}

// Embed sets up ZKAT-DLOG public parameters on curve and embeds them in p.
// It does not produce migration keys (see setup.SetupMigration).
func Embed(p *pp.PublicParams, curve mathlib.CurveID) (*dlogsetup.PublicParams, error) {
	legacy, raw, err := New(curve)
	if err != nil {
		return nil, err
	}
	p.LegacyDLogPublicParams = raw

	return legacy, nil
}

// Token is a ZKAT-DLOG output with its opening, as stored on the ledger and
// in the owner's vault.
type Token struct {
	// Raw is the serialized ZKAT-DLOG output (comm.Type typed token).
	Raw []byte
	// Metadata is the serialized opening (ZKAT-DLOG token metadata).
	Metadata []byte
}

// Mint creates a ZKAT-DLOG output of the passed type and value owned by owner
// under legacy, exactly as the ZKAT-DLOG driver does.
func Mint(legacy *dlogsetup.PublicParams, owner []byte, tokenType string, value uint64) (*Token, error) {
	c := mathlib.Curves[legacy.Curve]
	commitments, metadata, err := dlogtoken.GetTokensWithWitness([]uint64{value}, token2.Type(tokenType), legacy.PedersenGenerators, c)
	if err != nil {
		return nil, errors.Wrap(err, "failed to commit ZKAT-DLOG token")
	}
	raw, err := (&dlogtoken.Token{Owner: owner, Data: commitments[0]}).Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize ZKAT-DLOG token")
	}
	md, err := metadata[0].Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize ZKAT-DLOG token metadata")
	}

	return &Token{Raw: raw, Metadata: md}, nil
}
