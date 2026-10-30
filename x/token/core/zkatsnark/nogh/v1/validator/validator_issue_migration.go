/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"bytes"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/legacy"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

// decodedMigration is the public data of one ZKAT-DLOG input of an upgrade
// issuance: the Pedersen point, read from the ledger bytes of the redeemed
// token (never from the prover), and the value commitment of the migration
// proof.
type decodedMigration struct {
	PedersenX, PedersenY *big.Int
	// spend carries the value commitment, and the Pedersen point X || Y as the
	// input's commitment, for the binding signature.
	spend decodedSpend
	proof []byte
}

// migrationConfigured fails closed with ErrMigrationNotConfigured unless both
// the migration verifying key and the ZKAT-DLOG public parameters are
// available, and the zkatsnark curve is BLS12-381.
func (v *Validator) migrationConfigured() error {
	if v.prepared.VKMigration() == nil {
		return errors.Wrap(ErrMigrationNotConfigured, "no migration verifying key")
	}
	if v.legacy == nil || v.legacy.PP == nil {
		return errors.Wrap(ErrMigrationNotConfigured, "no ZKAT-DLOG public parameters")
	}
	if v.pp.Curve != ecc.BLS12_381 {
		return errors.Wrapf(ErrMigrationNotConfigured, "curve %s", v.pp.Curve)
	}

	return nil
}

// decodeMigrationInputs parses every input of a ZKAT-DLOG upgrade issuance:
// the ledger token must be a well-formed ZKAT-DLOG output of the configured
// generation (see snarktoken.SummarizeDLogInputs), and the migration
// description must carry a canonical value commitment on the embedded twisted
// Edwards curve and a non-empty proof.
func (v *Validator) decodeMigrationInputs(a *snarktoken.IssueAction) ([]decodedMigration, error) {
	if err := v.migrationConfigured(); err != nil {
		return nil, err
	}
	parsed, err := snarktoken.SummarizeDLogInputs(a.Inputs, v.legacy.PP)
	if err != nil {
		return nil, errors.WithMessagef(errors.Join(ErrMalformedAction, err), "%s", err.Error())
	}
	out := make([]decodedMigration, len(a.Inputs))
	for i, in := range a.Inputs {
		m := in.Migration
		if m == nil {
			return nil, errors.Wrapf(ErrMalformedAction, "ZKAT-DLOG input %d has no migration proof", i)
		}
		if len(m.ValueCommitInX) != fieldElementLen || len(m.ValueCommitInY) != fieldElementLen {
			return nil, errors.Wrapf(ErrMalformedAction, "ZKAT-DLOG input %d: value commitment coordinates must be %d bytes", i, fieldElementLen)
		}
		if len(m.MigrationProof) == 0 {
			return nil, errors.Wrapf(ErrMalformedAction, "ZKAT-DLOG input %d: MigrationProof must not be empty", i)
		}
		d := decodedMigration{proof: m.MigrationProof}
		d.spend.ValueCommitXBytes = m.ValueCommitInX
		d.spend.ValueCommitYBytes = m.ValueCommitInY
		if err := decodeFields(v.pp.Curve,
			namedField{"ValueCommitInX", m.ValueCommitInX, &d.spend.ValueCommitX},
			namedField{"ValueCommitInY", m.ValueCommitInY, &d.spend.ValueCommitY},
		); err != nil {
			return nil, errors.Wrapf(err, "ZKAT-DLOG input %d", i)
		}
		if err := checkValueCommitPoint(v.pp.Curve, m.ValueCommitInX, m.ValueCommitInY); err != nil {
			return nil, errors.Wrapf(err, "ZKAT-DLOG input %d", i)
		}
		xy := parsed[i].PedersenXY
		d.spend.CommitmentBytes = xy
		d.PedersenX = new(big.Int).SetBytes(xy[:legacy.PedersenCoordinateSize])
		d.PedersenY = new(big.Int).SetBytes(xy[legacy.PedersenCoordinateSize:])
		out[i] = d
	}

	return out, nil
}

// publicWitnessForMigration builds the public-only witness of
// circuit.MigrationCircuit. Every public input is set by the validator: the
// Pedersen point comes from the ledger bytes and tokenTypePed from the
// action's cleartext token type, so a proof only verifies if the redeemed
// commitment opens to that type.
func publicWitnessForMigration(d decodedMigration, tokenTypePed *big.Int, curve ecc.ID) (witness.Witness, error) {
	assignment := &circuit.MigrationCircuit{
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](d.PedersenX),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](d.PedersenY),
		TokenTypePed:        tokenTypePed,
		ValueCommitInX:      d.spend.ValueCommitX,
		ValueCommitInY:      d.spend.ValueCommitY,
	}
	w, err := frontend.NewWitness(assignment, curve.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return nil, errors.Wrapf(err, "validator: building migration public witness")
	}

	return w, nil
}

