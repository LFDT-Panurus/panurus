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
	// GeneratorDomain optionally replaces the default domain from which all generators are derived
	GeneratorDomain string
}

// NewDLogPublicParamsGenerator creates a new generator. The version is optional and defaults to v1.
func NewDLogPublicParamsGenerator(defaultCurveID math3.CurveID, version driver.TokenDriverVersion) *DLogPublicParamsGenerator {
	return &DLogPublicParamsGenerator{
		DefaultCurveID: defaultCurveID,
		DriverVersion:  version,
	}
}

func (d *DLogPublicParamsGenerator) Generate(tms *topology.TMS, wallets *topology.Wallets, args ...any) ([]byte, error) {
	ipkBytes, bits, err := parseArgs(args)
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
	pp, err := setup.NewWith(setup.SetupParams{
		DriverName:      setup.DLogNoGHDriverName,
		DriverVersion:   d.DriverVersion,
		BitLength:       bits,
		IdemixIssuerPK:  ipkBytes,
		CurveID:         curveID,
		ProofType:       proofType,
		GeneratorDomain: d.GeneratorDomain,
	})
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

// parseArgs reads the generator arguments: the idemix root path, whose issuer public key it
// returns, and the range proof bit length.
func parseArgs(args []any) ([]byte, uint64, error) {
	if len(args) != 2 {
		return nil, 0, errors.Errorf("invalid number of arguments, expected 2, got %d", len(args))
	}
	idemixRootPath, ok := args[0].(string)
	if !ok {
		return nil, 0, errors.Errorf("invalid argument type, expected string, got %T", args[0])
	}
	path := filepath.Join(idemixRootPath, msp.IdemixConfigDirMsp, msp.IdemixConfigFileIssuerPublicKey)
	ipkBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}

	baseArg, ok := args[1].(string)
	if !ok {
		return nil, 0, errors.Errorf("invalid argument type, expected string, got %T", args[1])
	}
	bits, err := strconv.ParseUint(baseArg, 10, 32)
	if err != nil {
		return nil, 0, err
	}

	return ipkBytes, bits, nil
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
				return errors.WithMessagef(err, "failed to create x509 identity for issuer [%v]", issuer)
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
