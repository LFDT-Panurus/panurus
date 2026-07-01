/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package db

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
)

var logger = logging.MustGetLogger()

type ConfigService interface {
	ConfigurationFor(network string, channel string, namespace string) (driver.Configuration, error)
}

type StoreServiceManager[S any] interface {
	StoreServiceByTMSId(token.TMSID) (S, error)
}

type manager[S any] struct{ lazy.Provider[token.TMSID, S] }

func NewStoreServiceManager[S any, T any](
	config ConfigService,
	prefix string,
	constructor func(name driver2.PersistenceName, params ...string) (S, error),
	mapper func(S) (T, error),
) StoreServiceManager[T] {
	return &manager[T]{
		Provider: lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (T, error) {
			logger.Infof("Creating manager for %T for [%s] and prefix [%s]", new(T), tmsID, prefix)
			cfg, err := config.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
			if err != nil {
				return utils.Zero[T](), err
			}

			s, err := constructor(
				common.GetPersistenceName(cfg, prefix),
				tmsID.Network,
				tmsID.Channel,
				tmsID.Namespace,
			)
			if err != nil {
				return utils.Zero[T](), err
			}

			return mapper(s)
		}),
	}
}

func (m *manager[S]) StoreServiceByTMSId(id token.TMSID) (S, error) { return m.Get(id) }
