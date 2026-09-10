/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package integrity provides the integrity checks the storage services apply to
// high-value payloads — token requests, public parameters, identities, and
// endorsement acknowledgements — before persisting them and after reading them
// back.
//
// The checks are deliberately cheap and self-contained: they need only the
// bytes themselves plus the key those bytes are stored under. They are not
// cryptographic verification. A token request is only fully verified by a
// [github.com/LFDT-Panurus/panurus/token.Validator] against a ledger, and an
// endorsement acknowledgement is only fully verified against the payload that
// was signed; both of those require state this package does not have, and both
// already happen at the layer that does — see
// docs/security/store_integrity_verification.md for the full division of
// responsibility.
//
// What these checks give is a fail-closed storage boundary. A payload that
// could not have been produced by a correct caller — empty, not a token
// request, declaring a protocol version this build does not implement, or bound
// to a transaction other than the one it is filed under — is rejected instead
// of being stored, or instead of being handed to a caller that will treat it as
// authentic evidence.
package integrity

import (
	"bytes"
	"encoding/base64"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"google.golang.org/protobuf/proto"
)

// Sentinel errors returned by the checks in this package. Callers that need to
// tell "this payload is structurally impossible" apart from a transport or
// database failure should match against these with errors.Is.
var (
	// ErrEmptyTxID is returned when a payload is filed under, or looked up by,
	// an empty transaction id.
	ErrEmptyTxID = errors.New("empty transaction id")
	// ErrEmptyTokenRequest is returned when a token request payload is empty.
	ErrEmptyTokenRequest = errors.New("empty token request")
	// ErrMalformedTokenRequest is returned when a token request payload cannot
	// be deserialized as the wire format expected at that storage boundary.
	ErrMalformedTokenRequest = errors.New("malformed token request")
	// ErrUnsupportedTokenRequestVersion is returned when a token request
	// declares a protocol version this build does not implement.
	ErrUnsupportedTokenRequestVersion = errors.New("unsupported token request version")
	// ErrAnchorMismatch is returned when a token request is bound to an anchor
	// other than the transaction id it is stored under.
	ErrAnchorMismatch = errors.New("token request anchor does not match transaction id")
	// ErrNoActions is returned when a token request carries no actions, and so
	// could not have been accepted by a validator.
	ErrNoActions = errors.New("token request carries no actions")
	// ErrEmptyPublicParamsHash is returned when the public parameters hash
	// accompanying a token request is empty.
	ErrEmptyPublicParamsHash = errors.New("empty public parameters hash")
	// ErrEmptyEndorser is returned when an endorsement acknowledgement carries
	// no endorser identity.
	ErrEmptyEndorser = errors.New("empty endorser identity")
	// ErrEmptySignature is returned when an endorsement acknowledgement carries
	// no signature.
	ErrEmptySignature = errors.New("empty endorsement signature")
	// ErrPublicParamsHashMismatch is returned when stored public parameters do
	// not hash to the hash they are filed under.
	ErrPublicParamsHashMismatch = errors.New("stored public parameters do not hash to the requested hash")
)

// CheckTokenRequestForStorage is the check applied before a token request is
// persisted. It is constant-time on purpose: at every insert site the payload
// was either just serialized from an in-memory request or just validated in the
// caller's own scope, so re-parsing it would cost time proportional to the
// request size and learn nothing new.
//
// What it does enforce is that the record will be checkable later. A record
// stored under an empty transaction id cannot be bound to a transaction; a
// record stored with empty bytes cannot be replayed or hashed; a record stored
// with an empty public parameters hash silently disables the
// public-parameters-mismatch check performed on the finality and recovery
// paths, turning a check that would have failed into one that never runs.
func CheckTokenRequestForStorage(txID string, raw []byte, ppHash driver.PPHash) error {
	if txID == "" {
		return ErrEmptyTxID
	}
	if len(raw) == 0 {
		return errors.Wrapf(ErrEmptyTokenRequest, "refusing to store token request for [%s]", txID)
	}
	if len(ppHash) == 0 {
		return errors.Wrapf(ErrEmptyPublicParamsHash, "refusing to store token request for [%s]", txID)
	}

	return nil
}

