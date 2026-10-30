/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package setup provides circuit compilation, key generation, and
// serialization utilities for the zkatsnark driver's proof system.
//
// This package is used in two distinct contexts:
//   - Offline, once: SetupAll compiles all circuits and runs the trusted
//     setup ceremony, populating a PublicParams with proving and
//     verification keys for distribution.
//   - Online, per-process startup: the prover and validator layers call
//     Compile*Circuit to reconstruct the constraint system (deterministic
//     and cheap, ~100ms) and LoadProvingKey/LoadVerifyingKey to deserialize
//     the keys already present in a distributed PublicParams. Neither of
//     these paths calls Setup, that only ever runs inside SetupAll.
package setup

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	dlogsetup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/params"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
)

// CircuitName identifies which circuit a proving/verification key or
// compiled constraint system belongs to. A named type instead of a bare
// string means an arbitrary, mistyped string can never be passed to
// LoadProvingKey/LoadVerifyingKey — only these three values compile.
type CircuitName string

const (
	CircuitSpend     CircuitName = "spend"
	CircuitOutput    CircuitName = "output"
	CircuitMigration CircuitName = "migration"
)

// ErrMissingKey is returned when a requested proving or verification key is
// absent from the given PublicParams i.e. setup.SetupAll has not yet been
// run, or was run without that particular circuit.
var ErrMissingKey = errors.New("setup: key missing from public parameters")

// ErrNotImplemented is returned for proof-system paths that are declared in
// PublicParams.ProofSystem but not yet implemented in this package.
var ErrNotImplemented = errors.New("setup: not yet implemented")

// ── Curve-dependent generator selection ─────────────────────────────────────────

// generatorCoords holds the affine coordinates of V and R generators
// as *big.Int, suitable for injection into circuit struct fields.
type generatorCoords struct {
	VX, VY *big.Int
	RX, RY *big.Int
}

// curveGenerators returns the value-commitment generators (V, R) for the
// given outer curve, as *big.Int coordinates suitable for circuit struct
// field injection. This is the single resolution point shared by all three
// Compile*Circuit functions.
func curveGenerators(curve ecc.ID) (generatorCoords, error) {
	switch curve {
	case ecc.BLS12_381:
		return generatorCoords{
			VX: edwards.Jubjub().V.X.BigInt(new(big.Int)),
			VY: edwards.Jubjub().V.Y.BigInt(new(big.Int)),
			RX: edwards.Jubjub().R.X.BigInt(new(big.Int)),
			RY: edwards.Jubjub().R.Y.BigInt(new(big.Int)),
		}, nil
	case ecc.BN254:
		return generatorCoords{
			VX: edwards.BabyJubjub().V.X.BigInt(new(big.Int)),
			VY: edwards.BabyJubjub().V.Y.BigInt(new(big.Int)),
			RX: edwards.BabyJubjub().R.X.BigInt(new(big.Int)),
			RY: edwards.BabyJubjub().R.Y.BigInt(new(big.Int)),
		}, nil
	default:
		return generatorCoords{}, errors.Errorf("setup: no value-commitment generators for curve %s", curve)
	}
}

// csBuilder returns the appropriate constraint system builder for the
// given proof system: r1cs.NewBuilder for groth16, scs.NewBuilder for plonk and plonkfri.
func csBuilder(proofSystem string) (frontend.NewBuilder, error) {
	switch proofSystem {
	case "groth16":
		return r1cs.NewBuilder, nil
	case "plonk", "plonkfri":
		return scs.NewBuilder, nil
	default:
		return nil, errors.Errorf("setup: unsupported proof system %q", proofSystem)
	}
}

// ── Circuit compilation ────────────────────────────────────────────────────────
//
// These functions are deterministic given the same PublicParams field
// values that affect circuit shape (currently Curve, ProofSystem, and for
// OutputCircuit, MaxBits). Two calls with equivalent PublicParams always
// produce an equivalent constraint system. They are cheap (well under a
// second) and safe to call once per process at startup — there is no need
// to cache or serialize the constraint system itself.

