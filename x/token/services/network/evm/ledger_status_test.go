/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

const (
	statusTokenStateA = "0x00000000000000000000000000000000000000a1"
	statusTokenStateB = "0x00000000000000000000000000000000000000b1"
)

// twoTMSNetwork binds two TMS, each with its own TokenState, on one network. answer gives what each
// contract's getTokenRequestHash returns.
func twoTMSNetwork(t *testing.T, answer map[string]func() ([]byte, error)) *Network {
	t.Helper()
	evm := &mock.EVMClient{}
	evm.CallStub = func(_ context.Context, to client.Address, _ []byte, _ string) ([]byte, error) {
		return answer[to.Hex()]()
	}

	namespaces := make([]NamespaceConfig, 0, 2)
	for ns, tokenState := range map[string]string{"a": statusTokenStateA, "b": statusTokenStateB} {
		cfg := validConfig()
		cfg.Contracts.TokenState = tokenState
		cfg.applyDefaults()
		require.NoError(t, cfg.Validate())
		namespaces = append(namespaces, NamespaceConfig{Namespace: ns, Config: cfg})
	}
	n, err := NewNetwork("evm-net", evm, namespaces, nil, nil)
	require.NoError(t, err)

	return n
}

func hexOf(t *testing.T, address string) string {
	t.Helper()
	a, err := client.HexToAddress(address)
	require.NoError(t, err)

	return a.Hex()
}

// TestLedgerStatusWithSeveralTMS checks that Status, which has no namespace to go by, still answers
// when more than one TMS shares the network. The vault's transaction drift check calls it with only a
// txID; refusing made it report every confirmed transaction as unknown to the ledger.
func TestLedgerStatusWithSeveralTMS(t *testing.T) {
	applied := func() ([]byte, error) {
		h := sha256Of("request")

		return h[:], nil
	}
	absent := func() ([]byte, error) { return make([]byte, 32), nil }
	broken := func() ([]byte, error) { return nil, errors.New("connection refused") }

	status := func(t *testing.T, a, b func() ([]byte, error)) (driver.ValidationCode, error) {
		t.Helper()
		n := twoTMSNetwork(t, map[string]func() ([]byte, error){
			hexOf(t, statusTokenStateA): a,
			hexOf(t, statusTokenStateB): b,
		})
		ledger, err := n.Ledger()
		require.NoError(t, err)

		return ledger.Status(anchorHex(0x01))
	}

	t.Run("found in whichever TokenState applied it", func(t *testing.T) {
		code, err := status(t, absent, applied)
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, code)

		code, err = status(t, applied, absent)
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, code)
	})

	t.Run("unknown when no TokenState has it", func(t *testing.T) {
		code, err := status(t, absent, absent)
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, code)
	})

	t.Run("a failed read is not reported as unknown", func(t *testing.T) {
		_, err := status(t, absent, broken)
		require.Error(t, err, "the anchor may be in the contract that could not be read")
	})

	t.Run("a failed read elsewhere does not hide a found anchor", func(t *testing.T) {
		code, err := status(t, broken, applied)
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, code)
	})

	t.Run("the TokenStates are asked at the same time", func(t *testing.T) {
		// Each read waits until the other has started: asked one after the other, neither returns.
		var started sync.WaitGroup
		started.Add(2)
		waitForBoth := func(answer func() ([]byte, error)) func() ([]byte, error) {
			return func() ([]byte, error) {
				started.Done()
				started.Wait()

				return answer()
			}
		}

		n := twoTMSNetwork(t, map[string]func() ([]byte, error){
			hexOf(t, statusTokenStateA): waitForBoth(absent),
			hexOf(t, statusTokenStateB): waitForBoth(applied),
		})
		ledger, err := n.Ledger()
		require.NoError(t, err)

		done := make(chan driver.ValidationCode, 1)
		go func() {
			code, err := ledger.Status(anchorHex(0x01))
			assert.NoError(t, err)
			done <- code
		}()
		select {
		case code := <-done:
			assert.Equal(t, driver.Valid, code)
		case <-time.After(5 * time.Second):
			t.Fatal("Status read the TokenStates one after the other")
		}
	})

	t.Run("a malformed id is rejected", func(t *testing.T) {
		n := twoTMSNetwork(t, nil)
		ledger, err := n.Ledger()
		require.NoError(t, err)
		_, err = ledger.Status("not-an-anchor")
		require.Error(t, err)
	})
}
