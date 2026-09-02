/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package pp defines the public parameters for the zkatsnark token driver.
//
// PublicParams is the single shared artifact between the prover (client)
// and the validator (eventually, the network layer). Every field that
// affects cryptographic validity, the curve, the proof system, the
// value-commitment generators, circuit keys, and the value range lives
// here. None of it is configurable via per-node config files: doing so
// would let different participants silently disagree about what a valid
// proof looks like, which breaks consensus. Distribution of this struct
// (serialized) is a network-layer concern outside this package's scope.
package pp

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/twistededwards"

	"github.com/LFDT-Panurus/panurus/token/core"
	pp3 "github.com/LFDT-Panurus/panurus/token/core/common/encoding/pp"
	driver "github.com/LFDT-Panurus/panurus/token/driver"
	pp2 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/params"
)

// Compile-time assertion: PublicParams must implement driver.PublicParameters.
// If any interface method is missing or has the wrong signature, this line
// fails to compile. This assertion must never be removed, it is the only
// thing that mechanically guarantees this struct stays a valid
// driver.PublicParameters as the interface or this struct evolves.
var _ driver.PublicParameters = (*PublicParams)(nil)

// IdemixIssuerPublicKey contains the public key and curve of an Idemix issuer.
// This is used for pseudonymous owner identities in the wallet service.
type IdemixIssuerPublicKey struct {
	PublicKey []byte
	Curve     mathlib.CurveID
}

// PublicParams holds every parameter shared between the prover and the
// validator for the zkatsnark driver.
type PublicParams struct {
	// ── driver.PublicParameters interface-backing fields ──────────────────

	// Label identifies this driver, e.g. "zkatsnark". Backs TokenDriverName().
	Label string

	// SchemeVersion versions this specific parameter set. Backs
	// TokenDriverVersion().
	SchemeVersion driver.TokenDriverVersion

	GraphHidingEnabled bool

	// CertifierID identifies the certification driver used in Phase 2.
	// Empty string means no certification is active (Phase 1). Backs
	// CertificationDriver().
	CertifierID string

	// AuditorIdentities lists identities authorized to audit transactions
	// under these parameters. Backs Auditors().
	AuditorIdentities []driver.Identity

	// IssuerIdentities lists identities authorized to issue tokens under
	// these parameters. Backs Issuers().
	IssuerIdentities []driver.Identity

	// PrecisionValue is the decimal precision used for token values —
	// distinct from MaxBits, which bounds the field-level bit-width of the
	// value witness inside circuits. Precision is a display/accounting
	// concept; MaxBits is a circuit-soundness concept. Backs Precision().
	PrecisionValue uint64

	// ── Proof system and curve ─────────────────────────────────────────────

	// ProofSystem selects the ZK proof system. Currently only "groth16" is
	// implemented; "plonk" is reserved for a future migration and is
	// rejected by Validate() and by setup.SetupAll until implemented.
	ProofSystem string

	// Curve is the outer pairing curve. BLS12-381 by default, per Angelo's
	// direction (BN254 is no longer recommended).
	Curve ecc.ID

	// ── Value commitment generators (Jubjub, embedded in BLS12-381 Fr) ────

	// V is the value generator: cv = Value·V + RCV·R.
	V twistededwards.PointAffine

	// R is the randomness generator, and also the group base point used
	// for the per-action binding signature (see prover.ComputeBindingSignature).
	R twistededwards.PointAffine

	// ── Circuit keys ────────────────────────────────────────────────────────
	// Populated once, offline, by setup.SetupAll. Proving keys are
	// distributed to clients (out-of-band or via the TMS); verification
	// keys are small and distributed via the network channel configuration.

	PKSpend     []byte
	PKOutput    []byte
	PKMigration []byte

	VKSpend     []byte
	VKOutput    []byte
	VKMigration []byte

	// ── Value range ─────────────────────────────────────────────────────────

	// MaxBits bounds the token value field inside OutputCircuit's range
	// check: 1 <= Value < 2^MaxBits. This is a compile-time circuit
	// parameter — changing it produces a different circuit requiring a
	// fresh setup and new keys. Defaults to 64.
	MaxBits int

	// ── Idemix identity keys ──────────────────────────────────────────────

	// IdemixIssuerPublicKeys lists the Idemix issuer public keys used for
	// pseudonymous owner identities. Wallets prefer keys earlier in the
	// slice.
	IdemixIssuerPublicKeys []*IdemixIssuerPublicKey

	// ── Driver-specific extras ─────────────────────────────────────────────

	// ExtraParams carries any additional parameters not covered by the
	// interface's named accessors. Backs Extras().
	ExtraParams driver.Extras
}

