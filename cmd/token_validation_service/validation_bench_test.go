/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/integration/benchmark"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

// viewPool pre-generates distinct views so that benchmark iterations rotate
// through different ZK proofs instead of repeatedly verifying same data.
type viewPool struct {
	views []view.View
	idx   atomic.Int64
}

const (
	defaultNumViews = 64
)

// shuffle randomizes the slice order unless shuffling is disabled.
func shuffle[T any](s []T, noSeed bool, disable bool) {
	if disable {
		return
	}
	if !noSeed {
		r := rand.New(rand.NewPCG(42, 54))
		r.Shuffle(len(s), func(i, j int) {
			s[i], s[j] = s[j], s[i]
		})
	} else {
		rand.Shuffle(len(s), func(i, j int) {
			s[i], s[j] = s[j], s[i]
		})
	}
}

// CreateViewsWithProofs builds a pool of benchmark views backed by transfer proofs.
func CreateViewsWithProofs(b *testing.B, testRoot string, f *TokenValidationServiceViewFactory, n int) (*viewPool, []*transferServiceParams) {
	b.Helper()
	vp := &viewPool{}

	ps, err := NewTokenValidationParamsSlice(testRoot)
	require.NoError(b, err)
	shuffle(ps, false, false)

	// Create n views
	vp.views = make([]view.View, n)
	for i := range vp.views {
		params := ps[i%len(ps)]

		input, _ := json.Marshal(params)
		v, err := f.NewView(input)
		require.NoError(b, err)
		vp.views[i] = v
	}

	vp.idx.Store(0)

	return vp, ps
}

// nextView returns views from the pool in round-robin.
func (vp *viewPool) nextView() view.View {
	i := vp.idx.Add(1) - 1
	l := len(vp.views)

	return vp.views[i%int64(l)]
}

// BenchmarkLocalTokenValidation measures local token validation throughput.
func BenchmarkLocalTokenValidation(b *testing.B) {
	f := &TokenValidationServiceViewFactory{}
	pool, params := CreateViewsWithProofs(b, *TestRoot, f, defaultNumViews)

	fmt.Println("==========")
	fmt.Println("Params size:")
	fmt.Println("OutputPath:", len(params[0].OutputPath))
	fmt.Println("TokenRequestRaw:", len(params[0].TokenData.TokenRequestRaw))
	fmt.Println("TxId:", len(params[0].TokenData.TxID))
	fmt.Println("==========")

	b.Run(fmt.Sprintf("out-tokens=%din-tokens=%d", params[0].NumOutputs(), params[0].NumInputs()), func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = pool.nextView().Call(nil)
			}
		})
		benchmark.ReportTPS(b)
	})
}