// CompileSpendCircuit compiles circuit.SpendCircuit for the curve and proof
// system specified in p.
func CompileSpendCircuit(p *pp.PublicParams) (constraint.ConstraintSystem, error) {
	if p.MaxBits <= 0 {
		return nil, errors.Errorf("setup: cannot compile SpendCircuit: MaxBits must be positive, got %d", p.MaxBits)
	}
	if p.MaxBits > params.MaxAllowedBits {
		return nil, errors.Errorf("setup: cannot compile SpendCircuit: MaxBits must be at most %d, got %d", params.MaxAllowedBits, p.MaxBits)
	}

	gens, err := curveGenerators(p.Curve)
	if err != nil {
		return nil, err
	}

	builder, err := csBuilder(p.ProofSystem)
	if err != nil {
		return nil, err
	}

	cs, err := frontend.Compile(
		p.Curve.ScalarField(),
		builder,
		&circuit.SpendCircuit{
			MaxBits: p.MaxBits,
			GenVX:   gens.VX, GenVY: gens.VY,
			GenRX: gens.RX, GenRY: gens.RY,
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: SpendCircuit compilation failed")
	}

	return cs, nil
}

// CompileOutputCircuit compiles circuit.OutputCircuit for the curve and
// proof system specified in p, with the range-check bit-width from p.MaxBits.
//
// MaxBits is a compile-time circuit parameter, not a witness value: two
// PublicParams differing only in MaxBits compile to two DIFFERENT circuits
// requiring separate keys. Always pass the same PublicParams (or one with
// an identical MaxBits) to CompileOutputCircuit as was used when the
// corresponding VKOutput/PKOutput were generated by SetupAll, otherwise
// proofs generated against one will not verify against the other.
func CompileOutputCircuit(p *pp.PublicParams) (constraint.ConstraintSystem, error) {
	if p.MaxBits <= 0 {
		return nil, errors.Errorf("setup: cannot compile OutputCircuit: MaxBits must be positive, got %d", p.MaxBits)
	}
	if p.MaxBits > params.MaxAllowedBits {
		return nil, errors.Errorf("setup: cannot compile OutputCircuit: MaxBits must be at most %d, got %d", params.MaxAllowedBits, p.MaxBits)
	}

	gens, err := curveGenerators(p.Curve)
	if err != nil {
		return nil, err
	}

	builder, err := csBuilder(p.ProofSystem)
	if err != nil {
		return nil, err
	}

	cs, err := frontend.Compile(
		p.Curve.ScalarField(),
		builder,
		&circuit.OutputCircuit{
			MaxBits: p.MaxBits,
			GenVX:   gens.VX, GenVY: gens.VY,
			GenRX: gens.RX, GenRY: gens.RY,
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: OutputCircuit compilation failed")
	}

	return cs, nil
}

// PedersenGeneratorCoords holds the affine coordinates of the three Pedersen
// generators of the ZKAT-DLOG deployment being upgraded, needed as
// compile-time constants for MigrationCircuit. They must be exactly the
// generators that created the on-chain commitments being redeemed, so they
// are only ever read from the ZKAT-DLOG public parameters (see
// PedersenGeneratorCoordsFromDLog), never re-derived.
type PedersenGeneratorCoords struct {
	G0X, G0Y *big.Int
	G1X, G1Y *big.Int
	G2X, G2Y *big.Int
}

// PedersenGeneratorCoordsFromDLog returns the coordinates of the Pedersen
// generators of legacy, which must be three non-identity points of the
// BLS12-381 G1 prime-order subgroup.
func PedersenGeneratorCoordsFromDLog(legacy *dlogsetup.PublicParams) (PedersenGeneratorCoords, error) {
	if legacy == nil {
		return PedersenGeneratorCoords{}, errors.New("setup: nil ZKAT-DLOG public parameters")
	}
	if !pp.IsLegacyDLogCurve(legacy.Curve) {
		return PedersenGeneratorCoords{}, errors.Errorf("setup: ZKAT-DLOG curve [%s] is not BLS12-381", legacy.Curve)
	}
	if len(legacy.PedersenGenerators) != 3 {
		return PedersenGeneratorCoords{}, errors.Errorf("setup: expected 3 ZKAT-DLOG Pedersen generators, got %d", len(legacy.PedersenGenerators))
	}
	var coords [3]struct{ X, Y *big.Int }
	for i, g := range legacy.PedersenGenerators {
		if g == nil {
			return PedersenGeneratorCoords{}, errors.Errorf("setup: nil ZKAT-DLOG Pedersen generator %d", i)
		}
		var pt bls12381.G1Affine
		if _, err := pt.SetBytes(g.Bytes()); err != nil {
			return PedersenGeneratorCoords{}, errors.Wrapf(err, "setup: invalid ZKAT-DLOG Pedersen generator %d", i)
		}
		if pt.IsInfinity() || !pt.IsInSubGroup() {
			return PedersenGeneratorCoords{}, errors.Errorf("setup: ZKAT-DLOG Pedersen generator %d is not a non-identity subgroup point", i)
		}
		coords[i].X = pt.X.BigInt(new(big.Int))
		coords[i].Y = pt.Y.BigInt(new(big.Int))
	}

	return PedersenGeneratorCoords{
		G0X: coords[0].X, G0Y: coords[0].Y,
		G1X: coords[1].X, G1Y: coords[1].Y,
		G2X: coords[2].X, G2Y: coords[2].Y,
	}, nil
}

// MigrationGenerators returns the Pedersen generator coordinates of the
// ZKAT-DLOG public parameters embedded in p.
func MigrationGenerators(p *pp.PublicParams) (PedersenGeneratorCoords, error) {
	legacy, err := p.LegacyDLog()
	if err != nil {
		return PedersenGeneratorCoords{}, err
	}

	return PedersenGeneratorCoordsFromDLog(legacy.PP)
}

// CompileMigrationCircuit compiles circuit.MigrationCircuit for the curve
// and proof system specified in p, with the range-check bit-width from
// p.MaxBits and the Pedersen generator coordinates from gens.
//
// The migration circuit opens BLS12-381 G1 commitments with BLS12-381 scalars,
// so it is only sound over the BLS12-381 scalar field: any other curve is
// refused, as is plonkfri, which the zkatsnark driver supports only on BN254.
//
// The Pedersen generators are compile-time constants baked into the
// constraint system. Two calls with different generators produce different
// circuits requiring separate keys.
func CompileMigrationCircuit(p *pp.PublicParams, gens PedersenGeneratorCoords) (constraint.ConstraintSystem, error) {
	if p.Curve != ecc.BLS12_381 {
		return nil, errors.Errorf("setup: cannot compile MigrationCircuit: Curve must be %s, got %s", ecc.BLS12_381, p.Curve)
	}
	if p.ProofSystem != "groth16" && p.ProofSystem != "plonk" {
		return nil, errors.Errorf("setup: cannot compile MigrationCircuit: unsupported proof system %q", p.ProofSystem)
	}
	if p.MaxBits <= 0 {
		return nil, errors.Errorf("setup: cannot compile MigrationCircuit: MaxBits must be positive, got %d", p.MaxBits)
	}
	if p.MaxBits > params.MaxAllowedBits {
		return nil, errors.Errorf("setup: cannot compile MigrationCircuit: MaxBits must be at most %d, got %d", params.MaxAllowedBits, p.MaxBits)
	}
	if gens.G0X == nil || gens.G0Y == nil || gens.G1X == nil || gens.G1Y == nil || gens.G2X == nil || gens.G2Y == nil {
		return nil, errors.New("setup: cannot compile MigrationCircuit: missing Pedersen generators")
	}

	vcGens, err := curveGenerators(p.Curve)
	if err != nil {
		return nil, err
	}

	builder, err := csBuilder(p.ProofSystem)
	if err != nil {
		return nil, err
	}

	cs, err := frontend.Compile(
		p.Curve.ScalarField(),
		builder,
		&circuit.MigrationCircuit{
			MaxBits: p.MaxBits,
			PedG0X:  gens.G0X, PedG0Y: gens.G0Y,
			PedG1X: gens.G1X, PedG1Y: gens.G1Y,
			PedG2X: gens.G2X, PedG2Y: gens.G2Y,
			GenVX: vcGens.VX, GenVY: vcGens.VY,
			GenRX: vcGens.RX, GenRY: vcGens.RY,
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: MigrationCircuit compilation failed")
	}

	return cs, nil
}

// SetupMigration compiles MigrationCircuit for the ZKAT-DLOG public parameters
// embedded in p (p.LegacyDLogPublicParams) and runs setup via the proof system
// backend, populating p.PKMigration/VKMigration in place. It fails if p
// embeds no ZKAT-DLOG public parameters.
//
// It is not called from SetupAll: migration keys are only needed by a
// deployment that upgrades ZKAT-DLOG outputs, and depend on the generators of
// that ZKAT-DLOG deployment.
func SetupMigration(p *pp.PublicParams) (*pp.PublicParams, error) {
	gens, err := MigrationGenerators(p)
	if err != nil {
		return nil, errors.Wrap(err, "setup: SetupMigration")
	}

	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: SetupMigration backend creation")
	}

	migCS, err := CompileMigrationCircuit(p, gens)
	if err != nil {
		return nil, err
	}

	pkBytes, vkBytes, err := b.Setup(migCS)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: MigrationCircuit setup failed")
	}

	p.PKMigration = pkBytes
	p.VKMigration = vkBytes

	if err := p.Validate(); err != nil {
		return nil, errors.Wrapf(err, "setup: resulting public params failed validation")
	}

	return p, nil
}

// ── Trusted setup ───────────────────────────────────────────────────────────────

// SetupAll compiles SpendCircuit and OutputCircuit and runs setup for
// each, populating p.PKSpend/VKSpend and p.PKOutput/VKOutput in place.
// Call this once, offline. It is slow (the setup ceremony itself takes
// seconds to tens of seconds per circuit depending on constraint count),
// do not call it on any request-serving path.
//
// The returned *pp.PublicParams is the same pointer passed in, mutated in
// place, returned for convenient chaining.
func SetupAll(p *pp.PublicParams) (*pp.PublicParams, error) {
	b, err := backend.NewProofSystemBackend(p.ProofSystem, p.Curve)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: SetupAll backend creation")
	}

	// ── SpendCircuit ──────────────────────────────────────────────────────
	spendCS, err := CompileSpendCircuit(p)
	if err != nil {
		return nil, err
	}
	pkBytes, vkBytes, err := b.Setup(spendCS)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: SpendCircuit setup failed")
	}
	p.PKSpend = pkBytes
	p.VKSpend = vkBytes

	// ── OutputCircuit ─────────────────────────────────────────────────────
	outputCS, err := CompileOutputCircuit(p)
	if err != nil {
		return nil, err
	}
	pkBytes, vkBytes, err = b.Setup(outputCS)
	if err != nil {
		return nil, errors.Wrapf(err, "setup: OutputCircuit setup failed")
	}
	p.PKOutput = pkBytes
	p.VKOutput = vkBytes

	if err := p.Validate(); err != nil {
		return nil, errors.Wrapf(err, "setup: resulting public params failed validation")
	}

	return p, nil
}

