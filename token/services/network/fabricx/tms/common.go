/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"context"
	"io"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/msp"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	cb "github.com/hyperledger/fabric-protos-go-apiv2/common"
)

// Signer models a message signer.
type Signer interface {
	// Sign signs the given message.
	Sign(msg []byte) ([]byte, error)
	// Serialize returns the serialized version of the signer.
	Serialize() ([]byte, error)
}

// SigningIdentityProvider models an identity provider for signing identities.
type SigningIdentityProvider interface {
	// DefaultSigningIdentity returns the default signing identity for the given network and channel.
	DefaultSigningIdentity(network, channel string) (Signer, error)
	// DefaultIdentity returns the default identity for the given network and channel.
	DefaultIdentity(network, channel string) (view.Identity, error)
}

// EnvelopeBroadcaster models an envelope broadcaster.
type EnvelopeBroadcaster interface {
	// Broadcast broadcasts the given envelope for the given network, channel, and transaction ID.
	Broadcast(network, channel string, txID driver.TxID, env *cb.Envelope) error
}

type fnsSigningIdentityProvider struct {
	fnsProvider *fabric.NetworkServiceProvider
}

type signerWrapper struct {
	signerWithPublicVersion
}

func (w *signerWrapper) Serialize() ([]byte, error) {
	return w.GetPublicVersion().Serialize()
}

type signerWithPublicVersion interface {
	Signer
	GetPublicVersion() msp.Identity
}

// DefaultSigningIdentity returns the default signing identity for the specified
// network and channel. It retrieves the identity from the membership service
// of the Fabric network service.
func (p *fnsSigningIdentityProvider) DefaultSigningIdentity(network, channel string) (Signer, error) {
	fns, err := p.fnsProvider.FabricNetworkService(network)
	if err != nil {
		return nil, errors.Wrapf(err, "fns for [%s] not found", network)
	}

	return &signerWrapper{
		signerWithPublicVersion: fns.LocalMembership().DefaultSigningIdentity().(signerWithPublicVersion),
	}, nil
}

// DefaultIdentity returns the default serialized identity for the specified
// network and channel.
func (p *fnsSigningIdentityProvider) DefaultIdentity(network, channel string) (view.Identity, error) {
	fns, err := p.fnsProvider.FabricNetworkService(network)
	if err != nil {
		return nil, errors.Wrapf(err, "fns for [%s] not found", network)
	}

	return fns.LocalMembership().DefaultIdentity(), nil
}

type fnsBroadcaster struct {
	fnsProvider *fabric.NetworkServiceProvider
}

// Broadcast sends the transaction envelope to the ordering service and blocks
// until finality is confirmed. It starts a background goroutine to poll
// for finality while performing the broadcast operation.
func (p *fnsBroadcaster) Broadcast(network, channel string, txID driver.TxID, env *cb.Envelope) error {
	fns, err := p.fnsProvider.FabricNetworkService(network)
	if err != nil {
		return errors.Wrapf(err, "fns for [%s] not found", network)
	}

	ch, err := fns.Channel(channel)
	if err != nil {
		return errors.Wrapf(err, "failed to get channel [%s]", channel)
	}

	final := make(chan error)
	go func() { final <- getFinality(ch.Finality(), txID) }()

	logger.Infof("Send transaction [txID=%v] for ordering", txID)
	if err := fns.Ordering().Broadcast(context.Background(), env); err != nil {
		return errors.Wrapf(err, "failed broadcasting on [%s,%s]", network, channel)
	}

	logger.Debugf("Wait for finality")

	return <-final
}

// getFinality polls the finality service to determine if a transaction has
// been committed to the ledger. It includes retry logic with a delay to
// handle EOF errors, which can occur during network startup or when
// block 0 is not yet available.
func getFinality(finality driver.Finality, txID string) error {
	logger.Debugf("wait for finality [txID=%v]", txID)
	for range finalityEOFRetries {
		err := finality.IsFinal(context.Background(), txID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, io.EOF) {
			return errors.Wrapf(err, "finality failed")
		}
		logger.Warnf("EOF returned. Maybe block 0 is not yet committed. Sleep and retry...")
		time.Sleep(finalityRetryDuration)
	}

	return errors.New("max retries reached")
}