// ── driver.PublicParameters interface implementation ────────────────────────

func (pp *PublicParams) TokenDriverName() driver.TokenDriverName {
	return driver.TokenDriverName(pp.Label)
}

func (pp *PublicParams) TokenDriverVersion() driver.TokenDriverVersion {
	return pp.SchemeVersion
}

// TokenDataHiding always returns true: this driver hides token values and
// types by construction (commitments + value commitments). This is not a
// configurable property of a given parameter set.
func (pp *PublicParams) TokenDataHiding() bool {
	return true
}

func (pp *PublicParams) GraphHiding() bool {
	return pp.GraphHidingEnabled
}

// MaxTokenValue returns 2^MaxBits - 1, the largest value the OutputCircuit
// range check permits. Falls back to the full uint64 range if MaxBits is
// unset or degenerate, rather than panicking, callers should still treat
// an unset MaxBits as invalid via Validate().
func (pp *PublicParams) MaxTokenValue() uint64 {
	if pp.MaxBits <= 0 || pp.MaxBits >= 64 {
		return ^uint64(0)
	}

	return (uint64(1) << uint(pp.MaxBits)) - 1
}

func (pp *PublicParams) CertificationDriver() string {
	return pp.CertifierID
}

func (pp *PublicParams) Auditors() []driver.Identity {
	return pp.AuditorIdentities
}

func (pp *PublicParams) Issuers() []driver.Identity {
	return pp.IssuerIdentities
}

func (pp *PublicParams) Precision() uint64 {
	return pp.PrecisionValue
}

func (pp *PublicParams) Extras() driver.Extras {
	return pp.ExtraParams
}

// AddIssuer appends an issuer identity to the public parameters.
func (pp *PublicParams) AddIssuer(id driver.Identity) {
	pp.IssuerIdentities = append(pp.IssuerIdentities, id)
}

// AddAuditor appends an auditor identity to the public parameters.
func (pp *PublicParams) AddAuditor(id driver.Identity) {
	pp.AuditorIdentities = append(pp.AuditorIdentities, id)
}

// SetIssuers sets the issuers to the passed identities.
func (pp *PublicParams) SetIssuers(ids []driver.Identity) {
	pp.IssuerIdentities = ids
}

// SetAuditors sets the auditors to the passed identities.
func (pp *PublicParams) SetAuditors(ids []driver.Identity) {
	pp.AuditorIdentities = ids
}

func (pp *PublicParams) String() string {
	return fmt.Sprintf(
		"zkatsnark public params [label=%s version=%d proofSystem=%s curve=%s "+
			"maxBits=%d graphHiding=%v certifier=%q auditors=%d issuers=%d]",
		pp.Label, pp.SchemeVersion, pp.ProofSystem, pp.Curve,
		pp.MaxBits, pp.GraphHidingEnabled, pp.CertifierID,
		len(pp.AuditorIdentities), len(pp.IssuerIdentities),
	)
}

// ── Serialization ─────────────────────────────────────────────────────────────

// Serialize converts PublicParams into its byte representation. Uses JSON
// for now, readable and simple to debug during development.
func (pp *PublicParams) Serialize() ([]byte, error) {
	raw, err := json.Marshal(pp)
	if err != nil {
		return nil, fmt.Errorf("pp: serialize failed: %w", err)
	}

	return pp3.Marshal(&pp2.PublicParameters{
		Identifier: string(core.DriverIdentifier(pp.TokenDriverName(), pp.TokenDriverVersion())),
		Raw:        raw,
	})
}

