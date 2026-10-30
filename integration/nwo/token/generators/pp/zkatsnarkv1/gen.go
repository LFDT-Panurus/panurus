/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnarkv1

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/IBM/idemix/msp"
	math3 "github.com/IBM/mathlib"
	zkatsnarkcrypto "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatsnarkv1"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

var logger = logging.MustGetLogger()

type ZkatsnarkPublicParamsGenerator struct {
	DriverVersion driver.TokenDriverVersion
}

// NewZkatsnarkPublicParamsGenerator creates a new generator.
func NewZkatsnarkPublicParamsGenerator(version driver.TokenDriverVersion) *ZkatsnarkPublicParamsGenerator {
	return &ZkatsnarkPublicParamsGenerator{
		DriverVersion: version,
	}
}

func (d *ZkatsnarkPublicParamsGenerator) Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error) {
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

	curveID := math3.BN254
	if zkatsnarkcrypto.IsAries(tms) {
		curveID = math3.BLS12_381_BBS_GURVY
	}

	// second argument is the max bit length of token values
	baseArg, ok := args[1].(string)
	if !ok {
		return nil, errors.Errorf("invalid argument type, expected string, got %T", args[1])
	}
	bits, err := strconv.ParseUint(baseArg, 10, 32)
	if err != nil {
		return nil, err
	}

	var auditors, issuers []driver.Identity
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
				auditors = append(auditors, wrap)
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
				issuers = append(issuers, wrap)
			}
		}
	}

	// Build, validate and set up the public parameters (circuit keys included).
	params, err := setup.GeneratePublicParams(setup.GenerateOptions{
		Version: d.DriverVersion,
		MaxBits: int(bits),
		IdemixIssuerPublicKeys: []*pp.IdemixIssuerPublicKey{
			{
				PublicKey: ipkBytes,
				Curve:     curveID,
			},
		},
		Issuers:  issuers,
		Auditors: auditors,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate public parameters")
	}
	if err := params.ValidateForDeployment(); err != nil {
		logger.Warnf("zkatsnark public parameters: %s", err)
	}

	// finalization
	ppRaw, err := params.Serialize()
	if err != nil {
		return nil, err
	}
	tms.Wallets = wallets

	return ppRaw, nil
}
