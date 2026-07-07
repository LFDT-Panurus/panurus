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
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
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
	if len(args) != 2 {
		return nil, errors.Errorf("invalid number of arguments, expected 2, got %d", len(args))
	}
	// first argument is the idemix root path
	idemixRootPath, ok := args[0].(string)
	if !ok {
		return nil, errors.Errorf("invalid argument type, expected string, got %T", args[0])
	}
	path := filepath.Join(idemixRootPath, msp.IdemixConfigDirMsp, msp.IdemixConfigFileIssuerPublicKey)
	ipkBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	curveID := d.DefaultCurveID
	if zkatdlognoghv1.IsAries(tms) {
		curveID = math3.BLS12_381_BBS_GURVY
	}

	bits := uint64(64)
	if len(args) > 1 {
		baseArg, ok := args[1].(string)
		if !ok {
			return nil, errors.Errorf("invalid argument type, expected string, got %T", args[1])
		}
		bits, err = strconv.ParseUint(baseArg, 10, 32)
		if err != nil {
			return nil, err
		}
	}
	pp, err := setup.WithVersion(bits, ipkBytes, curveID, d.DriverVersion)
	if err != nil {
		return nil, err
	}

	keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
	if len(tms.Auditors) != 0 {
		if len(wallets.Auditors) == 0 {
			return nil, errors.Errorf("no auditor wallets provided")
		}
		for _, auditor := range wallets.Auditors {
			// Build an MSP Identity
			km, _, err := x509.NewKeyManager(auditor.Path, auditor.Opts, keyStore)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to create x509 km")
			}
			identityDescriptor, err := km.Identity(context.Background(), nil)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get identity")
			}
			if tms.Auditors[0] == auditor.ID {
				wrap, err := identity.WrapWithType(x509.IdentityType, identityDescriptor.Identity)
				if err != nil {
					return nil, errors.WithMessagef(err, "failed to create x509 identity for auditor [%v]", auditor)
				}
				pp.AddAuditor(wrap)
			}
		}
	}

	if len(tms.Issuers) != 0 {
		if len(wallets.Issuers) == 0 {
			return nil, errors.Errorf("no issuer wallets provided")
		}
		issuersSet := collections.NewSet(tms.Issuers...)
		for _, issuer := range wallets.Issuers {
			// Build an MSP Identity
			km, _, err := x509.NewKeyManager(issuer.Path, issuer.Opts, keyStore)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to create x509 km")
			}
			identityDescriptor, err := km.Identity(context.Background(), nil)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get identity")
			}
			if issuersSet.Contains(issuer.ID) {
				wrap, err := identity.WrapWithType(x509.IdentityType, identityDescriptor.Identity)
				if err != nil {
					return nil, errors.WithMessagef(err, "failed to create x509 identity for issuer [%v]", issuer)
				}
				pp.AddIssuer(wrap)
			}
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
