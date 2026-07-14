/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/mocks"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

//go:generate counterfeiter -o mocks/envelope.go -fake-name Envelope github.com/LFDT-Panurus/panurus/token/services/network/driver.Envelope
//go:generate counterfeiter -o mocks/local_membership.go -fake-name LocalMembership github.com/LFDT-Panurus/panurus/token/services/network/driver.LocalMembership
//go:generate counterfeiter -o mocks/ledger.go -fake-name Ledger github.com/LFDT-Panurus/panurus/token/services/network/driver.Ledger
//go:generate counterfeiter -o mocks/network.go -fake-name Network github.com/LFDT-Panurus/panurus/token/services/network/driver.Network
//go:generate counterfeiter -o mocks/driver.go -fake-name Driver github.com/LFDT-Panurus/panurus/token/services/network/driver.Driver
//go:generate counterfeiter -o mocks/service_provider.go -fake-name ServiceProvider github.com/LFDT-Panurus/panurus/token.ServiceProvider

func TestValidationCodes(t *testing.T) {
	require.Equal(t, driver.Valid, network.Valid)
	require.Equal(t, driver.Invalid, network.Invalid)
	require.Equal(t, driver.Busy, network.Busy)
	require.Equal(t, driver.Unknown, network.Unknown)
}

func TestTxID(t *testing.T) {
	txID := &network.TxID{
		Nonce:   []byte("nonce"),
		Creator: []byte("creator"),
	}
	require.Equal(t, "[bm9uY2U=:Y3JlYXRvcg==]", txID.String())
}

func TestTransientMap(t *testing.T) {
	m := network.TransientMap{}
	require.True(t, m.IsEmpty())

	err := m.Set("key1", []byte("val1"))
	require.NoError(t, err)

	require.False(t, m.IsEmpty())
	require.True(t, m.Exists("key1"))
	require.False(t, m.Exists("key2"))
	require.Equal(t, []byte("val1"), m.Get("key1"))
	require.Nil(t, m.Get("key2"))

	type State struct {
		Name string
	}
	err = m.SetState("state", State{Name: "test"})
	require.NoError(t, err)
	require.True(t, m.Exists("state"))

	err = m.SetState("bad_channel", make(chan int))
	require.Error(t, err)

	var s State
	err = m.GetState("state", &s)
	require.NoError(t, err)
	require.Equal(t, "test", s.Name)

	err = m.GetState("not_exist", &s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exists")

	err = m.Set("empty", []byte{})
	require.NoError(t, err)
	err = m.GetState("empty", &s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "is empty")

	err = m.Set("bad_json", []byte("bad"))
	require.NoError(t, err)
	err = m.GetState("bad_json", &s)
	require.Error(t, err)
}

func TestEnvelope(t *testing.T) {
	dn := &mocks.Network{}
	driverEnv := &mocks.Envelope{}
	dn.NewEnvelopeReturns(driverEnv)

	driverEnv.BytesReturns([]byte("bytes"), nil)
	driverEnv.TxIDReturns("txid")
	driverEnv.StringReturns("string")

	n := network.NewNetwork(dn, nil)
	env := n.NewEnvelope()

	bytes, err := env.Bytes()
	require.NoError(t, err)
	require.Equal(t, []byte("bytes"), bytes)

	err = env.FromBytes([]byte("new_bytes"))
	require.NoError(t, err)
	require.Equal(t, 1, driverEnv.FromBytesCallCount())

	require.Equal(t, "txid", env.TxID())
	require.Equal(t, "string", env.String())

	// Test MarshalJSON
	driverEnv.BytesReturns([]byte("json_bytes"), nil)
	b, err := env.MarshalJSON()
	require.NoError(t, err)
	var decoded []byte
	err = json.Unmarshal(b, &decoded)
	require.NoError(t, err)
	require.Equal(t, []byte("json_bytes"), decoded)

	// Test MarshalJSON error
	driverEnv.BytesReturns(nil, errors.New("bytes err"))
	_, err = env.MarshalJSON()
	require.Error(t, err)

	// Test UnmarshalJSON
	err = env.UnmarshalJSON(b)
	require.NoError(t, err)
	require.Equal(t, 2, driverEnv.FromBytesCallCount())

	err = env.UnmarshalJSON([]byte("bad_json"))
	require.Error(t, err)
}

func TestLocalMembership(t *testing.T) {
	dlm := &mocks.LocalMembership{}
	dlm.DefaultIdentityReturns(view.Identity("default"))
	dlm.AnonymousIdentityReturns(view.Identity("anon"), nil)

	lm := network.NewLocalMembership(dlm)

	id := lm.DefaultIdentity()
	require.Equal(t, view.Identity("default"), id)

	anon, err := lm.AnonymousIdentity()
	require.NoError(t, err)
	require.Equal(t, view.Identity("anon"), anon)
}

func TestLedger(t *testing.T) {
	dn := &mocks.Network{}
	dl := &mocks.Ledger{}
	dn.LedgerReturns(dl, nil)

	dl.StatusReturns(network.Valid, nil)
	dl.GetStatesReturns([][]byte{[]byte("state1")}, nil)
	dl.TransferMetadataKeyReturns("meta_key", nil)

	n := network.NewNetwork(dn, nil)
	l, err := n.Ledger()
	require.NoError(t, err)

	vc, msg, err := l.Status("tx1")
	require.NoError(t, err)
	require.Equal(t, network.Valid, vc)
	require.Empty(t, msg)

	dl.StatusReturns(network.Unknown, errors.New("status err"))
	_, _, err = l.Status("tx1")
	require.Error(t, err)

	states, err := l.GetStates(context.Background(), "ns", "k1")
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("state1")}, states)

	mk, err := l.TransferMetadataKey("k1")
	require.NoError(t, err)
	require.Equal(t, "meta_key", mk)
}

