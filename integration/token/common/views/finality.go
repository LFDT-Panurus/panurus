/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type TxFinality struct {
	TxID    string
	TMSID   *token.TMSID
	Timeout time.Duration
}

type TxFinalityView struct {
	*TxFinality
}

func (r *TxFinalityView) Call(context view.Context) (any, error) {
	var tmsID token.TMSID
	if r.TMSID != nil {
		tmsID = *r.TMSID
	}

	errs := make(chan error, 2)

	// Listen for finality from vault
	tms, err := token.GetManagementService(context, token.WithTMSID(tmsID))
	assert.NoError(err)
	nw := network.GetInstance(context, tms.Network(), tms.Channel())
	assert.NotNil(nw)
	assert.NoError(nw.AddFinalityListener(tms.Namespace(), r.TxID, newFinalityListener(r.Timeout, errs)))

	// Listen for finality from DBs
	go func() {
		_, err := context.RunView(ttx.NewFinalityWithOpts(ttx.WithTxID(r.TxID), ttx.WithTMSID(tms.ID()), ttx.WithTimeout(r.Timeout)))
		errs <- err
	}()

	// When both arrive, return
	var err1, err2 error
	select {
	case err1 = <-errs:
		// Received first finality result
	case <-context.Context().Done():
		return nil, errors.Wrapf(context.Context().Err(), "context cancelled while waiting for first finality confirmation")
	}
	if err1 != nil {
		return nil, err1
	}

	select {
	case err2 = <-errs:
		// Received second finality result
	case <-context.Context().Done():
		return nil, errors.Wrapf(context.Context().Err(), "context cancelled while waiting for second finality confirmation")
	}

	return nil, err2
}

type TxFinalityViewFactory struct{}

func (p *TxFinalityViewFactory) NewView(in []byte) (view.View, error) {
	f := &TxFinalityView{TxFinality: &TxFinality{}}
	err := json.Unmarshal(in, f.TxFinality)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type finalityListener struct {
	success func()
}

func newFinalityListener(timeout time.Duration, errs chan error) *finalityListener {
	var once sync.Once

	if timeout > 0 {
		time.AfterFunc(timeout, func() { once.Do(func() { errs <- errors.New("timeout exceeded") }) })
	}

	return &finalityListener{
		success: func() { once.Do(func() { errs <- nil }) },
	}
}

func (l *finalityListener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	fmt.Printf("Received finality from network for TX [%s][%d]", txID, status)
	l.success()
}

func (l *finalityListener) OnError(ctx context.Context, txID string, err error) {
	fmt.Printf("Finality error for TX [%s]: %v", txID, err)
}
