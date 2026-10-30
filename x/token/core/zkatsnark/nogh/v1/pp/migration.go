/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"crypto/sha256"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	dlogsetup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
)

// ErrNoLegacyDLog is returned by LegacyDLog when no ZKAT-DLOG public
// parameters are embedded, that is, ZKAT-DLOG upgrades are not supported.
var ErrNoLegacyDLog = errors.New("pp: no ZKAT-DLOG public parameters configured")

// LegacyDLog is the parsed form of PublicParams.LegacyDLogPublicParams.
type LegacyDLog struct {
	// PP are the ZKAT-DLOG public parameters.
	PP *dlogsetup.PublicParams
	// Hash is the SHA-256 digest of the serialized ZKAT-DLOG public
	// parameters, as stored in LegacyDLogPublicParams. Upgrade proofs bind
	// the ZKAT-DLOG tokens they cover to this hash.
	Hash driver.PPHash
}

// IsLegacyDLogCurve reports whether curve is a BLS12-381 mathlib curve, the
// only ZKAT-DLOG curves the migration circuit can open commitments on. The BBS
// variants differ from the plain ones only in their hash-to-curve map, which
// matters only to derive the Pedersen generators: those are read from the
// ZKAT-DLOG public parameters, never re-derived.
func IsLegacyDLogCurve(curve mathlib.CurveID) bool {
	switch curve {
	case mathlib.BLS12_381, mathlib.BLS12_381_GURVY, mathlib.BLS12_381_BBS, mathlib.BLS12_381_BBS_GURVY:
		return true
	default:
		return false
	}
}

// LegacyDLog parses and validates the embedded ZKAT-DLOG public parameters.
// It returns ErrNoLegacyDLog when none are embedded. The result is not cached:
// long-lived consumers (the prepared parameters, the tokens and upgrade
// services) parse it once at construction.
func (pp *PublicParams) LegacyDLog() (*LegacyDLog, error) {
	if len(pp.LegacyDLogPublicParams) == 0 {
		return nil, ErrNoLegacyDLog
	}
	legacy, err := dlogsetup.NewPublicParamsFromBytes(pp.LegacyDLogPublicParams, dlogsetup.DLogNoGHDriverName, dlogsetup.ProtocolV1)
	if err != nil {
		return nil, errors.Wrap(err, "pp: invalid ZKAT-DLOG public parameters")
	}
	if err := legacy.Validate(); err != nil {
		return nil, errors.Wrap(err, "pp: invalid ZKAT-DLOG public parameters")
	}
	if !IsLegacyDLogCurve(legacy.Curve) {
		return nil, errors.Errorf("pp: ZKAT-DLOG public parameters on curve [%s] cannot be upgraded, BLS12-381 is required", legacy.Curve)
	}
	digest := sha256.Sum256(pp.LegacyDLogPublicParams)

	return &LegacyDLog{PP: legacy, Hash: digest[:]}, nil
}

// validateMigration checks the ZKAT-DLOG upgrade configuration: migration keys
// require embedded ZKAT-DLOG public parameters, and embedded ZKAT-DLOG public
// parameters require a well-formed, BLS12-381 ZKAT-DLOG deployment, a
// BLS12-381 zkatsnark curve, a proof system the migration circuit supports, and
// a migration verifying key.
func (pp *PublicParams) validateMigration() error {
	if len(pp.LegacyDLogPublicParams) == 0 {
		if len(pp.VKMigration) != 0 || len(pp.PKMigration) != 0 {
			return errors.New("pp: migration keys require ZKAT-DLOG public parameters")
		}

		return nil
	}
	if _, err := pp.LegacyDLog(); err != nil {
		return err
	}
	if pp.Curve != ecc.BLS12_381 {
		return errors.Errorf("pp: ZKAT-DLOG upgrades require Curve %s, got %s", ecc.BLS12_381, pp.Curve)
	}
	if pp.ProofSystem != "groth16" && pp.ProofSystem != "plonk" {
		return errors.Errorf("pp: ZKAT-DLOG upgrades are not supported with ProofSystem %q", pp.ProofSystem)
	}
	if len(pp.VKMigration) == 0 {
		return errors.New("pp: ZKAT-DLOG public parameters require a migration verifying key")
	}

	return nil
}
