/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup

import (
	"bytes"
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/go-co-op/gocron/v2"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/committer/queryservice"
)

var logger = logging.MustGetLogger()

// QueryService models the FabricX query service needed by the NSListenerManager
//
//go:generate counterfeiter -o mock/qs.go -fake-name QueryService . QueryService
type QueryService = queryservice.QueryService

// QueryServiceProvider is an alias for queryservice.Provider
//
//go:generate counterfeiter -o mock/qps.go -fake-name QueryServiceProvider . QueryServiceProvider
type QueryServiceProvider = queryservice.Provider

// Listener is an alias for lookup.Listener
//
//go:generate counterfeiter -o mock/ll.go -fake-name Listener . Listener
type Listener = lookup.Listener

// CronListenerManager is a lookup listener manager that uses gocron for task scheduling.
type CronListenerManager struct {
	queryService QueryService
	config       ConfigGetter
	scheduler    gocron.Scheduler

	mu   sync.RWMutex
	jobs map[Listener]gocron.Job
}

// NewCronListenerManager initializes a lookup listener manager and starts
// its underlying gocron scheduler for task orchestration.
func NewCronListenerManager(qs QueryService, config ConfigGetter) (*CronListenerManager, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, errors.Wrapf(err, "failed creating scheduler")
	}
	s.Start()

	return &CronListenerManager{
		queryService: qs,
		config:       config,
		scheduler:    s,
		jobs:         make(map[Listener]gocron.Job),
	}, nil
}

// PermanentLookupListenerSupported returns true, confirming that periodic
// state checks are implemented by this manager.
func (n *CronListenerManager) PermanentLookupListenerSupported() bool {
	return true
}

// AddPermanentLookupListener registers a listener that is notified whenever
// the value of a key changes. It schedules a recurring job that polls
// the query service at the configured permanent interval.
func (n *CronListenerManager) AddPermanentLookupListener(namespace string, key string, listener Listener) error {
	logger.Debugf("AddPermanentLookupListener [%s:%s]", namespace, key)

	job := NewPermanentJob(namespace, key, listener, n.queryService)
	j, err := n.scheduler.NewJob(
		gocron.DurationJob(n.config.PermanentInterval()),
		gocron.NewTask(job.Run),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return errors.Wrapf(err, "failed scheduling permanent lookup job")
	}

	n.mu.Lock()
	n.jobs[listener] = j
	n.mu.Unlock()

	return nil
}

// AddLookupListener registers a listener that waits for a specific key to
// be created or updated. It schedules a job that polls the ledger
// repeatedly until the key is found or the configured deadline is reached.
// The job is automatically removed after completion.
func (n *CronListenerManager) AddLookupListener(namespace string, key string, listener lookup.Listener) error {
	logger.Debugf("AddLookupListener [%s:%s]", namespace, key)

	deadline := time.Now().Add(n.config.OnceDeadline())

	// Use a pointer to the job so the task can remove it
	var j gocron.Job
	var err error

	task := gocron.NewTask(func() {
		logger.Debugf("[KeyCheck] check for key [%s:%s]", namespace, key)
		v, err := n.queryService.GetState(namespace, key)
		if err == nil && v != nil && len(v.Raw) != 0 {
			logger.Debugf("[KeyCheck] key [%s:%s] found, notify listener", namespace, key)
			listener.OnStatus(context.Background(), key, v.Raw)

			// Stop the job, no error is expected here
			_ = n.RemoveLookupListener(key, listener)

			return
		}

		if time.Now().After(deadline) {
			logger.Debugf("[KeyCheck] key [%s:%s] not found, deadline reached", namespace, key)
			listener.OnError(context.Background(), key, errors.Errorf("key [%s:%s] not found", namespace, key))

			// Stop the job, no error is expected here
			_ = n.RemoveLookupListener(key, listener)

			return
		}
	})

	j, err = n.scheduler.NewJob(
		gocron.DurationJob(n.config.OnceInterval()),
		task,
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return errors.Wrapf(err, "failed scheduling lookup job")
	}

	n.mu.Lock()
	n.jobs[listener] = j
	n.mu.Unlock()

	return nil
}

// RemoveLookupListener stops and removes the gocron job associated
// with the provided listener.
func (n *CronListenerManager) RemoveLookupListener(id string, listener Listener) error {
	logger.Debugf("RemoveLookupListener [%s]", id)
	n.mu.Lock()
	defer n.mu.Unlock()

	if j, ok := n.jobs[listener]; ok {
		err := n.scheduler.RemoveJob(j.ID())
		if err != nil {
			logger.Warningf("failed removing job [%s]: %s", j.ID(), err)
		}
		delete(n.jobs, listener)
	}

	return nil
}

// Stop shuts down the gocron scheduler and waits for active jobs to finish.
func (n *CronListenerManager) Stop() error {
	return n.scheduler.Shutdown()
}

// CronNSListenerManagerProvider is a provider for CronListenerManager.
type CronNSListenerManagerProvider struct {
	QueryServiceProvider QueryServiceProvider
	config               ConfigGetter
}

// NewCronNSListenerManagerProvider creates a provider that initializes
// CronListenerManager instances using the specified query service provider
// and configuration.
func NewCronNSListenerManagerProvider(queryServiceProvider QueryServiceProvider, config ConfigGetter) lookup.ListenerManagerProvider {
	return &CronNSListenerManagerProvider{
		QueryServiceProvider: queryServiceProvider,
		config:               config,
	}
}

// NewManager creates a new CronListenerManager for the specified network
// and channel by retrieving the appropriate query service.
func (n *CronNSListenerManagerProvider) NewManager(network, channel string) (lookup.ListenerManager, error) {
	qs, err := n.QueryServiceProvider.Get(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting query service")
	}

	return NewCronListenerManager(qs, n.config)
}

type PermanentJob struct {
	namespace string
	key       string
	listener  Listener

	queryService QueryService
	lastValue    []byte
}

// NewPermanentJob returns a state object for tracking a key's value across polls.
func NewPermanentJob(namespace string, key string, listener Listener, queryService QueryService) *PermanentJob {
	return &PermanentJob{namespace: namespace, key: key, listener: listener, queryService: queryService}
}

// Run executes a single poll of the ledger key. It compares the current value
// hash with the last seen hash and notifies the listener only if a change
// is detected.
func (j *PermanentJob) Run() {
	logger.Debugf("[PermanentKeyCheck] check for key [%s:%s]", j.namespace, j.key)
	v, err := j.queryService.GetState(j.namespace, j.key)
	if err == nil && v != nil && len(v.Raw) != 0 {
		newHash := token.Hashable(v.Raw).Raw()
		if !bytes.Equal(j.lastValue, newHash) {
			logger.Debugf("[PermanentKeyCheck] key [%s:%s] found with new value [%s], notify listener", j.namespace, j.key, base64.StdEncoding.EncodeToString(newHash))
			j.listener.OnStatus(context.Background(), j.key, v.Raw)
			j.lastValue = token.Hashable(v.Raw).Raw()
		}
	}
}