func TestNetwork(t *testing.T) {
	dn := &mocks.Network{}
	dlm := &mocks.LocalMembership{}

	dn.NameReturns("my_network")
	dn.ChannelReturns("my_channel")
	dn.LocalMembershipReturns(dlm)
	dlm.AnonymousIdentityReturns(view.Identity("anon"), nil)

	denv := &mocks.Envelope{}
	dn.NewEnvelopeReturns(denv)

	dn.BroadcastReturns(nil)
	dn.RequestApprovalReturns(denv, nil)
	dn.ComputeTxIDReturns("computed_txid")
	dn.FetchPublicParametersReturns([]byte("pp"), nil)
	dn.QueryTokensReturns([][]byte{[]byte("t1")}, nil)
	dn.AreTokensSpentReturns([]bool{true}, nil)
	dn.AddFinalityListenerReturns(nil)
	dn.LookupTransferMetadataKeyReturns([]byte("mkey"), nil)

	dl := &mocks.Ledger{}
	dn.LedgerReturns(dl, nil)

	opts := &token.ServiceOptions{Network: "foo"}
	dn.NormalizeReturns(opts, nil)
	dn.ConnectReturns([]token.ServiceOption{token.WithNetwork("foo")}, nil)

	n := network.NewNetwork(dn, network.NewLocalMembership(dlm))
	require.Equal(t, "my_network", n.Name())
	require.Equal(t, "my_channel", n.Channel())

	anon, err := n.AnonymousIdentity()
	require.NoError(t, err)
	require.Equal(t, view.Identity("anon"), anon)

	env := n.NewEnvelope()
	require.NotNil(t, env)

	err = n.Broadcast(context.Background(), env)
	require.NoError(t, err)

	err = n.Broadcast(context.Background(), "blob")
	require.NoError(t, err)

	txID := network.TxID{Nonce: []byte("nonce"), Creator: []byte("creator")}
	apprEnv, err := n.RequestApproval(nil, nil, []byte("req"), view.Identity("sig"), txID, nil)
	require.NoError(t, err)
	require.NotNil(t, apprEnv)

	dn.RequestApprovalReturns(nil, errors.New("appr err"))
	_, err = n.RequestApproval(nil, nil, []byte("req"), view.Identity("sig"), txID, nil)
	require.Error(t, err)

	cTxID := n.ComputeTxID(&txID)
	require.Equal(t, "computed_txid", cTxID)

	pp, err := n.FetchPublicParameters("ns")
	require.NoError(t, err)
	require.Equal(t, []byte("pp"), pp)

	toks, err := n.QueryTokens(context.Background(), "ns", []*token2.ID{{TxId: "tx", Index: 0}})
	require.NoError(t, err)
	require.Len(t, toks, 1)

	spent, err := n.AreTokensSpent(context.Background(), "ns", []*token2.ID{{TxId: "tx", Index: 0}}, []string{"meta"})
	require.NoError(t, err)
	require.Equal(t, []bool{true}, spent)

	require.NotNil(t, n.LocalMembership())

	err = n.AddFinalityListener("ns", "txid", nil)
	require.NoError(t, err)

	mk, err := n.LookupTransferMetadataKey("ns", "k", time.Second)
	require.NoError(t, err)
	require.Equal(t, []byte("mkey"), mk)

	ldg, err := n.Ledger()
	require.NoError(t, err)
	require.NotNil(t, ldg)

	dn.LedgerReturns(nil, errors.New("ledger err"))
	_, err = n.Ledger()
	require.Error(t, err)

	opt, err := n.Normalize(opts)
	require.NoError(t, err)
	require.Equal(t, opts, opt)

	so, err := n.Connect("ns")
	require.NoError(t, err)
	require.Len(t, so, 1)
}

