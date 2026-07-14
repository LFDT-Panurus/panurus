/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package certifier

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/certifier"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

var logger = logging.MustGetLogger()

type RegisterView struct {
	Network   string
	Channel   string
	Namespace string
	Wallet    string
}

func NewRegisterView(network string, channel string, namespace string, wallet string) *RegisterView {
	return &RegisterView{Network: network, Channel: channel, Namespace: namespace, Wallet: wallet}
}

func (r *RegisterView) Call(context view.Context) (any, error) {
	// If the tms does not support graph hiding, skip
	tms, err := token.GetManagementService(
		context,
		token.WithNetwork(r.Network),
		token.WithChannel(r.Channel),
		token.WithNamespace(r.Namespace),
	)
	if err != nil {
		return nil, errors.Wrapf(err, "tms not found [%s:%s:%s]", r.Network, r.Channel, r.Namespace)
	}
	pp := tms.PublicParametersManager().PublicParameters()
	if pp == nil {
		logger.Debugf("public parameters not yet available, start a background task...")

		// Use the view's context for cancellation.
		// Keep trying until parameters are ready or the context is cancelled.
		ctx := context.Context()

		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					logger.Debugf("context cancelled (reason: %v), stopping certification service setup", ctx.Err())

					return
				case <-ticker.C:
					pp := tms.PublicParametersManager().PublicParameters()
					if pp != nil {
						logger.Debugf("public parameters available, set certification service...")
						if err := r.startCertificationService(context, tms, pp); err != nil {
							logger.Errorf("failed to start certification service [%s]", err)
						}

						return
					}
					logger.Debugf("public parameters not yet available, wait...")
				}
			}
		}()
	} else {
		logger.Debugf("public parameters available, set certification service...")
		if err := r.startCertificationService(context, tms, pp); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (r *RegisterView) startCertificationService(context view.Context, tms *token.ManagementService, pp *token.PublicParameters) error {
	if !pp.GraphHiding() {
		logger.Warnf("the token management system for [%s:%s] does not support graph hiding, skipping certifier registration", r.Channel, r.Namespace)

		return nil
	}

	// Start Certifier
	certificationDriver := pp.CertificationDriver()
	logger.Debugf("start certification service with driver [%s]...", certificationDriver)
	c, err := certifier.NewCertificationService(tms, r.Wallet)
	if err != nil {
		return errors.WithMessagef(err, "failed instantiating certifier [%s]", tms)
	}
	if err := c.Start(); err != nil {
		return errors.WithMessagef(err, "failed starting certifier [%s]", tms)
	}

	return nil
}
