/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const (
	ConfigurationKey = "certification.interactive"
)

// Certification holds the configuration for the interactive certifier, parsed
// from the TMS configuration under the "certification.interactive" key.
type Certification struct {
	// IDs lists the endpoint identifiers of the remote certifier nodes.
	IDs []string `yaml:"ids,omitempty"`

	// MaxAttempts is the maximum number of times a certification request is
	// retried before the batch is discarded. Zero uses DefaultMaxAttempts.
	MaxAttempts int `yaml:"maxAttempts,omitempty"`

	// WaitTime is the backoff duration between retry attempts.
	// Zero uses DefaultWaitTime.
	WaitTime time.Duration `yaml:"waitTime,omitempty"`

	// BatchSize is the maximum number of tokens assembled into a single
	// certification request. Zero uses DefaultBatchSize.
	BatchSize int `yaml:"batchSize,omitempty"`

	// BufferSize is the capacity of the incoming token channel. Tokens are
	// dropped (and counted) when the buffer is full. Zero uses DefaultBufferSize.
	BufferSize int `yaml:"bufferSize,omitempty"`

	// FlushInterval is the maximum time a partial batch waits before being
	// dispatched to the worker pool. Zero uses DefaultFlushInterval.
	FlushInterval time.Duration `yaml:"flushInterval,omitempty"`

	// Workers is the number of concurrent goroutines that process certification
	// batches. Zero uses DefaultWorkers.
	Workers int `yaml:"workers,omitempty"`

	// ResponseTimeout is the maximum time the client waits for the certifier to
	// respond before treating the request as failed. Zero uses DefaultResponseTimeout.
	ResponseTimeout time.Duration `yaml:"responseTimeout,omitempty"`
}

type BackendFactory func(tms *token.ManagementService, wallet string) (Backend, error)

type Resolver interface {
	ResolveIdentities(endpoints ...string) ([]view.Identity, error)
}

//go:generate counterfeiter -o mock/subscriber.go -fake-name SubscriberMock . Subscriber
type Subscriber = events.Subscriber

type Driver struct {
	BackendFactory    BackendFactory
	Resolver          Resolver
	Subscriber        Subscriber
	ViewManager       ViewManager
	ResponderRegistry ResponderRegistry
	MetricsProvider   metrics.Provider

	Sync                 sync.Mutex
	CertificationClients map[string]*CertificationClient
	CertificationService *CertificationService
}

func NewDriver(backendFactory BackendFactory, resolver Resolver, subscriber Subscriber, viewManager ViewManager, responderRegistry ResponderRegistry, metricsProvider metrics.Provider) *Driver {
	return &Driver{
		BackendFactory:       backendFactory,
		Resolver:             resolver,
		Subscriber:           subscriber,
		ViewManager:          viewManager,
		ResponderRegistry:    responderRegistry,
		MetricsProvider:      metricsProvider,
		CertificationClients: map[string]*CertificationClient{},
	}
}

func (d *Driver) NewCertificationClient(ctx context.Context, tms *token.ManagementService) (driver.CertificationClient, error) {
	d.Sync.Lock()
	defer d.Sync.Unlock()

	k := tms.Channel() + ":" + tms.Namespace()

	cm, ok := d.CertificationClients[k]
	if !ok {
		certification := &Certification{}
		if err := tms.Configuration().UnmarshalKey(ConfigurationKey, &certification); err != nil {
			return nil, errors.Wrap(err, "failed unmarshalling certification config")
		}

		certifiers, err := d.Resolver.ResolveIdentities(certification.IDs...)
		if err != nil {
			return nil, errors.WithMessagef(err, "cannot resolve certifier identities")
		}

		if len(certifiers) == 0 {
			return nil, errors.Errorf("no certifier id configured")
		}

		maxAttempts := certification.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = DefaultMaxAttempts
		}

		waitTime := certification.WaitTime
		if waitTime <= 0 {
			waitTime = DefaultWaitTime
		}

		batchSize := certification.BatchSize
		if batchSize <= 0 {
			batchSize = DefaultBatchSize
		}

		bufferSize := certification.BufferSize
		if bufferSize <= 0 {
			bufferSize = DefaultBufferSize
		}

		flushInterval := certification.FlushInterval
		if flushInterval <= 0 {
			flushInterval = DefaultFlushInterval
		}

		workers := certification.Workers
		if workers <= 0 {
			workers = DefaultWorkers
		}

		responseTimeout := certification.ResponseTimeout
		if responseTimeout <= 0 {
			responseTimeout = DefaultResponseTimeout
		}

		certificationClient := NewCertificationClient(
			ctx,
			tms.Network(),
			tms.Channel(),
			tms.Namespace(),
			tms.Vault().NewQueryEngine(),
			tms.Vault().CertificationStorage(),
			d.ViewManager,
			certifiers,
			d.Subscriber,
			maxAttempts,
			waitTime,
			batchSize,
			bufferSize,
			flushInterval,
			workers,
			responseTimeout,
			d.MetricsProvider,
		)
		if err := certificationClient.Scan(); err != nil {
			logger.Warnf("failed to scan the vault for tokens to be certified [%s]", err)
		}

		certificationClient.Start()

		d.CertificationClients[k] = certificationClient
		cm = certificationClient
	}

	return cm, nil
}

func (d *Driver) NewCertificationService(tms *token.ManagementService, wallet string) (driver.CertificationService, error) {
	d.Sync.Lock()
	defer d.Sync.Unlock()

	if d.CertificationService == nil {
		backend, err := d.BackendFactory(tms, wallet)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to create backend")
		}

		d.CertificationService = NewCertificationService(d.ResponderRegistry, d.MetricsProvider, backend)
	}

	d.CertificationService.SetWallet(tms, wallet)

	return d.CertificationService, nil
}

type ChaincodeBackend struct{}

func (c *ChaincodeBackend) Load(context view.Context, cr *CertificationRequest) ([][]byte, error) {
	logger.Debugf("invoke chaincode to get commitments for [%v]", cr.IDs)
	// TODO: if the certifier fetches all token transactions, it might have the tokens in its on vault.
	tokensBoxed, err := context.RunView(tcc.NewGetTokensView(cr.Channel, cr.Namespace, cr.IDs...))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tokens [%s:%s][%v]", cr.Channel, cr.Namespace, cr.IDs)
	}

	tokenOutputs, ok := tokensBoxed.([][]byte)
	if !ok {
		return nil, errors.Errorf("expected [][]byte, got [%T]", tokensBoxed)
	}

	return tokenOutputs, nil
}
