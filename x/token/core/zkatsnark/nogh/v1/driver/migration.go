/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// newMigrationProver returns the prover of ZKAT-DLOG migration proofs, or nil
// when the public parameters embed no ZKAT-DLOG public parameters or carry no
// migration proving key: the issuer then refuses ZKAT-DLOG upgrades.
func newMigrationProver(p *pp.PublicParams, proverParams *prepared.Prover) (*prover.MigrationProver, error) {
	if len(p.LegacyDLogPublicParams) == 0 || len(p.PKMigration) == 0 {
		return nil, nil
	}
	withMigration, err := proverParams.WithMigration()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to prepare the migration proving key")
	}

	return prover.NewMigrationProver(withMigration)
}

// legacyDLogResolver returns the resolver of the ZKAT-DLOG formats that can be
// upgraded, or nil when the public parameters embed no ZKAT-DLOG public
// parameters.
func legacyDLogResolver(p *pp.PublicParams) (upgrade.PublicParamsResolver, error) {
	legacy, err := p.LegacyDLog()
	if errors.Is(err, pp.ErrNoLegacyDLog) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return token.NewLegacyDLogResolver(legacy)
}
