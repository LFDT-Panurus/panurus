/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/frontend"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
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

type proofOutcome struct {
	err error
}

// verifyAllProofs verifies all spend and output proofs in parallel using
// the already-prepared backend and verifying keys. The backend handles
// both Groth16 and Plonk transparently — the validator never touches
// proof-system-specific types. Every goroutine shares the same
// already-deserialized verifying keys: gnark's Verify does not mutate a
// verifying key, so this is safe without synchronization.
func verifyAllProofs(
	v *prepared.Verifier,
	inputs []snarktoken.SpendDescription,
	decodedInputs []decodedSpend,
	outputs []snarktoken.OutputDescription,
	decodedOutputs []decodedOutput,
) error {
	total := len(inputs) + len(outputs)
	if total == 0 {
		return nil
	}
	curve := v.PP().Curve
	ch := make(chan proofOutcome, total)

	for i := range inputs {
		go func() {
			pw, err := publicWitnessForSpend(decodedInputs[i], curve)
			if err != nil {
				ch <- proofOutcome{err}

				return
			}

			if err := v.Backend().Verify(v.VKSpend(), inputs[i].SpendProof, pw); err != nil {
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
			if err := v.Backend().Verify(v.VKOutput(), outputs[j].OutputProof, pw); err != nil {
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