// verifyMigrationProofs verifies every migration proof in parallel against
// the migration verifying key, with the type scalar of tokenType.
func (v *Validator) verifyMigrationProofs(decoded []decodedMigration, tokenType string) error {
	if err := v.migrationConfigured(); err != nil {
		return err
	}
	tokenTypePed := snarktoken.TokenTypePed(tokenType)
	ch := make(chan proofOutcome, len(decoded))
	for i := range decoded {
		go func() {
			pw, err := publicWitnessForMigration(decoded[i], tokenTypePed, v.pp.Curve)
			if err != nil {
				ch <- proofOutcome{err}

				return
			}
			if err := v.prepared.Backend().Verify(v.prepared.VKMigration(), decoded[i].proof, pw); err != nil {
				ch <- proofOutcome{errors.Wrapf(ErrInvalidProof, "migration input %d: %s", i, err)}

				return
			}
			ch <- proofOutcome{}
		}()
	}
	var firstErr error
	for range decoded {
		if o := <-ch; o.err != nil && firstErr == nil {
			firstErr = o.err
		}
	}

	return firstErr
}

// verifyDLogUpgradeCrypto runs the cryptographic checks specific to an upgrade
// issuance of ZKAT-DLOG outputs, after the outputs have been decoded and their
// proofs verified: one migration proof per input, TotalValue equal to zero
// (the redeemed values stay hidden), and a binding signature, under
// ActionTypeIssueMigration, that conserves value between the migration value
// commitments and the outputs.
func (v *Validator) verifyDLogUpgradeCrypto(a *snarktoken.IssueAction, decodedOutputs []decodedOutput) error {
	if len(a.TokenType) == 0 {
		return errors.Wrap(ErrMalformedAction, "ZKAT-DLOG upgrade without a token type")
	}
	decoded, err := v.decodeMigrationInputs(a)
	if err != nil {
		return err
	}
	if !bytes.Equal(a.TotalValue, make([]byte, fieldElementLen)) {
		return errors.Wrap(ErrMalformedAction, "ZKAT-DLOG upgrade must have a zero TotalValue")
	}
	if err := v.verifyMigrationProofs(decoded, a.TokenType); err != nil {
		return err
	}
	inputs := make([]decodedSpend, len(decoded))
	for i := range decoded {
		inputs[i] = decoded[i].spend
	}

	return verifyBindingSignature(
		v.pp.Curve,
		snarktoken.ActionTypeIssueMigration, a.TypeCommitment,
		inputs, decodedOutputs,
		a.BindingSignature, 0,
	)
}

// validateDLogUpgradeInputs binds a ZKAT-DLOG upgrade issuance to the type it
// redeems: every input is a well-formed ZKAT-DLOG output of the configured
// generation, and TypeCommitment opens to TokenType with the revealed
// TypeRandomness, so the minted notes carry the type the migration proofs were
// verified against. Values are bound by the proofs and the binding signature
// (verifyDLogUpgradeCrypto), not here.
func (v *Validator) validateDLogUpgradeInputs(a *snarktoken.IssueAction) error {
	if err := v.migrationConfigured(); err != nil {
		return err
	}
	if _, err := snarktoken.SummarizeDLogInputs(a.Inputs, v.legacy.PP); err != nil {
		return errors.WithMessagef(errors.Join(ErrMalformedAction, err), "%s", err.Error())
	}

	return v.checkTypeOpening(a)
}

// checkTypeOpening checks that TypeCommitment opens to TokenType with the
// revealed TypeRandomness.
func (v *Validator) checkTypeOpening(a *snarktoken.IssueAction) error {
	if _, err := decodeScalar(v.pp.Curve, a.TypeRandomness); err != nil {
		return errors.Wrapf(err, "validator: TypeRandomness")
	}
	tc, err := snarktoken.ComputeTypeCommitment(a.TokenType, a.TypeRandomness, v.pp.Curve)
	if err != nil {
		return errors.Wrap(err, "validator: recomputing type commitment")
	}
	if !bytes.Equal(tc, a.TypeCommitment) {
		return errors.Wrap(ErrTypeMismatch, "type commitment does not open to the redeemed type")
	}

	return nil
}
