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

func (f *FabTokenPublicParamsGenerator) Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error) {
	precision, err := precisionFromArgs(args)
	if err != nil {
		return nil, err
	}
	pp, err := setup.WithVersion(precision, f.DriverVersion)
	if err != nil {
		return nil, err
	}

	keyStore := x509.NewKeyStore(kvs.NewTrackedMemory())
	if err := addAuditors(pp, tms, wallets, keyStore); err != nil {
		return nil, err
	}
	if err := addIssuers(pp, tms, wallets, keyStore); err != nil {
		return nil, err
	}

	ppRaw, err := pp.Serialize()
	if err != nil {
		return nil, err
	}

	tms.Wallets = wallets

	return ppRaw, nil
}

// precisionFromArgs returns the precision passed as the second generator argument (the first is
// empty), or setup.DefaultPrecision when the generator was not given exactly two arguments.
func precisionFromArgs(args []any) (uint64, error) {
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

// addAuditors adds to pp the identity of the TMS's first auditor, taken from the auditor wallets.
func addAuditors(pp *setup.PublicParams, tms *topology.TMS, wallets *topology.Wallets, keyStore crypto.KeyStore) error {
	if len(tms.Auditors) == 0 {
		return nil
	}
	if len(wallets.Auditors) == 0 {
		return errors.Errorf("no auditor wallets provided")
	}
	for _, auditor := range wallets.Auditors {
		id, err := walletIdentity(auditor, keyStore)
		if err != nil {
			return err
		}
		if tms.Auditors[0] == auditor.ID {
			wrap, err := identity.WrapWithType(x509.IdentityType, id)
			if err != nil {
				return errors.WithMessagef(err, "failed to create x509 identity for auditor [%v]", auditor)
			}
			pp.AddAuditor(wrap)
		}
	}

	return nil
}

// addIssuers adds to pp the identities of the TMS's issuers, taken from the issuer wallets.
func addIssuers(pp *setup.PublicParams, tms *topology.TMS, wallets *topology.Wallets, keyStore crypto.KeyStore) error {
	if len(tms.Issuers) == 0 {
		return nil
	}
	if len(wallets.Issuers) == 0 {
		return errors.Errorf("no issuer wallets provided")
	}
	issuersSet := collections.NewSet(tms.Issuers...)
	for _, issuer := range wallets.Issuers {
		id, err := walletIdentity(issuer, keyStore)
		if err != nil {
			return err
		}
		if issuersSet.Contains(issuer.ID) {
			wrap, err := identity.WrapWithType(x509.IdentityType, id)
			if err != nil {
				return errors.WithMessagef(err, "failed to create x509 identity for auditor [%v]", issuer)
			}
			pp.AddIssuer(wrap)
		}
	}

	return nil
}

// walletIdentity builds the x509 MSP identity of a wallet entry.
func walletIdentity(w topology.Identity, keyStore crypto.KeyStore) (identity.Identity, error) {
	km, _, err := x509.NewKeyManager(w.Path, w.Opts, keyStore)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create x509 km")
	}
	identityDescriptor, err := km.Identity(context.Background(), nil)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get identity")
	}

	return identityDescriptor.Identity, nil
}
