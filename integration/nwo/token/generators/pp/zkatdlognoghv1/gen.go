/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatdlognoghv1

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/IBM/idemix/msp"
	math3 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatdlognoghv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

const DefaultDriverVersion = setup.ProtocolV1

type DLogPublicParamsGenerator struct {
	DefaultCurveID math3.CurveID
	DriverVersion  driver.TokenDriverVersion
}

// NewDLogPublicParamsGenerator creates a new generator. The version is optional and defaults to v1.
func NewDLogPublicParamsGenerator(defaultCurveID math3.CurveID, version driver.TokenDriverVersion) *DLogPublicParamsGenerator {
	return &DLogPublicParamsGenerator{
		DefaultCurveID: defaultCurveID,
		DriverVersion:  version,
	}
}

// parseGenerateArgs validates and extracts Generate's two required arguments: the idemix root
// path (args[0]) and the token-value bit width (args[1]).
func parseGenerateArgs(args []any) (idemixRootPath string, bits uint64, err error) {
	if len(args) != 2 {
		return "", 0, errors.Errorf("invalid number of arguments, expected 2, got %d", len(args))
	}
	idemixRootPath, ok := args[0].(string)
	if !ok {
		return "", 0, errors.Errorf("invalid argument type, expected string, got %T", args[0])
	}
	baseArg, ok := args[1].(string)
	if !ok {
		return "", 0, errors.Errorf("invalid argument type, expected string, got %T", args[1])
	}
	bits, err = strconv.ParseUint(baseArg, 10, 32)
	if err != nil {
		return "", 0, err
	}

	return idemixRootPath, bits, nil
}

// buildPublicParams reads the idemix issuer public key from idemixRootPath, selects the curve
// (an Aries TMS always uses BLS12_381_BBS_GURVY regardless of the generator's default) and range
// proof type (CSP or the standard range proof, depending on the TMS), and constructs the
// versioned public parameters.
func (d *DLogPublicParamsGenerator) buildPublicParams(tms *topology.TMS, idemixRootPath string, bits uint64) (*setup.PublicParams, error) {
	path := filepath.Join(idemixRootPath, msp.IdemixConfigDirMsp, msp.IdemixConfigFileIssuerPublicKey)
	ipkBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	curveID := d.DefaultCurveID
	if zkatdlognoghv1.IsAries(tms) {
		curveID = math3.BLS12_381_BBS_GURVY
	}

	proofType := rp.RangeProofType
	if zkatdlognoghv1.IsCSP(tms) {
		proofType = rp.CSPRangeProofType
	}

	return setup.WithVersionAndProofType(bits, ipkBytes, curveID, d.DriverVersion, proofType)
}

// addX509Identities builds an MSP x509 identity for each entry in wallet and, for any whose ID
// satisfies matches, wraps it and passes it to add. Shared by the auditor/issuer identity-building
// blocks in Generate, which otherwise repeat this build-and-conditionally-wrap loop identically.
func addX509Identities(keyStore crypto.KeyStore, wallet []topology.Identity, matches func(id string) bool, roleLabel string, add func(driver.Identity)) error {
	for _, w := range wallet {
		km, _, err := x509.NewKeyManager(w.Path, w.Opts, keyStore)
		if err != nil {
			return errors.WithMessagef(err, "failed to create x509 km")
		}
		identityDescriptor, err := km.Identity(context.Background(), nil)
		if err != nil {
			return errors.WithMessagef(err, "failed to get identity")
		}
		if matches(w.ID) {
			wrap, err := identity.WrapWithType(x509.IdentityType, identityDescriptor.Identity)
			if err != nil {
				return errors.WithMessagef(err, "failed to create x509 identity for %s [%v]", roleLabel, w)
			}
			add(wrap)
		}
	}

	return nil
}

func (d *DLogPublicParamsGenerator) Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error) {
	idemixRootPath, bits, err := parseGenerateArgs(args)
	if err != nil {
		return nil, err
	}
	pp, err := d.buildPublicParams(tms, idemixRootPath, bits)
	if err != nil {
		return nil, err
	}

	keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
	if len(tms.Auditors) != 0 {
		if len(wallets.Auditors) == 0 {
			return nil, errors.Errorf("no auditor wallets provided")
		}
		matchesFirstAuditor := func(id string) bool { return tms.Auditors[0] == id }
		if err := addX509Identities(keyStore, wallets.Auditors, matchesFirstAuditor, "auditor", pp.AddAuditor); err != nil {
			return nil, err
		}
	}

	if len(tms.Issuers) != 0 {
		if len(wallets.Issuers) == 0 {
			return nil, errors.Errorf("no issuer wallets provided")
		}
		issuersSet := collections.NewSet(tms.Issuers...)
		if err := addX509Identities(keyStore, wallets.Issuers, issuersSet.Contains, "issuer", pp.AddIssuer); err != nil {
			return nil, err
		}
	}

	// validate before serialization
	if err := pp.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed to validate public parameters")
	}

	// finalization
	ppRaw, err := pp.Serialize()
	if err != nil {
		return nil, err
	}
	tms.Wallets = wallets

	return ppRaw, nil
}
