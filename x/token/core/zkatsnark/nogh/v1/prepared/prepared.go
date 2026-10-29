/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package prepared holds the augmented ("installed") form of the zkatsnark
// public parameters: the proof-system backend, the compiled constraint
// systems, and the deserialized proving/verifying keys, built once when
// public parameters are installed, so that no proving or verification path
// ever deserializes a key or recompiles a circuit again.
//
// Preparation is split by role because the two roles need different keys:
// a Prover needs proving keys (hundreds of kilobytes to megabytes each,
// per circuit); a Verifier needs only the small verifying keys and never
// touches a proving key. A validator-only process (for example
// cmd/token_validation_service) should never pay to deserialize proving
// keys it will never use — hence Verifier and Prover are separate types
// built from a shared Params base.
//
// Everything in this package is immutable after construction and safe for
// concurrent use by any number of goroutines: gnark's Prove and Verify do
// not mutate the proving or verifying key they are given (the only gnark
// key mutations are ProvingKey.SetAccelerator and VerifyingKey.ExportSolidity,
// neither reachable from this package), and the compiled constraint systems
// are only ever read by Prove/Verify here.
package prepared

import (
	"github.com/consensys/gnark/constraint"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
)

// Params is the role-independent part of the prepared public parameters:
// the raw public parameters, the proof-system backend selected by
// pp.ProofSystem/pp.Curve, and the compiled spend and output constraint
// systems. The compiled migration constraint system is only present after
// WithMigration.
//
// A Params (and any Verifier/Prover built from it) always has its
// constraint systems and keys sourced from the same *pp.PublicParams, which
// is what guarantees a key is never handed to gnark alongside a constraint
// system compiled for a different curve or proof system — that specific
// combination still panics inside gnark (it dispatches on the constraint
// system's concrete type before asserting the key), so this invariant must
// be preserved by any code that constructs a Params directly.
type Params struct {
	pp          *pp.PublicParams
	backend     backend.ProofSystemBackend
	spendCS     constraint.ConstraintSystem
	outputCS    constraint.ConstraintSystem
	migrationCS constraint.ConstraintSystem
}

// PP returns the underlying public parameters.
func (b *Params) PP() *pp.PublicParams { return b.pp }

// Backend returns the proof-system backend selected for these parameters.
//
//nolint:ireturn // returns the ProofSystemBackend interface, same as backend.NewProofSystemBackend
func (b *Params) Backend() backend.ProofSystemBackend { return b.backend }

// SpendCS returns the compiled spend circuit constraint system. Callers
// must treat the result as read-only.
func (b *Params) SpendCS() constraint.ConstraintSystem { return b.spendCS }

// OutputCS returns the compiled output circuit constraint system. Callers
// must treat the result as read-only.
func (b *Params) OutputCS() constraint.ConstraintSystem { return b.outputCS }

// MigrationCS returns the compiled migration circuit constraint system, or
// nil if WithMigration has not been called. Callers must treat the result
// as read-only.
func (b *Params) MigrationCS() constraint.ConstraintSystem { return b.migrationCS }

// NewParams compiles the spend and output circuits for p and creates the
// proof-system backend selected by p.ProofSystem and p.Curve. It performs
// no key deserialization. Call once per set of public parameters.
func NewParams(p *pp.PublicParams) (*Params, error) {
	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: creating proof system backend")
	}

	spendCS, err := setup.CompileSpendCircuit(p)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: compiling spend circuit")
	}

	outputCS, err := setup.CompileOutputCircuit(p)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: compiling output circuit")
	}

	return &Params{pp: p, backend: b, spendCS: spendCS, outputCS: outputCS}, nil
}

// Verifier is the prepared public parameters for a verifying role: Params
// plus the deserialized spend and output verifying keys. It deliberately
// holds no proving keys.
type Verifier struct {
	*Params
	vkSpend     *backend.VerifyingKey
	vkOutput    *backend.VerifyingKey
	vkMigration *backend.VerifyingKey
}

// VKSpend returns the deserialized spend verifying key.
func (v *Verifier) VKSpend() *backend.VerifyingKey { return v.vkSpend }

// VKOutput returns the deserialized output verifying key.
func (v *Verifier) VKOutput() *backend.VerifyingKey { return v.vkOutput }

// VKMigration returns the deserialized migration verifying key, or nil if
// this deployment has no migration verifying key configured.
func (v *Verifier) VKMigration() *backend.VerifyingKey { return v.vkMigration }

// NewVerifier prepares p for a verifying role: compiles the spend and
// output circuits and deserializes the spend and output verifying keys.
// Missing or malformed spend/output keys are both hard errors. The
// migration verifying key is loaded opportunistically: absent is tolerated
// (VKMigration returns nil; migration validation fails closed), but present
// and malformed is a hard error.
func NewVerifier(p *pp.PublicParams) (*Verifier, error) {
	base, err := NewParams(p)
	if err != nil {
		return nil, err
	}

	return NewVerifierFrom(base)
}

