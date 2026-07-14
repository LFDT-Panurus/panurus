/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms_test

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	"github.com/stretchr/testify/require"
)

type fakeConfig struct {
	id driver.TMSID
}

func (f *fakeConfig) ID() driver.TMSID                          { return f.id }
func (f *fakeConfig) IsSet(key string) bool                     { return false }
func (f *fakeConfig) UnmarshalKey(key string, rawVal any) error { return nil }
func (f *fakeConfig) GetString(key string) string               { return "" }
func (f *fakeConfig) GetBool(key string) bool                   { return false }
func (f *fakeConfig) TranslatePath(path string) string          { return path }

type fakeConfigService struct {
	configs []driver.Configuration
	err     error
}

func (f *fakeConfigService) Configurations() ([]driver.Configuration, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.configs, nil
}

type fakeNormalizer struct {
	called   bool
	received *token.ServiceOptions
	retErr   error
	retOpts  *token.ServiceOptions
}

func (f *fakeNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	f.called = true
	f.received = opt
	if f.retOpts != nil {
		return f.retOpts, f.retErr
	}

	return opt, f.retErr
}

func TestNormalize_ConfigServiceError(t *testing.T) {
	cs := &fakeConfigService{err: errors.New("boom")}
	norm := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, norm)

	_, err := n.Normalize(&token.ServiceOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed getting tms configs")
}

func TestNormalize_NoConfigs(t *testing.T) {
	cs := &fakeConfigService{configs: []driver.Configuration{}}
	norm := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, norm)

	_, err := n.Normalize(&token.ServiceOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no token management service configs found")
}

func TestNormalize_FilterByNetwork_HappyPath(t *testing.T) {
	configs := []driver.Configuration{
		&fakeConfig{id: driver.TMSID{Network: "net1", Channel: "c1", Namespace: "ns1"}},
		&fakeConfig{id: driver.TMSID{Network: "net2", Channel: "c2", Namespace: "ns2"}},
	}
	cs := &fakeConfigService{configs: configs}
	norm := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, norm)

	opt := &token.ServiceOptions{Network: "net2"}
	res, err := n.Normalize(opt)
	require.NoError(t, err)
	require.True(t, norm.called)
	require.Equal(t, "net2", res.Network)
	require.Equal(t, "c2", res.Channel)
	require.Equal(t, "ns2", res.Namespace)
}

func TestNormalize_NoConfigForNetwork(t *testing.T) {
	configs := []driver.Configuration{
		&fakeConfig{id: driver.TMSID{Network: "net1", Channel: "c1", Namespace: "ns1"}},
	}
	cs := &fakeConfigService{configs: configs}
	norm := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, norm)

	opt := &token.ServiceOptions{Network: "nope"}
	_, err := n.Normalize(opt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no token management service config found for network")
}

func TestNormalize_FilterByChannel_NoMatchAndMatch(t *testing.T) {
	configs := []driver.Configuration{
		&fakeConfig{id: driver.TMSID{Network: "net1", Channel: "c1", Namespace: "ns1"}},
		&fakeConfig{id: driver.TMSID{Network: "net1", Channel: "c2", Namespace: "ns2"}},
	}
	cs := &fakeConfigService{configs: configs}
	norm := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, norm)

	opt := &token.ServiceOptions{Channel: "c2"}
	res, err := n.Normalize(opt)
	require.NoError(t, err)
	require.True(t, norm.called)
	require.Equal(t, "net1", res.Network)
	require.Equal(t, "c2", res.Channel)
	require.Equal(t, "ns2", res.Namespace)

	opt = &token.ServiceOptions{Channel: "c3"}
	_, err = n.Normalize(opt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no token management service config found for network and channel")
}

func TestNormalize_FilterByNamespace_NoMatch(t *testing.T) {
	configs := []driver.Configuration{
		&fakeConfig{id: driver.TMSID{Network: "net1", Channel: "c1", Namespace: "ns1"}},
	}
	cs := &fakeConfigService{configs: configs}
	norm := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, norm)

	opt := &token.ServiceOptions{Namespace: "nope"}
	_, err := n.Normalize(opt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no token management service config found for network, channel, and namespace")
}

func TestNormalize_HappyPath_DefaultsTakenFromFirstConfig(t *testing.T) {
	configs := []driver.Configuration{
		&fakeConfig{id: driver.TMSID{Network: "N", Channel: "C", Namespace: "NS"}},
	}
	cs := &fakeConfigService{configs: configs}
	f := &fakeNormalizer{}
	n := tms.NewTMSNormalizer(cs, f)

	opt := &token.ServiceOptions{}
	res, err := n.Normalize(opt)
	require.NoError(t, err)
	require.True(t, f.called)
	require.Equal(t, "N", res.Network)
	require.Equal(t, "C", res.Channel)
	require.Equal(t, "NS", res.Namespace)
}