func TestProvider(t *testing.T) {
	dp := &mocks.Driver{}
	dn := &mocks.Network{}
	dlm := &mocks.LocalMembership{}
	dn.LocalMembershipReturns(dlm)

	dp.NewReturns(dn, nil)

	p := network.NewProvider(nil) // we can pass nil config for simple tests
	p.RegisterDriver(dp)

	net, err := p.GetNetwork("net", "chan")
	require.NoError(t, err)
	require.NotNil(t, net)
	// I cannot access `net.n` anymore as `net` is opaque, but let's test a method
	dn.NameReturns("my_network")
	require.Equal(t, "my_network", net.Name())

	// Test Normalize
	opts := &token.ServiceOptions{Network: "net", Channel: "chan"}
	dn.NormalizeReturns(opts, nil)
	opt, err := p.Normalize(opts)
	require.NoError(t, err)
	require.Equal(t, opts, opt)

	_, err = p.Normalize(nil)
	require.Error(t, err)

	dp.NewReturns(nil, errors.New("driver_err"))
	p2 := network.NewProvider(nil)
	p2.RegisterDriver(dp)
	_, err = p2.GetNetwork("net2", "chan2")
	require.Error(t, err)

	_, err = p2.Normalize(&token.ServiceOptions{Network: "net2", Channel: "chan2"})
	require.Error(t, err)
}

func TestGetInstance(t *testing.T) {
	sp := &mocks.ServiceProvider{}
	dn := &mocks.Network{}
	dlm := &mocks.LocalMembership{}
	dn.LocalMembershipReturns(dlm)
	dr := &mocks.Driver{}
	dr.NewReturns(dn, nil)

	p := network.NewProvider(nil)
	p.RegisterDriver(dr)

	sp.GetServiceReturns(p, nil)

	net := network.GetInstance(sp, "n1", "c1")
	require.NotNil(t, net)
	dn.NameReturns("my_net")
	require.Equal(t, "my_net", net.Name())

	sp.GetServiceReturns(nil, errors.New("err"))
	require.Panics(t, func() {
		network.GetInstance(sp, "n1", "c1")
	})

	// Test panic
	sp.GetServiceReturns(nil, errors.New("no service"))
	require.Panics(t, func() {
		network.GetProvider(sp)
	})
}