// NewVerifierFrom builds a Verifier over an already-prepared Params, for
// callers that build both a Prover and a Verifier over the same parameters
// and want to share the compiled constraint systems.
func NewVerifierFrom(base *Params) (*Verifier, error) {
	vkSpend, err := loadVerifyingKey(base, setup.CircuitSpend)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: loading spend verifying key")
	}

	vkOutput, err := loadVerifyingKey(base, setup.CircuitOutput)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: loading output verifying key")
	}

	v := &Verifier{Params: base, vkSpend: vkSpend, vkOutput: vkOutput}

	vkMigration, err := loadVerifyingKey(base, setup.CircuitMigration)
	switch {
	case errors.Is(err, setup.ErrMissingKey):
		// No migration verifying key configured for this deployment. Leave
		// vkMigration nil; ValidateMigration fails closed with
		// ErrMigrationNotConfigured.
	case err != nil:
		return nil, errors.Wrapf(err, "prepared: loading migration verifying key")
	default:
		v.vkMigration = vkMigration
	}

	return v, nil
}

// WithMigration returns a copy of v additionally holding the compiled
// migration circuit and the migration verifying key. Returns an error if
// p's public parameters carry no migration verifying key at all (absent is
// tolerated by NewVerifier/NewVerifierFrom for the default, non-migration
// deployment; a caller that explicitly asks for migration support wants a
// hard error when it is unavailable).
func (v *Verifier) WithMigration(gens setup.PedersenGeneratorCoords) (*Verifier, error) {
	if v.vkMigration == nil {
		return nil, errors.Wrapf(setup.ErrMissingKey, "prepared: no migration verifying key configured")
	}

	migrationCS, err := setup.CompileMigrationCircuit(v.pp, gens)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: compiling migration circuit")
	}

	base := *v.Params
	base.migrationCS = migrationCS
	next := *v
	next.Params = &base

	return &next, nil
}

// Prover is the prepared public parameters for a proving role: Params plus
// the deserialized spend and output proving keys.
type Prover struct {
	*Params
	pkSpend     *backend.ProvingKey
	pkOutput    *backend.ProvingKey
	pkMigration *backend.ProvingKey
}

// PKSpend returns the deserialized spend proving key.
func (p *Prover) PKSpend() *backend.ProvingKey { return p.pkSpend }

// PKOutput returns the deserialized output proving key.
func (p *Prover) PKOutput() *backend.ProvingKey { return p.pkOutput }

// PKMigration returns the deserialized migration proving key, or nil if
// this deployment has no migration proving key configured.
func (p *Prover) PKMigration() *backend.ProvingKey { return p.pkMigration }

// NewProver prepares p for a proving role: compiles the spend and output
// circuits and deserializes the spend and output proving keys. Missing or
// malformed spend/output keys are both hard errors. The migration proving
// key is loaded opportunistically, the same way NewVerifierFrom loads the
// migration verifying key.
func NewProver(p *pp.PublicParams) (*Prover, error) {
	base, err := NewParams(p)
	if err != nil {
		return nil, err
	}

	return NewProverFrom(base)
}

// NewProverFrom builds a Prover over an already-prepared Params.
func NewProverFrom(base *Params) (*Prover, error) {
	pkSpend, err := loadProvingKey(base, setup.CircuitSpend)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: loading spend proving key")
	}

	pkOutput, err := loadProvingKey(base, setup.CircuitOutput)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: loading output proving key")
	}

	p := &Prover{Params: base, pkSpend: pkSpend, pkOutput: pkOutput}

	pkMigration, err := loadProvingKey(base, setup.CircuitMigration)
	switch {
	case errors.Is(err, setup.ErrMissingKey):
		// No migration proving key configured for this deployment. Leave
		// pkMigration nil.
	case err != nil:
		return nil, errors.Wrapf(err, "prepared: loading migration proving key")
	default:
		p.pkMigration = pkMigration
	}

	return p, nil
}

// WithMigration returns a copy of p additionally holding the compiled
// migration circuit and the migration proving key. Returns an error if p's
// public parameters carry no migration proving key at all.
func (p *Prover) WithMigration(gens setup.PedersenGeneratorCoords) (*Prover, error) {
	if p.pkMigration == nil {
		return nil, errors.Wrapf(setup.ErrMissingKey, "prepared: no migration proving key configured")
	}

	migrationCS, err := setup.CompileMigrationCircuit(p.pp, gens)
	if err != nil {
		return nil, errors.Wrapf(err, "prepared: compiling migration circuit")
	}

	base := *p.Params
	base.migrationCS = migrationCS
	next := *p
	next.Params = &base

	return &next, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func loadVerifyingKey(base *Params, name setup.CircuitName) (*backend.VerifyingKey, error) {
	raw, err := setup.LoadVerifyingKeyBytes(base.pp, name)
	if err != nil {
		return nil, err
	}

	return base.backend.NewVerifyingKey(raw)
}

func loadProvingKey(base *Params, name setup.CircuitName) (*backend.ProvingKey, error) {
	raw, err := setup.LoadProvingKeyBytes(base.pp, name)
	if err != nil {
		return nil, err
	}

	return base.backend.NewProvingKey(raw)
}
