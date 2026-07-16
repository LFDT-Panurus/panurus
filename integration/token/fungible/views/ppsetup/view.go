/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ppsetup

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// SetupPublicParams describes a request to submit new or updated public parameters for a
// namespace through the FSC endorsement flow, as an alternative to the FabricX
// chaincode-bypass deployment path.
type SetupPublicParams struct {
	// Network is the name of the target network.
	Network string
	// Channel is the name of the target channel.
	Channel string
	// Namespace is the target namespace whose public parameters are being set up.
	Namespace string
	// PublicParamsRaw is the raw, serialized public parameters to submit.
	PublicParamsRaw []byte
	// Timeout bounds how long to wait for the setup transaction to reach finality.
	// A zero value means wait indefinitely.
	Timeout time.Duration
}

// SetupPublicParamsView drives public parameters setup/update through the FSC endorsement
// machinery: it invokes network.Network.SetupPublicParams, broadcasts the resulting envelope,
// and waits for the transaction to reach finality before returning.
type SetupPublicParamsView struct {
	*SetupPublicParams
}

// Call implements view.View.
func (v *SetupPublicParamsView) Call(ctx view.Context) (any, error) {
	nw := network.GetInstance(ctx, v.Network, v.Channel)
	if nw == nil {
		return nil, errors.Errorf("network not found for [%s:%s]", v.Network, v.Channel)
	}

	tmsID := token.TMSID{
		Network:   v.Network,
		Channel:   v.Channel,
		Namespace: v.Namespace,
	}

	signer := nw.LocalMembership().DefaultIdentity()
	txID := network.TxID{Creator: signer}
	computedTxID := nw.ComputeTxID(&txID)

	env, err := nw.SetupPublicParams(ctx, tmsID, v.PublicParamsRaw, signer, txID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to setup public params for [%s]", tmsID)
	}

	errs := make(chan error, 1)
	if err := nw.AddFinalityListener(v.Namespace, computedTxID, newFinalityListener(v.Timeout, errs)); err != nil {
		return nil, errors.WithMessagef(err, "failed to add finality listener for [%s]", computedTxID)
	}

	if err := nw.Broadcast(ctx.Context(), env); err != nil {
		return nil, errors.WithMessagef(err, "failed to broadcast setup transaction [%s]", computedTxID)
	}

	select {
	case err := <-errs:
		if err != nil {
			return nil, errors.WithMessagef(err, "failed waiting for finality of [%s]", computedTxID)
		}
	case <-ctx.Context().Done():
		return nil, errors.Wrapf(ctx.Context().Err(), "context cancelled while waiting for finality of [%s]", computedTxID)
	}

	return computedTxID, nil
}

// SetupPublicParamsViewFactory creates instances of SetupPublicParamsView.
type SetupPublicParamsViewFactory struct{}

// NewView implements view.Factory.
func (p *SetupPublicParamsViewFactory) NewView(in []byte) (view.View, error) {
	f := &SetupPublicParamsView{SetupPublicParams: &SetupPublicParams{}}
	if err := json.Unmarshal(in, f.SetupPublicParams); err != nil {
		return nil, errors.WithMessagef(err, "failed unmarshalling input")
	}

	return f, nil
}

// finalityListener waits for a single finality notification and reports its outcome on errs.
type finalityListener struct {
	report func(err error)
}

func newFinalityListener(timeout time.Duration, errs chan error) *finalityListener {
	var once sync.Once
	report := func(err error) { once.Do(func() { errs <- err }) }

	if timeout > 0 {
		time.AfterFunc(timeout, func() { report(errors.New("timeout exceeded")) })
	}

	return &finalityListener{report: report}
}

// OnStatus implements network.FinalityListener.
func (l *finalityListener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	if status != network.Valid {
		l.report(errors.Errorf("transaction [%s] is not valid, status [%d]: %s", txID, status, message))

		return
	}
	l.report(nil)
}

// OnError implements network.FinalityListener.
func (l *finalityListener) OnError(ctx context.Context, txID string, err error) {
	l.report(errors.WithMessagef(err, "finality error for transaction [%s]", txID))
}
