/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package runner

import (
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/runner"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/views"
)

// viewRunner enhances the underlying SuiteRunner by registering the auditor on start.
type viewRunner struct {
	runner.SuiteRunner
	viewUserProvider *ViewUserProvider
	logger           logging.Logger
	auditorId        model.Username
	issuerIds        []model.Username
}

func NewViewRunner(runner runner.SuiteRunner, userProvider *ViewUserProvider, logger logging.Logger, auditorId model.Username, issuerIds ...model.Username) *viewRunner {
	return &viewRunner{
		SuiteRunner:      runner,
		viewUserProvider: userProvider,
		logger:           logger,
		auditorId:        auditorId,
		issuerIds:        issuerIds,
	}
}

type viewClient interface {
	CallView(fid string, in []byte) (any, error)
}

func (r *viewRunner) Start(ctx context.Context) error {
	if err := r.SuiteRunner.Start(ctx); err != nil {
		return err
	}

	r.logger.Infof("Register auditor [%s]", r.auditorId)
	input, err := json.Marshal(&views.RegisterAuditor{})
	if err != nil {
		return err
	}
	_, err = r.client(r.auditorId).CallView("registerAuditor", input)
	if err != nil {
		return err
	}
	r.logger.Infof("Set KVS entry on %d issuer(s): [%v]", len(r.issuerIds), r.issuerIds)
	input, err = json.Marshal(&views.KVSEntry{Key: "auditor", Value: r.auditorId})
	if err != nil {
		return err
	}
	for _, issuerId := range r.issuerIds {
		if _, err = r.client(issuerId).CallView("SetKVSEntry", input); err != nil {
			return err
		}
	}
	r.logger.Infof("Done with initialization")

	return nil
}

func (r *viewRunner) client(id model.Username) viewClient {
	return r.viewUserProvider.Get(id).(viewClient)
}
