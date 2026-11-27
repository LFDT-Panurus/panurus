/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network

import (
	"reflect"
	"testing"
	"time"

	selector "github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestLockerProvider_NoGoroutineOutlivesOwner asserts the desired behaviour of
// #2638: once the caller drops the locker built per TMS, no scan goroutine may
// remain. The simple.Locker interface exposes no Stop, so production cannot
// stop it.
func TestLockerProvider_NoGoroutineOutlivesOwner(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ttxMgr := &mockTTXStoreServiceManager{}
	ttxMgr.On("StoreServiceByTMSId", mock.Anything).Return(&ttxdb.StoreService{}, nil)

	p := NewLockerProvider(ttxMgr, 10*time.Millisecond, time.Minute)
	l, err := p.New("n", "c", "ns")
	require.NoError(t, err)
	require.NotNil(t, l)
	_, hasStop := any(l).(interface{ Stop() error })
	t.Logf("concrete locker has Stop: %v; simple.Locker interface has Stop: %v",
		hasStop, hasMethod(reflect.TypeFor[selector.Locker](), "Stop"))
}

// TestSelectorService_HasStop asserts the desired behaviour of #2638: the
// SelectorService that owns the per-TMS lockers must offer a stop path.
func TestSelectorService_HasStop(t *testing.T) {
	require.True(t, hasMethod(reflect.TypeFor[*selector.SelectorService](), "Stop"),
		"simple.SelectorService has no Stop, so its lockers can never be stopped")
}

func hasMethod(t reflect.Type, name string) bool {
	_, ok := t.MethodByName(name)

	return ok
}
