/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// publicWitnessForSpend builds a public-only witness directly from
// already-decoded field elements, the validator never has private
// witness data (Value, Randomness, RCV), only the public bytes off the
// wire, so frontend.PublicOnly() is the only viable construction path
// here, unlike the prover side which builds a full witness and could
// derive the public portion from it.
func publicWitnessForSpend(d decodedSpend, curve ecc.ID) (witness.Witness, error) {
	assignment := &circuit.SpendCircuit{
		CommitmentIn:   d.Commitment,
		ValueCommitInX: d.ValueCommitX,
		ValueCommitInY: d.ValueCommitY,
		TypeCommitment: d.TypeCommitment,
	}

	w, err := frontend.NewWitness(assignment, curve.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return nil, errors.Wrapf(err, "validator: building spend public witness")
	}

	return w, nil
}

// publicWitnessForOutput is the OutputCircuit equivalent. MaxBits is
// deliberately not set here, it is a compile-time-only field
// (frontend.NewWitness never reads non-frontend.Variable struct fields);
// the range constraint's actual bit-width is already fixed inside vkOutput,
// which is what enforces it for this specific proof.
func publicWitnessForOutput(d decodedOutput, curve ecc.ID) (witness.Witness, error) {
	assignment := &circuit.OutputCircuit{
		CommitmentOut:   d.Commitment,
		ValueCommitOutX: d.ValueCommitX,
		ValueCommitOutY: d.ValueCommitY,
		TypeCommitment:  d.TypeCommitment,
	}
	w, err := frontend.NewWitness(assignment, curve.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return nil, errors.Wrapf(err, "validator: building output public witness")
	}

	return w, nil
}

// publicWitnessForMigration builds a public-only witness for
// MigrationCircuit from decoded field elements.
//
// Only the six gnark:",public" fields are populated (CommitmentPedersenX/Y,
// CommitmentMiMC, ValueCommitOutX/Y, TypeCommitment). Every private field
// (Value, TokenTypePed, RandomnessPed, RandomnessNew, RCV) and every
// compile-time-only field (MaxBits, PedG0X/Y, PedG1X/Y, PedG2X/Y,
// GenVX/Y, GenRX/Y) is left at its zero value. frontend.PublicOnly() only
// reads gnark:",public"-tagged frontend.Variable (and emulated.Element)
// fields via reflection — it never dereferences the *big.Int compile-time
// fields, so leaving them nil is safe here.
func publicWitnessForMigration(d decodedMigration, curve ecc.ID) (witness.Witness, error) {
	assignment := &circuit.MigrationCircuit{
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](d.PedersenX),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](d.PedersenY),
		CommitmentMiMC:      d.CommitmentMiMC,
		ValueCommitOutX:     d.ValueCommitX,
		ValueCommitOutY:     d.ValueCommitY,
		TypeCommitment:      d.TypeCommitment,
	}
	w, err := frontend.NewWitness(assignment, curve.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return nil, errors.Wrapf(err, "validator: building migration public witness")
	}

	return w, nil
}

type proofOutcome struct {
	err error
}

// verifyAllProofs verifies all spend and output proofs in parallel using
// the ProofSystemBackend. The backend handles both Groth16 and Plonk
// transparently — the validator never touches proof-system-specific types.
func verifyAllProofs(
	b backend.ProofSystemBackend,
	vkSpendBytes []byte,
	vkOutputBytes []byte,
	curve ecc.ID,
	inputs []snarktoken.SpendDescription,
	decodedInputs []decodedSpend,
	outputs []snarktoken.OutputDescription,
	decodedOutputs []decodedOutput,
) error {
	total := len(inputs) + len(outputs)
	if total == 0 {
		return nil
	}
	ch := make(chan proofOutcome, total)

	for i := range inputs {
		go func() {
			pw, err := publicWitnessForSpend(decodedInputs[i], curve)
			if err != nil {
				ch <- proofOutcome{err}

				return
			}

			if err := b.Verify(vkSpendBytes, inputs[i].SpendProof, pw); err != nil {
				ch <- proofOutcome{errors.Wrapf(ErrInvalidProof, "input %d: %s", i, err)}

				return
			}

			ch <- proofOutcome{}
		}()
	}

	for j := range outputs {
		go func() {
			pw, err := publicWitnessForOutput(decodedOutputs[j], curve)
			if err != nil {
				ch <- proofOutcome{err}

				return
			}
			if err := b.Verify(vkOutputBytes, outputs[j].OutputProof, pw); err != nil {
				ch <- proofOutcome{errors.Wrapf(ErrInvalidProof, "output %d: %s", j, err)}

				return
			}
			ch <- proofOutcome{}
		}()
	}

	for range total {
		if o := <-ch; o.err != nil {
			return o.err
		}
	}

	return nil
}

// verifyMigrationProof verifies a single MigrationAction's proof against
// vkMigration via the ProofSystemBackend.
func verifyMigrationProof(
	b backend.ProofSystemBackend,
	vkMigrationBytes []byte,
	curve ecc.ID,
	proofBytes []byte,
	decoded decodedMigration,
) error {
	pw, err := publicWitnessForMigration(decoded, curve)
	if err != nil {
		return err
	}
	if err := b.Verify(vkMigrationBytes, proofBytes, pw); err != nil {
		return errors.Wrapf(ErrInvalidProof, "%s", err)
	}

	return nil
}