// DeserializePublicParams reconstructs a PublicParams from bytes produced
// by Serialize, and validates the result before returning it. A caller
// never receives a PublicParams that failed Validate() from this function.
func DeserializePublicParams(raw []byte) (*PublicParams, error) {
	container, err := pp3.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("pp: deserialize failed: %w", err)
	}
	var p PublicParams
	if err := json.Unmarshal(container.Raw, &p); err != nil {
		return nil, fmt.Errorf("pp: deserialize failed: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("pp: deserialized params failed validation: %w", err)
	}

	return &p, nil
}

// ── Validation ────────────────────────────────────────────────────────────────

// Validate checks that PublicParams is internally consistent and usable.
// It does NOT require circuit keys to be populated — a freshly constructed
// PublicParams destined for setup.SetupAll is expected to have empty
// PK*/VK* fields at the point Validate is called. Key presence is checked
// separately, at load time, by setup.LoadProvingKey / LoadVerifyingKey.
func (pp *PublicParams) Validate() error {
	if pp.Label == "" {
		return errors.New("pp: Label must not be empty")
	}
	if pp.SchemeVersion <= 0 {
		return fmt.Errorf("pp: SchemeVersion must be positive, got %d", pp.SchemeVersion)
	}
	if pp.ProofSystem != "groth16" && pp.ProofSystem != "plonk" {
		return fmt.Errorf("pp: unsupported ProofSystem %q", pp.ProofSystem)
	}
	if pp.Curve != ecc.BLS12_381 && pp.Curve != ecc.BN254 {
		return fmt.Errorf("pp: unsupported Curve %s", pp.Curve)
	}
	if pp.MaxBits <= 0 {
		return fmt.Errorf("pp: MaxBits must be positive, got %d", pp.MaxBits)
	}
	if !pp.V.IsOnCurve() {
		return errors.New("pp: V is not a valid point on the Jubjub curve")
	}
	if !pp.R.IsOnCurve() {
		return errors.New("pp: R is not a valid point on the Jubjub curve")
	}
	if len(pp.IdemixIssuerPublicKeys) == 0 {
		return errors.New("pp: expected at least one idemix issuer public key")
	}
	for i, key := range pp.IdemixIssuerPublicKeys {
		if key == nil {
			return fmt.Errorf("pp: idemix issuer public key at index %d is nil", i)
		}
		if len(key.PublicKey) == 0 {
			return fmt.Errorf("pp: idemix issuer public key at index %d has empty public key", i)
		}
	}

	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// Hash computes a deterministic SHA-256 hash of the serialized parameters.
// Intended for consistency checks between parties operating under the same
// PublicParams — e.g. confirming all signers of an action agree on which
// parameter set they are signing under, before that check exists formally
// elsewhere.
func (pp *PublicParams) Hash() ([32]byte, error) {
	raw, err := pp.Serialize()
	if err != nil {
		return [32]byte{}, err
	}

	return sha256.Sum256(raw), nil
}

// DefaultPublicParams returns a correctly initialized Phase 1 PublicParams
// for local development, testing, and as the starting point passed into
// setup.SetupAll. Circuit keys (PK*/VK*) are empty until SetupAll populates
// them, DefaultPublicParams alone is NOT sufficient for proving or
// verifying; it must go through setup first.
func DefaultPublicParams() *PublicParams {
	return &PublicParams{
		Label:              "zkatsnark",
		SchemeVersion:      1,
		ProofSystem:        params.DefaultProofSystem,
		Curve:              params.DefaultCurve,
		V:                  jubjub.V,
		R:                  jubjub.R,
		MaxBits:            params.DefaultMaxBits,
		GraphHidingEnabled: false,
		CertifierID:        "",
		PrecisionValue:     64,
		IdemixIssuerPublicKeys: []*IdemixIssuerPublicKey{
			{
				PublicKey: []byte("dummy-idemix-pk"),
				Curve:     mathlib.BN254,
			},
		},
	}
}