// ── Key loading by circuit type ───────────────────────────────────────────────
//
// These are the functions the prover and validator layers actually call at
// startup: given a fully-populated PublicParams (post-SetupAll, or received
// over the network already populated), load the raw key bytes for a named
// circuit.

// LoadProvingKeyBytes retrieves the serialized proving key bytes for the
// named circuit from p.
func LoadProvingKeyBytes(p *pp.PublicParams, circuitName CircuitName) ([]byte, error) {
	var raw []byte
	switch circuitName {
	case CircuitSpend:
		raw = p.PKSpend
	case CircuitOutput:
		raw = p.PKOutput
	case CircuitMigration:
		raw = p.PKMigration
	default:
		return nil, errors.Errorf("setup: unknown circuit name %q", circuitName)
	}
	if len(raw) == 0 {
		return nil, errors.Wrapf(ErrMissingKey, "no proving key for circuit %q", circuitName)
	}

	return raw, nil
}

// LoadVerifyingKeyBytes retrieves the serialized verification key bytes for
// the named circuit from p.
func LoadVerifyingKeyBytes(p *pp.PublicParams, circuitName CircuitName) ([]byte, error) {
	var raw []byte
	switch circuitName {
	case CircuitSpend:
		raw = p.VKSpend
	case CircuitOutput:
		raw = p.VKOutput
	case CircuitMigration:
		raw = p.VKMigration
	default:
		return nil, errors.Errorf("setup: unknown circuit name %q", circuitName)
	}
	if len(raw) == 0 {
		return nil, errors.Wrapf(ErrMissingKey, "no verifying key for circuit %q", circuitName)
	}

	return raw, nil
}
