/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// OperationType represents the HTLC operation being performed:
// Claim or Reclaim (or None when undetermined).
type OperationType int

const (
	None OperationType = iota
	Claim
	Reclaim
)

// Action abstracts an action that potentially carries metadata used by the HTLC validators.
// Tests can provide a fake implementation with the desired
// metadata map to drive validation scenarios.
//
//go:generate counterfeiter -o mock/action.go -fake-name Action . Action
type Action interface {
	GetMetadata() map[string][]byte
}

// VerifyOwner validates that the provided output owner corresponds to the
// expected owner according to the HTLC script encoded in senderRawOwner.
// It returns the parsed script and the inferred operation type (Claim or Reclaim).
// The provided 'now' timestamp is used to determine whether the
// HTLC is still claimable (now before deadline) or reclaimable (now after or equal to deadline).
func VerifyOwner(senderRawOwner []byte, outRawOwner []byte, now time.Time) (*htlc.Script, OperationType, error) {
	if len(outRawOwner) == 0 {
		return nil, None, errors.Errorf("the output owner must be set")
	}
	sender, err := identity.UnmarshalTypedIdentity(senderRawOwner)
	if err != nil {
		return nil, None, err
	}
	if sender.Type != ScriptType {
		return nil, None, errors.Errorf("invalid identity type, expected [%s], got [%s]", ScriptType, sender.Type)
	}
	script := &htlc.Script{}
	err = json.Unmarshal(sender.Identity, script)
	if err != nil {
		return nil, None, err
	}

	if now.Before(script.Deadline) {
		// this should be a claim
		if !script.Recipient.Equal(outRawOwner) {
			return nil, None, errors.New("owner of output token does not correspond to recipient in htlc request")
		}

		return script, Claim, nil
	} else {
		// this should be a reclaim
		if !script.Sender.Equal(outRawOwner) {
			return nil, None, errors.New("owner of output token does not correspond to sender in htlc request")
		}

		return script, Reclaim, nil
	}
}

// MetadataClaimKeyCheck validates that the Claim metadata is present and consistent.
// For Claim operations it expects the action metadata to contain
// a key derived from the hash image and the value equal to the preimage.
// For Reclaim operations it returns no error and an empty key.
func MetadataClaimKeyCheck(action Action, script *htlc.Script, op OperationType, sig []byte) (string, error) {
	if op == Reclaim {
		// No metadata in this case
		return "", nil
	}

	// Unmarshal signature to ClaimSignature
	claim := &htlc.ClaimSignature{}
	if err := json.Unmarshal(sig, claim); err != nil {
		return "", errors.Wrapf(err, "failed unmarshalling claim signature [%s]", string(sig))
	}
	// Check that it is well-formed
	if len(claim.Preimage) == 0 || len(claim.RecipientSignature) == 0 {
		return "", errors.New("expected a valid claim preImage and recipient signature")
	}

	// Check that the pre-image is in the action's metadata
	metadata := action.GetMetadata()
	if len(metadata) == 0 {
		return "", errors.New("cannot find htlc pre-image, no metadata")
	}
	image, err := script.HashInfo.Image(claim.Preimage)
	if err != nil {
		return "", errors.Wrapf(err, "failed to compute image of [%x]", claim.Preimage)
	}
	key := htlc.ClaimKey(image)
	value, ok := metadata[key]
	if !ok {
		return "", errors.New("cannot find htlc pre-image, missing metadata entry")
	}
	if !bytes.Equal(value, claim.Preimage) {
		return "", errors.Errorf("invalid action, cannot match htlc pre-image with metadata [%x]!=[%x]", value, claim.Preimage)
	}

	return key, nil
}

// MetadataLockKeyCheck validates that the lock metadata entry is present and correct.
// It checks that the metadata contains the lock key derived from
// the script hash and that the associated value matches the expected lock value.
func MetadataLockKeyCheck(action Action, script *htlc.Script) (string, error) {
	metadata := action.GetMetadata()
	if len(metadata) == 0 {
		return "", errors.New("cannot find htlc lock, no metadata")
	}
	key := htlc.LockKey(script.HashInfo.Hash)
	value, ok := metadata[key]
	if !ok {
		return "", errors.New("cannot find htlc lock, missing metadata entry")
	}
	if !bytes.Equal(value, htlc.LockValue(script.HashInfo.Hash)) {
		return "", errors.Errorf("invalid action, cannot match htlc lock with metadata [%x]!=[%x]", value, script.HashInfo.Hash)
	}

	return key, nil
}
