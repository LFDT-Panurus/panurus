/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

var (
	// ErrOwnerTokenMismatch is returned when the number of owners does not match the number of tokens
	ErrOwnerTokenMismatch = errors.New("number of owners does not match number of tokens")
	// ErrNilOutput is returned when there is a nil output in an issue action
	ErrNilOutput = errors.New("nil output in issue action")
	// ErrInvalidProtocolVersion is returned when the protocol version is invalid
	ErrInvalidProtocolVersion = errors.New("invalid protocol version")
	// ErrIssuerNotSet is returned when the issuer is not set
	ErrIssuerNotSet = errors.New("issuer is not set")
	// ErrNilInput is returned when there is a nil input in an issue action
	ErrNilInput = errors.New("nil input in issue action")
	// ErrNilInputToken is returned when there is a nil input token in an issue action
	ErrNilInputToken = errors.New("nil input token in issue action")
	// ErrNilInputID is returned when there is a nil input id in an issue action
	ErrNilInputID = errors.New("nil input id in issue action")
	// ErrNoOutputs is returned when there are no outputs in an issue action
	ErrNoOutputs = errors.New("no outputs in issue action")
	// ErrNilPublicParameters is returned when public parameters are nil
	ErrNilPublicParameters = errors.New("failed to generate ZK Issue: nil public parameters")
	// ErrInvalidPublicParameters is returned when public parameters are invalid
	ErrInvalidPublicParameters = errors.New("failed to generate ZK Issue: please initialize public parameters with an admissible curve")
	// ErrNilSigner is returned when the signer is nil
	ErrNilSigner = errors.New("failed to generate ZK Issue: please initialize signer")
	// ErrInvalidTokenWitness is returned when the token witness is invalid
	ErrInvalidTokenWitness = errors.New("invalid token witness")
	// ErrInvalidTokenWitnessValues is returned when the token witness values are invalid
	ErrInvalidTokenWitnessValues = errors.New("invalid token witness values")
	// ErrGetRNGFailed is returned when the RNG cannot be obtained
	ErrGetRNGFailed = errors.New("failed to get RNG")
	// ErrGetIssueProverFailed is returned when the issue prover cannot be obtained
	ErrGetIssueProverFailed = errors.New("cannot get issue prover")
	// ErrInvalidSameTypeProof is returned when the same type proof is invalid
	ErrInvalidSameTypeProof = errors.New("invalid same type proof")
	// ErrInvalidIssueProof is returned when the issue proof is invalid
	ErrInvalidIssueProof = errors.New("invalid issue proof")
	// ErrProveTypeFailed is returned when the type proof fails
	ErrProveTypeFailed = errors.New("couldn't prove type during the issue")
	// ErrVerifySameTypeProofFailed is returned when the same type proof verification fails
	ErrVerifySameTypeProofFailed = errors.New("failed to verify same type proof")
	// ErrGenerateZKProof is returned when the ZK proof generation fails
	ErrGenerateZKProof = errors.New("failed to generate zero knwoledge proof for issue")
	// ErrGenerateIssueProofFailed is returned when the issue proof generation fails
	ErrGenerateIssueProofFailed = errors.New("failed to generate issue proof")
	// ErrGenerateRangeProofFailed is returned when the range proof generation fails
	ErrGenerateRangeProofFailed = errors.New("failed to generate range proof for issue")
	// ErrSignTokenActionsNilSigner is returned when the signer is nil while signing token actions
	ErrSignTokenActionsNilSigner = errors.New("failed to sign Token Actions: please initialize signer")
	// ErrSerializeInputsFailed is returned when inputs serialization fails
	ErrSerializeInputsFailed = errors.New("failed to serialize inputs")
	// ErrSerializeOutputFailed is returned when output serialization fails
	ErrSerializeOutputFailed = errors.New("failed to serialize output")
	// ErrSerializeOutputsFailed is returned when outputs serialization fails
	ErrSerializeOutputsFailed = errors.New("failed to serialize outputs")
	// ErrDeserializeIssueActionFailed is returned when issue action deserialization fails
	ErrDeserializeIssueActionFailed = errors.New("failed to deserialize issue action")
	// ErrUnmarshalReceiversMetadataFailed is returned when receivers metadata unmarshalling fails
	ErrUnmarshalReceiversMetadataFailed = errors.New("failed unmarshalling receivers metadata")
	// ErrDeserializeOutputFailed is returned when output deserialization fails
	ErrDeserializeOutputFailed = errors.New("failed to deserialize output")
	// ErrUnmarshalSameTypeFailed is returned when same type proof unmarshalling fails
	ErrUnmarshalSameTypeFailed = errors.New("failed to initialize unmarshaller")
	// ErrDeserializeProofFailed is returned when proof deserialization fails
	ErrDeserializeProofFailed = errors.New("failed to deserialize proof")
	// ErrGetTokensFailed is returned when token generation fails
	ErrGetTokensFailed = errors.New("failed to generate tokens")
	// ErrSerializeSignerFailed is returned when signer serialization fails
	ErrSerializeSignerFailed = errors.New("failed to serialize signer")
	// ErrCreateActionFailed is returned when action creation fails
	ErrCreateActionFailed = errors.New("failed to create issue action")
	// ErrDeserializeTypeFailed is returned when type deserialization fails
	ErrDeserializeTypeFailed = errors.New("failed to deserialize type")
	// ErrDeserializeBlindingFactorFailed is returned when blinding factor deserialization fails
	ErrDeserializeBlindingFactorFailed = errors.New("failed to deserialize blinding factor")
	// ErrDeserializeChallengeFailed is returned when challenge deserialization fails
	ErrDeserializeChallengeFailed = errors.New("failed to deserialize challenge")
	// ErrDeserializeCommitmentToTypeFailed is returned when commitment to type deserialization fails
	ErrDeserializeCommitmentToTypeFailed = errors.New("failed to deserialize commitment to type")
	// ErrNilProof is returned when the proof is nil
	ErrNilProof = errors.New("nil proof")
	// ErrEmptyProof is returned when the proof is empty
	ErrEmptyProof       = errors.New("proof cannot be empty")
	ErrInvalidProofType = errors.New("invalid proof type")
	// ErrProofTypeMismatch is returned when the proof type in the action refers to a range-proof
	// algorithm whose params sub-struct is not populated in PublicParams. This prevents an attacker
	// from selecting a verifier whose params sub-struct is nil (nil-deref / algorithm confusion).
	ErrProofTypeMismatch = errors.New("proof type in action is not available in public parameters")
)
