/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

// logger is the package logger, used for the initiator's per-endorser diagnostics.
var logger = logging.MustGetLogger()

// Sentinel errors for the endorsement flow, classifiable with errors.Is (design §13). They are all
// permanent: none is worth retrying, because each means the request itself is unacceptable, not that
// the network was briefly unavailable.
var (
	// ErrUnauthorized is returned by a responder when the requesting FSC identity is not in the
	// endorsement allowlist.
	ErrUnauthorized = errors.New("requester not authorized to request endorsement")

	// ErrValidation is returned when the token request fails validation against the ledger and public
	// parameters.
	ErrValidation = errors.New("token request validation failed")

	// ErrInsufficientEndorsements is returned by the initiator when fewer than the threshold of
	// distinct authorized endorsers signed.
	ErrInsufficientEndorsements = errors.New("insufficient endorsements")

	// ErrUnknownSigner is returned when a signature recovers to an address that is not a registered
	// endorser.
	ErrUnknownSigner = errors.New("signature recovered to an unknown signer")

	// ErrDuplicateSigner is returned when two collected signatures recover to the same endorser; the
	// contract counts distinct signers only, so the initiator must not assemble a quorum that would be
	// rejected on-chain.
	ErrDuplicateSigner = errors.New("duplicate endorser signature")
)
