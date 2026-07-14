/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/sdk/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"
)

func TestNewAuditorCheckServiceProvider(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"auditdb-checkers"`
	}{
		TMSProvider:     tmsProvider,
		NetworkProvider: networkProvider,
		Checkers:        checkers,
	}

	provider := NewAuditorCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.AuditorCheckServiceProvider{}, provider)
}

func TestNewAuditorCheckServiceProvider_EmptyCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"auditdb-checkers"`
	}{
		TMSProvider:     tmsProvider,
		NetworkProvider: networkProvider,
		Checkers:        []common.NamedChecker{},
	}

	provider := NewAuditorCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.AuditorCheckServiceProvider{}, provider)
}

func TestNewAuditorCheckServiceProvider_MultipleCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker3", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"auditdb-checkers"`
	}{
		TMSProvider:     tmsProvider,
		NetworkProvider: networkProvider,
		Checkers:        checkers,
	}

	provider := NewAuditorCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.AuditorCheckServiceProvider{}, provider)
}

func TestNewOwnerCheckServiceProvider(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"ttxdb-checkers"`
	}{
		TMSProvider:     tmsProvider,
		NetworkProvider: networkProvider,
		Checkers:        checkers,
	}

	provider := NewOwnerCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.OwnerCheckServiceProvider{}, provider)
}

func TestNewOwnerCheckServiceProvider_EmptyCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"ttxdb-checkers"`
	}{
		TMSProvider:     tmsProvider,
		NetworkProvider: networkProvider,
		Checkers:        []common.NamedChecker{},
	}

	provider := NewOwnerCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.OwnerCheckServiceProvider{}, provider)
}

func TestNewOwnerCheckServiceProvider_MultipleCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker3", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"ttxdb-checkers"`
	}{
		TMSProvider:     tmsProvider,
		NetworkProvider: networkProvider,
		Checkers:        checkers,
	}

	provider := NewOwnerCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.OwnerCheckServiceProvider{}, provider)
}

func TestNewAuditorCheckServiceProvider_WithNilProviders(t *testing.T) {
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"auditdb-checkers"`
	}{
		TMSProvider:     nil,
		NetworkProvider: nil,
		Checkers:        checkers,
	}

	provider := NewAuditorCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.AuditorCheckServiceProvider{}, provider)
}

func TestNewOwnerCheckServiceProvider_WithNilProviders(t *testing.T) {
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	input := struct {
		dig.In
		TMSProvider     common.TokenManagementServiceProvider
		NetworkProvider common.NetworkProvider
		Checkers        []common.NamedChecker `group:"ttxdb-checkers"`
	}{
		TMSProvider:     nil,
		NetworkProvider: nil,
		Checkers:        checkers,
	}

	provider := NewOwnerCheckServiceProvider(input)

	require.NotNil(t, provider)
	assert.IsType(t, &db.OwnerCheckServiceProvider{}, provider)
}
