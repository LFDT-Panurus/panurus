/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model"
	txgen "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
	c "github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/constants"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/logging"
	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/service/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
)

var operationTypeMap = map[c.ApiRequestType]metrics.OperationType{
	c.PaymentInitiationRequest: metrics.Transfer,
	c.PaymentTransferRequest:   metrics.Transfer,
	c.BalanceRequest:           metrics.Balance,
	c.WithdrawRequest:          metrics.Withdraw,
}

func newRestUser(user model.UserConfig, metrics *metrics.Metrics, httpClient *http.Client, logger logging.Logger) *restUser {
	return &restUser{
		logger:     logger,
		httpClient: httpClient,
		username:   user.Username,
		endpoint:   user.Endpoint,
		password:   user.Password,
		metrics:    metrics,
	}
}

type restUser struct {
	logger         logging.Logger
	httpClient     *http.Client
	accessTokenExp time.Time
	username       model.Username
	endpoint       string
	password       string
	accessToken    string
	metrics        *metrics.Metrics
}

func (u *restUser) hasTokenExpired() bool {
	return len(u.accessToken) == 0 || u.accessTokenExp.Before(time.Now())
}

func (u *restUser) updateToken(token string) {
	u.accessToken = token
	u.accessTokenExp = time.Now().Add(c.PayerAccessTokenExpMin)
}

func (u *restUser) refreshAuthToken() txgen.Error {
	// TODO introduce concurrency check
	if u.hasTokenExpired() {
		u.logger.Infof("Refresh authentication token for %s", u.username)
		token, err := u.authenticateUser()
		if err != nil {
			return err
		}
		u.updateToken(token)
	}

	return nil
}

func (u *restUser) Withdraw(value txgen.Amount) txgen.Error {
	u.logger.Debug("Withdraw")
	if err := u.refreshAuthToken(); err != nil {
		return err
	}

	urlStr := fmt.Sprintf("%s/zkat/withdraw?user=%s", u.endpoint, u.username)
	form := url.Values{}
	form.Add("value", strconv.FormatUint(value, 10))

	u.logger.Debugf("Withdraw %s for %s\n", form.Encode(), u.username)

	request, _ := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(form.Encode()))
	request.Header.Add(c.HeaderContentType, c.ApplicationUrlEncoded)

	_, err := u.doRequest(request, c.WithdrawRequest)

	return err
}

func (u *restUser) GetBalance() (txgen.Amount, txgen.Error) {
	if err := u.refreshAuthToken(); err != nil {
		return 0, err
	}

	urlStr := fmt.Sprintf("%s/zkat/balance?user=%s", u.endpoint, u.username)
	request, _ := http.NewRequest(http.MethodGet, urlStr, nil)

	respBody, apiErr := u.doRequest(request, c.BalanceRequest)
	if apiErr != nil {
		return 0, apiErr
	}

	var balanceResponse BalanceResponse
	err := json.Unmarshal(respBody, &balanceResponse)

	if err != nil {
		u.logger.Errorf("Can't unmarshal body from get balance request: %s", err.Error())

		return 0, txgen.NewInternalServerError(err, "Can't unmarshal body")
	}

	u.logger.Debugf("User %s has balance %v", u.username, balanceResponse)

	amount, err := strconv.ParseUint(balanceResponse.Balance.Quantity, 10, 64)
	if err != nil {
		u.logger.Errorf("Can't convert balance api.Amount to int: %s, balance: %s", err.Error(), balanceResponse.Balance.Quantity)

		return 0, txgen.NewInternalServerError(err, "Can't convert balance api.Amount to int")
	}

	return amount, nil
}

func (u *restUser) Transfer(value txgen.Amount, recipient model.Username, nonce txgen.UUID) txgen.Error {
	u.logger.Debugf("Execute payment with nonce %s from %s to %s of %d", nonce.String(), u.username, recipient, value)
	if err := u.refreshAuthToken(); err != nil {
		return err
	}

	urlStr := u.endpoint + "/zkat/payments/transfer"
	form := newTransferForm(value, nonce, recipient)
	request, _ := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(form.Encode()))
	request.Header.Add(c.HeaderContentType, c.ApplicationUrlEncoded)

	_, err := u.doRequest(request, c.PaymentTransferRequest)

	return err
}

