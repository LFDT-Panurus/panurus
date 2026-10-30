/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package setup

import (
	"maps"
	"strings"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/params"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// Supported proof system identifiers, as stored in pp.PublicParams.ProofSystem.
const (
	ProofSystemGroth16  = "groth16"
	ProofSystemPlonk    = "plonk"
	ProofSystemPlonkFRI = "plonkfri"
)

// GenerateOptions configures GeneratePublicParams. Zero values fall back to
// the defaults of pp.DefaultPublicParams().
type GenerateOptions struct {
	// Version is the scheme version of the public parameters. Zero means 1.
	Version driver.TokenDriverVersion
	// ProofSystem is one of groth16, plonk, plonkfri. Empty means params.DefaultProofSystem.
	ProofSystem string
	// Curve is the outer SNARK curve (BN254 or BLS12-381). ecc.UNKNOWN means params.DefaultCurve.
	Curve ecc.ID
	// MaxBits bounds the token value range inside the circuits. Zero means params.DefaultMaxBits.
	MaxBits int
	// Precision is the token quantity precision. Zero means MaxBits.
	// It must not exceed MaxBits, otherwise quantities accepted by the
	// token layer could not be proven by the circuits.
	Precision uint64
	// IdemixIssuerPublicKeys lists the idemix issuer public keys. At least one is required.
	IdemixIssuerPublicKeys []*pp.IdemixIssuerPublicKey
	// Issuers lists the identities allowed to issue tokens. Empty means open-policy mode.
	Issuers []driver.Identity
	// Auditors lists the auditor identities.
	Auditors []driver.Identity
	// Extras carries additional driver-agnostic parameters.
	Extras driver.Extras
}

// GeneratePublicParams builds a fresh zkatsnark PublicParams from opts,
// validates it, and runs SetupAll so that the circuit proving and
// verification keys are populated. The result is ready to be serialized
// and deployed.
//
// SetupAll is a single-party setup: for groth16 the per-circuit toxic waste
// is known to the process running it, and plonk uses an unsafe (test) KZG
// SRS. plonkfri is transparent and needs no trusted setup.
func GeneratePublicParams(opts GenerateOptions) (*pp.PublicParams, error) {
	p := pp.DefaultPublicParams()
	if opts.Version != 0 {
		p.SchemeVersion = opts.Version
	}
	if opts.ProofSystem != "" {
		ps, err := ParseProofSystem(opts.ProofSystem)
		if err != nil {
			return nil, err
		}
		p.ProofSystem = ps
	}
	if opts.Curve != ecc.UNKNOWN {
		p.Curve = opts.Curve
	}
	if opts.MaxBits < 0 {
		return nil, errors.Errorf("setup: MaxBits must be positive, got %d", opts.MaxBits)
	}
	if opts.MaxBits != 0 {
		p.MaxBits = opts.MaxBits
	}
	if p.MaxBits > params.MaxAllowedBits {
		return nil, errors.Errorf("setup: MaxBits must be at most %d, got %d", params.MaxAllowedBits, p.MaxBits)
	}
	p.PrecisionValue = uint64(p.MaxBits) // #nosec G115 -- 0 < MaxBits <= 64
	if opts.Precision != 0 {
		if opts.Precision > uint64(p.MaxBits) { // #nosec G115
			return nil, errors.Errorf("setup: precision [%d] must not exceed MaxBits [%d]", opts.Precision, p.MaxBits)
		}
		p.PrecisionValue = opts.Precision
	}

	if len(opts.IdemixIssuerPublicKeys) == 0 {
		return nil, errors.New("setup: at least one idemix issuer public key is required")
	}
	p.IdemixIssuerPublicKeys = opts.IdemixIssuerPublicKeys
	p.IssuerIdentities = opts.Issuers
	p.AuditorIdentities = opts.Auditors
	p.ExtraParams = driver.Extras{}
	maps.Copy(p.ExtraParams, opts.Extras)

	if err := p.Validate(); err != nil {
		return nil, errors.Wrap(err, "setup: invalid public parameters")
	}
	if _, err := SetupAll(p); err != nil {
		return nil, errors.Wrapf(err, "setup: failed to set up %s circuits", p.ProofSystem)
	}

	return p, nil
}

// ParseProofSystem normalizes and validates a proof system name
// (groth16, plonk or plonkfri, case-insensitive).
func ParseProofSystem(s string) (string, error) {
	ps := strings.ToLower(strings.TrimSpace(s))
	switch ps {
	case ProofSystemGroth16, ProofSystemPlonk, ProofSystemPlonkFRI:
		return ps, nil
	default:
		return "", errors.Errorf("setup: unsupported proof system [%s], expected one of groth16, plonk, plonkfri", s)
	}
}

// ParseCurve parses a SNARK curve name (bn254 or bls12_381, case-insensitive)
// and rejects curves the zkatsnark driver does not support.
func ParseCurve(s string) (ecc.ID, error) {
	id, err := ecc.IDFromString(strings.TrimSpace(s))
	if err != nil {
		return ecc.UNKNOWN, errors.Wrapf(err, "setup: invalid curve [%s]", s)
	}
	if id != ecc.BN254 && id != ecc.BLS12_381 {
		return ecc.UNKNOWN, errors.Errorf("setup: unsupported curve [%s], expected bn254 or bls12_381", s)
	}

	return id, nil
}
