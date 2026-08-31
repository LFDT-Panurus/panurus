/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// EndorsementVerifier read methods, plus the TokenState accessor that names it. The signatures are
// canonical ABI forms; their selectors are what the contracts dispatch on.
const (
	endorsementVerifierMethod = "endorsementVerifier()"
	getThresholdMethod        = "getThreshold()"
	isEndorserMethod          = "isEndorser(address)"
)

// verifyEndorsementPolicy checks that the endorsement policy this node is configured with is the one
// the chain will actually enforce.
//
// The two are meant to agree, and the configuration says so in its own field comments: the threshold
// "must match the threshold the EndorsementVerifier was constructed with", and an endorser address is
// documented as one "registered in the EndorsementVerifier". Nothing checked it. Drift is easy to
// introduce (a redeploy, a copied config, an endorser added to one side only) and expensive to
// diagnose, because it does not look like a configuration problem from the outside: the initiator
// collects what it believes is a quorum, spends a full endorsement round trip doing it, and the
// transaction is then rejected by the contract. With the estimate gas strategy that surfaces as
// ErrTransactionReverted, which reads as the chain refusing the delta rather than as this node
// asking for the wrong number of signatures.
//
// This is the same startup guard Connect already applies to the chain id, for the same reason.
//
// Only a positively observed contradiction is fatal. A read that fails says nothing about the policy
// (the contract may not be deployed yet, or the node may be briefly unhappy) and must not take a node
// down over it, so it is logged and the connection proceeds; the pre-existing behaviour, no check at
// all, is what a failed read falls back to.
//
// The reads are at the latest block rather than the configured tag on purpose. The endorser set and
// threshold are immutable after the verifier's construction, so there is no value here that a later
// block can change and no reorg exposure to speak of; reading at "finalized" instead would leave a
// freshly deployed network unable to see its own verifier for the whole finalization window, turning
// the common case into the one that gets no checking.
func (n *Network) verifyEndorsementPolicy(ctx context.Context, nsBinding *namespaceBinding) error {
	verifier, ok := n.endorsementVerifierAddress(ctx, nsBinding)
	if !ok {
		return nil
	}

	if configured := nsBinding.config.Contracts.EndorsementVerifier; configured != "" {
		want, err := client.HexToAddress(configured)
		if err != nil {
			return errors.Wrap(err, "evm network: invalid endorsementVerifier address")
		}
		if want != verifier {
			return errors.Errorf(
				"evm network: tokenState [%s] calls verifier %s, configuration says %s",
				nsBinding.tokenState, verifier, want)
		}
	}

	if err := n.verifyThreshold(ctx, nsBinding, verifier); err != nil {
		return err
	}

	return n.verifyEndorserSet(ctx, nsBinding, verifier)
}

// endorsementVerifierAddress reads the verifier the TokenState delegates signature checking to. The
// TokenState holds the authoritative reference, which is why the configuration may leave it out
// entirely, so it is read from there rather than taken from configuration. It reports false when the
// answer could not be obtained, which is not an error: see verifyEndorsementPolicy.
func (n *Network) endorsementVerifierAddress(ctx context.Context, nsBinding *namespaceBinding) (client.Address, bool) {
	tokenState := nsBinding.tokenState
	raw, err := n.client.Call(ctx, tokenState, abi.MethodID(endorsementVerifierMethod), client.BlockTagLatest)
	if err != nil {
		logger.Warnf("could not read the endorsement verifier of [%s]; skipping the policy check: %v", tokenState, err)

		return client.Address{}, false
	}
	word, err := abi.DecodeBytes32(raw)
	if err != nil {
		logger.Warnf("could not decode the endorsement verifier of [%s]; skipping the policy check: %v", tokenState, err)

		return client.Address{}, false
	}
	verifier := client.BytesToAddress(word[12:])
	if verifier == (client.Address{}) {
		logger.Warnf("tokenState [%s] names no endorsement verifier yet; skipping the policy check", tokenState)

		return client.Address{}, false
	}

	return verifier, true
}

// verifyThreshold checks the configured quorum size is the one the contract enforces.
func (n *Network) verifyThreshold(ctx context.Context, nsBinding *namespaceBinding, verifier client.Address) error {
	raw, err := n.client.Call(ctx, verifier, abi.MethodID(getThresholdMethod), client.BlockTagLatest)
	if err != nil {
		logger.Warnf("could not read the on-chain endorsement threshold from %s: %v", verifier, err)

		return nil
	}
	onChain, err := abi.DecodeUint64(raw)
	if err != nil {
		logger.Warnf("could not decode the on-chain endorsement threshold from %s: %v", verifier, err)

		return nil
	}
	if onChain != uint64(nsBinding.config.Endorsement.Threshold) {
		return errors.Errorf(
			"evm network: verifier %s requires %d endorsements, configuration says %d; "+
				"every transaction would collect a quorum this contract rejects",
			verifier, onChain, nsBinding.config.Endorsement.Threshold)
	}

	return nil
}

// verifyEndorserSet checks every configured endorser is one the contract will actually accept a
// signature from. A configured endorser the contract does not know is worse than useless: the
// initiator routes requests to it and counts its reply toward the quorum, and the contract then
// rejects the whole bundle as an unauthorized signer, so one stale entry can fail every transaction
// even when enough genuine endorsers signed.
//
// It checks membership per address rather than reading the whole set back, because that needs only a
// static-word decode of the codec this driver keeps deliberately minimal. The opposite direction, an
// endorser on chain that this node does not know about, needs no check: the threshold comparison
// above already catches the case where it matters.
func (n *Network) verifyEndorserSet(ctx context.Context, nsBinding *namespaceBinding, verifier client.Address) error {
	for _, endorser := range nsBinding.config.Endorsement.Endorsers {
		address, err := client.HexToAddress(endorser.Address)
		if err != nil {
			// Validate already rejected this at load time; reaching it here means the configuration was
			// not loaded through LoadConfig, which is not this check's business to police.
			return errors.Wrapf(err, "evm network: endorser [%s] has an invalid address", endorser.FSCIdentity)
		}

		var arg [32]byte
		copy(arg[12:], address[:])
		raw, err := n.client.Call(ctx, verifier, abi.EncodeBytes32Call(isEndorserMethod, arg), client.BlockTagLatest)
		if err != nil {
			logger.Warnf("could not check endorser %s against verifier %s: %v", address, verifier, err)

			return nil
		}
		registered, err := abi.DecodeUint64(raw)
		if err != nil {
			logger.Warnf("could not decode the endorser check for %s from %s: %v", address, verifier, err)

			return nil
		}
		if registered == 0 {
			return errors.Errorf(
				"evm network: endorser [%s] at %s is not registered in verifier %s; "+
					"the contract rejects any bundle carrying its signature",
				endorser.FSCIdentity, address, verifier)
		}
	}

	return nil
}
