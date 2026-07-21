/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"github.com/LFDT-Panurus/panurus/token/services/network/common/replay"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	// ErrInvalidTransient signals that the transient values are invalid
	ErrInvalidTransient = errors.New("invalid transient")
	// ErrInvalidProposal signals that the proposal is invalid
	ErrInvalidProposal = errors.New("invalid proposal")
	// ErrReceivedProposal signals that an error occurred when receiving the proposal
	ErrReceivedProposal = errors.New("failed to received proposal")
	// ErrValidateProposal signals a failure in validating the proposal
	ErrValidateProposal = errors.New("failed to validate proposal")
	// ErrEndorseProposal signals a failure in endorsing the proposal
	ErrEndorseProposal = errors.New("failed to endorse proposal")
	// ErrValidatePublicParams signals that the submitted public parameters failed
	// authorization or consistency checks
	ErrValidatePublicParams = errors.New("invalid public parameters")
	// ErrAlreadyProcessed signals that an equivalent request has already been processed
	ErrAlreadyProcessed = replay.ErrAlreadyProcessed
	// ErrOutOfWindow signals that a request's claimed timestamp falls outside the replay
	// guard's freshness window
	ErrOutOfWindow = replay.ErrOutOfWindow
)