// CheckStoredTokenRequest verifies that raw, as read back from storage under
// txID, is a well-formed serialized [github.com/LFDT-Panurus/panurus/token.Request]
// — the format produced by Request.Bytes and stored by the ttxdb and auditdb
// services — and that it is anchored to txID.
//
// The anchor check is the substantive one. The anchor is the transaction id the
// request commits to and is covered by the signatures inside the request, so it
// is the one field that ties the bytes to the row they were found in. Callers
// treat a retrieved request as authentic evidence about txID: they hash it and
// compare against the ledger, re-broadcast it, or hand it to an auditor. If two
// rows were swapped, or a row was rewritten out of band, the request's
// signatures authorize a different transaction than the caller is about to
// attribute them to, and no downstream check catches it — the finality-path
// hash comparison would report a hash mismatch for whichever transaction the
// bytes actually belong to.
//
// The cost is one protobuf unmarshal per retrieved request, on paths that then
// do considerably more work with the result.
func CheckStoredTokenRequest(txID string, raw []byte) error {
	if txID == "" {
		return ErrEmptyTxID
	}
	if len(raw) == 0 {
		return errors.Wrapf(ErrEmptyTokenRequest, "token request for [%s]", txID)
	}

	requestWithMetadata := &request.TokenRequestWithMetadata{}
	if err := proto.Unmarshal(raw, requestWithMetadata); err != nil {
		return errors.Wrapf(ErrMalformedTokenRequest, "failed unmarshalling token request for [%s]: %v", txID, err)
	}
	if requestWithMetadata.Version != driver.ProtocolV1 {
		return errors.Wrapf(ErrUnsupportedTokenRequestVersion, "token request for [%s] declares version [%d], expected [%d]", txID, requestWithMetadata.Version, driver.ProtocolV1)
	}
	if requestWithMetadata.Anchor != txID {
		return errors.Wrapf(ErrAnchorMismatch, "token request stored under [%s] is anchored to [%s]", txID, requestWithMetadata.Anchor)
	}

	return nil
}

// CheckTokenRequestActions verifies that raw is a well-formed serialized
// [github.com/LFDT-Panurus/panurus/token/driver.TokenRequest] — the bare
// actions-and-signatures format, without anchor or metadata, that the endorser
// store holds — and that it carries at least one action.
//
// The two conditions are exactly the ones a validator rejects on before looking
// at any action: an unsupported protocol version, and an empty action list.
// This is not a substitute for validation. It is applied where a raw payload
// arrives from the network and is about to be persisted as the validated
// request for a transaction, so that a payload reaching that store on a path
// that skipped validation is rejected rather than filed as validated.
//
// Unlike CheckStoredTokenRequest there is no anchor to compare: this format
// does not carry one, so records in this format cannot be bound to their
// transaction id by a structural check alone.
func CheckTokenRequestActions(raw []byte) error {
	if len(raw) == 0 {
		return ErrEmptyTokenRequest
	}

	tokenRequest := &driver.TokenRequest{}
	if err := tokenRequest.FromBytes(raw); err != nil {
		if errors.Is(err, driver.ErrUnsupportedVersion) {
			return errors.Wrapf(ErrUnsupportedTokenRequestVersion, "%v", err)
		}

		return errors.Wrapf(ErrMalformedTokenRequest, "failed unmarshalling token request actions: %v", err)
	}
	if len(tokenRequest.Actions) == 0 {
		return ErrNoActions
	}

	return nil
}

// CheckEndorsementAck verifies that an endorsement acknowledgement carries both
// an endorser identity and a signature.
//
// An acknowledgement row is evidence that a party signed off on a transaction.
// Consumers of [github.com/LFDT-Panurus/panurus/token/services/ttx.TransactionInfo]
// read acknowledgements as a map keyed by endorser and do not inspect the
// values, so a row with an empty signature is indistinguishable from a genuine
// one and turns "this party never signed" into "this party signed". An empty
// endorser identity collapses to a single map key and would mask, or be masked
// by, an unrelated party's acknowledgement.
//
// This does not verify the signature. Verifying it requires the payload that
// was signed, which is not persisted alongside the acknowledgement; the
// verification therefore happens where that payload is still in scope, before
// the acknowledgement reaches storage.
func CheckEndorsementAck(endorser []byte, sigma []byte) error {
	if len(endorser) == 0 {
		return ErrEmptyEndorser
	}
	if len(sigma) == 0 {
		return ErrEmptySignature
	}

	return nil
}

// CheckPublicParamsHash verifies that raw, read back from storage under
// rawHash, actually hashes to rawHash.
//
// Public parameters are the highest-value payload the token store holds: they
// carry the issuer and auditor public keys and the cryptographic setup every
// action is validated against. They are addressed by hash precisely because the
// hash is what a caller has already established out of band — a transaction
// records the hash of the parameters it was created under, and the finality and
// recovery paths fetch parameters by that hash in order to re-validate. Recomputing
// the hash is what makes that addressing mean anything: without it, a row whose
// raw and raw_hash columns disagree hands back parameters the caller did not ask
// for, and every subsequent check runs against the wrong setup while appearing to
// run against the right one.
//
// An empty rawHash is refused, because it would make the comparison vacuous. An
// empty raw is not an error: the store's convention is that an absent record
// reads back as nil bytes with no error, and distinguishing "absent" from
// "corrupt" is the caller's job.
//
// The cost is one SHA-256 over the parameters, on paths that then deserialize
// and use them.
func CheckPublicParamsHash(rawHash driver.PPHash, raw []byte) error {
	if len(rawHash) == 0 {
		return ErrEmptyPublicParamsHash
	}
	if len(raw) == 0 {
		return nil
	}
	if computed := utils.Hashable(raw).Raw(); !bytes.Equal(computed, rawHash) {
		return errors.Wrapf(
			ErrPublicParamsHashMismatch,
			"public parameters stored under hash [%s] hash to [%s]",
			base64.StdEncoding.EncodeToString(rawHash),
			base64.StdEncoding.EncodeToString(computed),
		)
	}

	return nil
}
