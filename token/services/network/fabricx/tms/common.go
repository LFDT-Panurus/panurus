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

// ordering models the subset of the ordering service used to broadcast an envelope.
type ordering interface {
	// Broadcast sends the passed blob to the ordering service to be ordered.
	Broadcast(ctx context.Context, blob any) error
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

	return broadcastAndWaitForFinality(context.Background(), fns.Ordering(), ch.Finality(), network, channel, txID, env)
}

// broadcastAndWaitForFinality broadcasts env and waits for txID to become final.
// Polling starts before the broadcast so a transaction that commits before the
// broadcast returns is not missed.
func broadcastAndWaitForFinality(ctx context.Context, orderer ordering, finality driver.Finality, network, channel string, txID driver.TxID, env *cb.Envelope) error {
	finalityCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Buffered, and the context is cancelled on return, so the poller always
	// exits even when the broadcast error path never reads the result.
	final := make(chan error, 1)
	go func() {
		final <- getFinality(finalityCtx, finality, txID, finalityEOFRetries, finalityRetryDuration)
	}()

	logger.Infof("Send transaction [txID=%v] for ordering", txID)
	if err := orderer.Broadcast(ctx, env); err != nil {
		return errors.Wrapf(err, "failed broadcasting on [%s,%s]", network, channel)
	}

	logger.Debugf("Wait for finality")

	return <-final
}

// getFinality polls the finality service until txID is committed, retrying at
// most retries times with delay in between. Only io.EOF is retried: it means
// block 0 is not yet available, typically during network startup.
func getFinality(ctx context.Context, finality driver.Finality, txID string, retries int, delay time.Duration) error {
	logger.Debugf("wait for finality [txID=%v]", txID)
	for attempt := range retries {
		err := finality.IsFinal(ctx, txID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, io.EOF) {
			return errors.Wrapf(err, "finality failed")
		}
		if attempt == retries-1 {
			break
		}
		logger.Warnf("EOF returned. Maybe block 0 is not yet committed. Sleep and retry...")

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()

			return errors.Wrapf(ctx.Err(), "finality wait interrupted")
		case <-timer.C:
		}
	}

	return errors.New("max retries reached")
}
