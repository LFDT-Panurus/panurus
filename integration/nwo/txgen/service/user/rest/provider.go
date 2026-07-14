/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rest

import (
	"net/http"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/metrics"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/user"
)

type restUserProvider struct {
	users map[model.UserAlias]*restUser
}

func NewRestUserProvider(config model.UserProviderConfig, metrics *metrics.Metrics, logger logging.Logger) user.Provider {
	users := make(map[model.UserAlias]*restUser, len(config.Users))
	for _, user := range config.Users {
		users[user.Name] = newRestUser(user, metrics, newHttpClient(config.HttpClient), logger)
	}

	return &restUserProvider{users: users}
}

func newHttpClient(c model.HttpClientConfig) *http.Client {
	return &http.Client{
		Timeout: c.Timeout,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: c.MaxIdleConnsPerHost,
			MaxConnsPerHost:     c.MaxConnsPerHost,
			//	IdleConnTimeout: c.INTERMEDIARY_REQUEST_TIMEOUT_SEC * time.Second,
		},
	}
}

func (u *restUserProvider) Get(name model.UserAlias) user.User {
	return u.users[name]
}
