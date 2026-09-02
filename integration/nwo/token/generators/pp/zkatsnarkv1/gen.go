/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zkatsnarkv1

import (
	"context"
	"os"
	"path/filepath"

	"github.com/IBM/idemix/msp"
	math3 "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	zkatsnarkcrypto "github.com/LFDT-Panurus/panurus/integration/nwo/token/generators/crypto/zkatsnarkv1"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

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

	// For zkatsnark, we get the default public parameters and then add issuers and auditors.
	params := pp.DefaultPublicParams()
	params.SchemeVersion = d.DriverVersion
	params.IdemixIssuerPublicKeys = []*pp.IdemixIssuerPublicKey{
		{
			PublicKey: ipkBytes,
			Curve:     curveID,
		},
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
				params.AuditorIdentities = append(params.AuditorIdentities, wrap)
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
				params.IssuerIdentities = append(params.IssuerIdentities, wrap)
			}
		}
	}

	// validate before serialization
	if err := params.Validate(); err != nil {
		return nil, errors.Wrapf(err, "failed to validate public parameters")
	}

	// Setup groth16 circuits and generate proving and verifying keys
	if _, err := setup.SetupAll(params); err != nil {
		return nil, errors.Wrapf(err, "failed to setup groth16 circuits")
	}

	// finalization
	ppRaw, err := params.Serialize()
	if err != nil {
		return nil, err
	}
	tms.Wallets = wallets

	return ppRaw, nil
}