func (u *restUser) InitiateTransfer(value txgen.Amount, nonce txgen.UUID) txgen.Error {
	u.logger.Debugf("Initiate payment with nonce %s to %s ", nonce, u.username)
	if err := u.refreshAuthToken(); err != nil {
		return err
	}

	urlStr := u.endpoint + "/zkat/payments/initiation"
	form := newTransferForm(value, nonce, u.username)

	request, _ := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(form.Encode()))
	request.Header.Add(c.HeaderContentType, c.ApplicationUrlEncoded)

	_, err := u.doRequest(request, c.PaymentInitiationRequest)

	return err
}

func (u *restUser) doRequest(request *http.Request, requestType c.ApiRequestType) ([]byte, txgen.Error) {
	request.Header.Set(c.HeaderAuthorization, "Bearer "+u.accessToken)

	operationType := operationTypeMap[requestType]

	u.metrics.RequestsSent.
		With(metrics.OperationLabel, operationType).Add(1)

	start := time.Now()
	response, err := u.httpClient.Do(request)

	successType := metrics.SuccessValues[err == nil || response != nil && response.StatusCode >= http.StatusBadRequest]
	u.metrics.RequestsReceived.
		With(metrics.OperationLabel, operationType, metrics.SuccessLabel, successType).
		Add(1)
	u.metrics.RequestDuration.
		With(metrics.OperationLabel, operationType, metrics.SuccessLabel, successType).
		Observe(time.Since(start).Seconds())

	if err != nil {
		u.logger.Errorf("Can't make request %s for %s. Path: %s\n", err, u.username, request.URL.RequestURI())

		return nil, txgen.NewBadRequestError(err, "Can't make request")
	}

	defer utils.IgnoreError(response.Body.Close)
	respBody, _ := io.ReadAll(response.Body)

	if response.StatusCode >= http.StatusBadRequest {
		u.logger.Errorf("Request failed: %s for %s. Path: %s\n", string(respBody), u.username, request.URL.RequestURI())

		return nil, &txgen.AppError{
			Code:    response.StatusCode,
			Message: string(respBody),
		}
	}

	return respBody, nil
}

func (u *restUser) Username() model.Username {
	return u.username
}

func newTransferForm(value txgen.Amount, nonce txgen.UUID, username model.Username) url.Values {
	form := url.Values{}
	form.Add("value", strconv.FormatUint(value, 10))
	form.Add("recipient", username)
	form.Add("nonce", nonce.String())

	return form
}

func (u *restUser) authenticateUser() (string, txgen.Error) {
	u.logger.Infof("Authenticate user %s", u.username)
	url := u.endpoint + "/login"

	request := LoginRequest{
		Username: u.username,
		Password: u.password,
	}

	data, _ := json.Marshal(request) //nolint:gosec // G117: Password must be marshaled for authentication request

	response, err := u.httpClient.Post(url, c.ApplicationJson, bytes.NewReader(data))

	if err != nil {
		u.logger.Errorf("Can't make authentication request %s", err.Error())

		return "", txgen.NewBadRequestError(err, "Can't make authentication request")
	}

	defer utils.IgnoreError(response.Body.Close)
	respBody, _ := io.ReadAll(response.Body)

	if response.StatusCode >= http.StatusBadRequest {
		u.logger.Errorf("Failed authentication request: %s for %s\n", string(respBody), u.username)

		return "", &txgen.AppError{
			Code:    response.StatusCode,
			Message: string(respBody),
		}
	}

	var loginResponse LoginResponse
	err = json.Unmarshal(respBody, &loginResponse)

	if err != nil {
		u.logger.Errorf("Can't unmarshal body from authentication request: %s", err.Error())

		return "", txgen.NewInternalServerError(err, "Can't unmarshal body")
	}

	return loginResponse.Token, nil
}
