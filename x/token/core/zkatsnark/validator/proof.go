/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

// publicWitnessForSpend builds a public-only witness directly from
// already-decoded field elements, the validator never has private
// witness data (Value, Randomness, RCV), only the public bytes off the
// wire, so frontend.PublicOnly() is the only viable construction path
// here, unlike the prover side which builds a full witness and could
// derive the public portion from it.
func publicWitnessForSpend(d decodedSpend) (witness.Witness, error) {
	assignment := &circuit.SpendCircuit{
		CommitmentIn:   d.Commitment,
		ValueCommitInX: d.ValueCommitX,
		ValueCommitInY: d.ValueCommitY,
		TokenType:      d.TokenType,
	}

	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
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
func publicWitnessForOutput(d decodedOutput) (witness.Witness, error) {
	assignment := &circuit.OutputCircuit{
		CommitmentOut:   d.Commitment,
		ValueCommitOutX: d.ValueCommitX,
		ValueCommitOutY: d.ValueCommitY,
		TokenType:       d.TokenType,
	}
	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return nil, errors.Wrapf(err, "validator: building output public witness")
	}
	return w, nil
}

// publicWitnessForMigration builds a public-only witness for
// MigrationCircuit from decoded field elements.
//
// Only the six gnark:",public" fields are populated (CommitmentPedersenX/Y,
// CommitmentMiMC, ValueCommitOutX/Y, TokenType). Every private field
// (Value, TokenTypePed, RandomnessPed, RandomnessNew, RCV) and every
// compile-time-only field (MaxBits, PedG0X/Y, PedG1X/Y, PedG2X/Y) is left
// at its zero value. frontend.PublicOnly() only reads gnark:",public"
// -tagged frontend.Variable (and emulated.Element) fields via reflection —
// it never dereferences the *big.Int compile-time fields, so leaving them
// nil is safe here, exactly as OutputCircuit's MaxBits is safely left
// unset in publicWitnessForOutput above. Worth double-checking this
// specific circuit given it has more compile-time-only fields (seven) than
// any circuit validated so far.
func publicWitnessForMigration(d decodedMigration) (witness.Witness, error) {
	assignment := &circuit.MigrationCircuit{
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](d.PedersenX.BigInt(new(big.Int))),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](d.PedersenY.BigInt(new(big.Int))),
		CommitmentMiMC:      d.CommitmentMiMC,
		ValueCommitOutX:     d.ValueCommitX,
		ValueCommitOutY:     d.ValueCommitY,
		TokenType:           d.TokenType,
	}
	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return nil, errors.Wrapf(err, "validator: building migration public witness")
	}
	return w, nil
}

type proofOutcome struct {
	err error
}

func verifyAllProofs(
	vkSpend groth16.VerifyingKey,
	vkOutput groth16.VerifyingKey,
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
		i := i
		go func() {
			proof, err := setup.DeserializeProof(inputs[i].SpendProof, curve)
			if err != nil {
				ch <- proofOutcome{errors.Wrapf(err, "input %d: proof decode", i)}
				return
			}

			pw, err := publicWitnessForSpend(decodedInputs[i])
			if err != nil {
				ch <- proofOutcome{err}
				return
			}

			if err := groth16.Verify(proof, vkSpend, pw); err != nil {
				ch <- proofOutcome{errors.Wrapf(ErrInvalidProof, "input %d: %s", i, err)}
				return
			}

			ch <- proofOutcome{}
		}()
	}

	for j := range outputs {
		j := j
		go func() {
			proof, err := setup.DeserializeProof(outputs[j].OutputProof, curve)
			if err != nil {
				ch <- proofOutcome{errors.Wrapf(err, "output %d: proof decode", j)}
				return
			}
			pw, err := publicWitnessForOutput(decodedOutputs[j])
			if err != nil {
				ch <- proofOutcome{err}
				return
			}
			if err := groth16.Verify(proof, vkOutput, pw); err != nil {
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
// vkMigration. A MigrationAction always carries exactly one proof — never
// a batch the way TransferAction carries many SpendProofs/OutputProofs —
// so no internal parallelism is needed here, unlike verifyAllProofs. A
// caller validating many MigrationActions together is responsible for
// parallelizing ACROSS calls to ValidateMigration, at a layer above this
// package.
func verifyMigrationProof(
	vkMigration groth16.VerifyingKey,
	curve ecc.ID,
	proofBytes []byte,
	decoded decodedMigration,
) error {
	proof, err := setup.DeserializeProof(proofBytes, curve)
	if err != nil {
		return errors.Wrapf(err, "validator: migration proof decode")
	}
	pw, err := publicWitnessForMigration(decoded)
	if err != nil {
		return err
	}
	if err := groth16.Verify(proof, vkMigration, pw); err != nil {
		return errors.Wrapf(ErrInvalidProof, "%s", err)
	}
	return nil
}
