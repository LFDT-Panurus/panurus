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

func (d *DLogPublicParamsGenerator) Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error) {
	ipkBytes, bits, err := parseGenerateArgs(args)
	if err != nil {
		return nil, err
	}

	pp, err := d.buildPublicParams(tms, ipkBytes, bits)
	if err != nil {
		return nil, err
	}

	keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
	if len(tms.Auditors) != 0 {
		if len(wallets.Auditors) == 0 {
			return nil, errors.Errorf("no auditor wallets provided")
		}
		if err := addX509Identities(keyStore, wallets.Auditors, func(id string) bool { return tms.Auditors[0] == id }, pp.AddAuditor); err != nil {
			return nil, err
		}
	}

	if len(tms.Issuers) != 0 {
		if len(wallets.Issuers) == 0 {
			return nil, errors.Errorf("no issuer wallets provided")
		}
		issuersSet := collections.NewSet(tms.Issuers...)
		if err := addX509Identities(keyStore, wallets.Issuers, issuersSet.Contains, pp.AddIssuer); err != nil {
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

// buildPublicParams selects the curve (the Aries variant when tms is configured for
// it) and range-proof type (the CSP variant when tms is configured for it), and
// builds the public parameters for them.
func (d *DLogPublicParamsGenerator) buildPublicParams(tms *topology.TMS, ipkBytes []byte, bits uint64) (*setup.PublicParams, error) {
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

// parseGenerateArgs validates and extracts Generate's positional arguments: the
// idemix issuer public key bytes read from the root path in args[0], and the
// range-proof bit width in args[1].
func parseGenerateArgs(args []any) (ipkBytes []byte, bits uint64, err error) {
	if len(args) != 2 {
		return nil, 0, errors.Errorf("invalid number of arguments, expected 2, got %d", len(args))
	}
	// first argument is the idemix root path
	idemixRootPath, ok := args[0].(string)
	if !ok {
		return nil, 0, errors.Errorf("invalid argument type, expected string, got %T", args[0])
	}
	path := filepath.Join(idemixRootPath, msp.IdemixConfigDirMsp, msp.IdemixConfigFileIssuerPublicKey)
	ipkBytes, err = os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}

	bits = 64
	if len(args) > 1 {
		baseArg, ok := args[1].(string)
		if !ok {
			return nil, 0, errors.Errorf("invalid argument type, expected string, got %T", args[1])
		}
		bits, err = strconv.ParseUint(baseArg, 10, 32)
		if err != nil {
			return nil, 0, err
		}
	}

	return ipkBytes, bits, nil
}

// addX509Identities builds an x509 MSP identity for each wallet entry and, for
// entries selected by include, wraps it and passes it to add.
func addX509Identities(keyStore crypto.KeyStore, entries []topology.Identity, include func(id string) bool, add func(driver.Identity)) error {
	for _, entry := range entries {
		km, _, err := x509.NewKeyManager(entry.Path, entry.Opts, keyStore)
		if err != nil {
			return errors.WithMessagef(err, "failed to create x509 km")
		}
		identityDescriptor, err := km.Identity(context.Background(), nil)
		if err != nil {
			return errors.WithMessagef(err, "failed to get identity")
		}
		if !include(entry.ID) {
			continue
		}
		wrap, err := identity.WrapWithType(x509.IdentityType, identityDescriptor.Identity)
		if err != nil {
			return errors.WithMessagef(err, "failed to create x509 identity for [%v]", entry)
		}
		add(wrap)
	}

	return nil
}
