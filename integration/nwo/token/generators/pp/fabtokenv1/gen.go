/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtokenv1

import (
	"context"
	"strconv"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

const DefaultDriverVersion = setup.ProtocolV1

type FabTokenPublicParamsGenerator struct {
	DriverVersion driver.TokenDriverVersion
}

func NewFabTokenPublicParamsGenerator(version driver.TokenDriverVersion) *FabTokenPublicParamsGenerator {
	return &FabTokenPublicParamsGenerator{
		DriverVersion: version,
	}
}

// parsePrecision extracts the optional `precision` argument (args[1], the first is always empty)
// passed to Generate, falling back to setup.DefaultPrecision when no argument is given.
func parsePrecision(args []any) (uint64, error) {
	if len(args) != 2 {
		return setup.DefaultPrecision, nil
	}
	precisionStr, ok := args[1].(string)
	if !ok {
		return 0, errors.Errorf("expected string as first argument")
	}
	precision, err := strconv.ParseUint(precisionStr, 10, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse max token value [%s] to uint64", precisionStr)
	}

	return precision, nil
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

func (f *FabTokenPublicParamsGenerator) Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error) {
	precision, err := parsePrecision(args)
	if err != nil {
		return nil, err
	}
	pp, err := setup.WithVersion(precision, f.DriverVersion)
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
		if err := addX509Identities(keyStore, wallets.Issuers, issuersSet.Contains, "auditor", pp.AddIssuer); err != nil {
			return nil, err
		}
	}

	ppRaw, err := pp.Serialize()
	if err != nil {
		return nil, err
	}

	tms.Wallets = wallets

	return ppRaw, nil
}
