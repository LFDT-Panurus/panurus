/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"math/big"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// transferGas is the gas a plain value transfer costs. It is fixed by the protocol, so there is
// nothing to estimate.
const transferGas = 21_000

// DefaultFunding is what each node's submitter account is given: far more than a test run spends, and
// small enough that every node on a dev network can be funded from one pre-funded account.
var DefaultFunding = new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)) // 10 ether

// fundingReceiptTimeout bounds the wait for a funding transfer to be mined. Instant mining makes this
// immediate in practice; the wait exists so that an unfunded account surfaces here rather than as a
// node failing to broadcast much later, with nothing pointing at its balance.
const fundingReceiptTimeout = 30 * time.Second

// Funder pays fresh accounts out of one pre-funded account.
//
// Every node that broadcasts needs its own account. Sharing one is not a simplification, it is a bug
// with a delay on it: Ethereum nonces are per account and each node tracks its own locally, so two
// nodes on one account hand out the same nonce and whichever sends second is rejected with "nonce too
// low". It stays hidden for as long as only one node happens to be broadcasting.
//
// Funding is sequential on purpose. The funder's own nonces have to be consecutive, and reading the
// pending nonce once and incrementing is both cheaper and more predictable than asking the node
// between every transfer.
type Funder struct {
	client  evmclient.EVMClient
	key     *secp256k1.PrivateKey
	from    evmclient.Address
	chainID *big.Int

	nonce       uint64
	nonceLoaded bool
}

// NewFunder returns a funder paying from the account key belongs to.
func NewFunder(evmClient evmclient.EVMClient, key *secp256k1.PrivateKey, from evmclient.Address, chainID *big.Int) (*Funder, error) {
	if evmClient == nil {
		return nil, errors.New("evm nwo: funder needs a client")
	}
	if key == nil {
		return nil, errors.New("evm nwo: funder needs a key")
	}
	if chainID == nil {
		return nil, errors.New("evm nwo: funder needs a chain id")
	}

	return &Funder{client: evmClient, key: key, from: from, chainID: chainID}, nil
}

// Fund sends amount to the given address and waits for the transfer to be mined.
func (f *Funder) Fund(ctx context.Context, to evmclient.Address, amount *big.Int) error {
	if !f.nonceLoaded {
		nonce, err := f.client.PendingNonceAt(ctx, f.from)
		if err != nil {
			return errors.Wrapf(err, "evm nwo: failed to read the nonce of the funding account [%s]", f.from)
		}
		f.nonce, f.nonceLoaded = nonce, true
	}

	fees, err := f.client.SuggestGasFees(ctx)
	if err != nil {
		return errors.Wrap(err, "evm nwo: failed to get gas fees for a funding transfer")
	}

	tx := &evmclient.DynamicFeeTx{
		ChainID:              f.chainID,
		Nonce:                f.nonce,
		MaxPriorityFeePerGas: fees.MaxPriorityFeePerGas,
		MaxFeePerGas:         fees.MaxFeePerGas,
		Gas:                  transferGas,
		To:                   &to,
		Value:                amount,
	}
	raw, err := evmclient.SignTx(tx, f.key)
	if err != nil {
		return errors.Wrap(err, "evm nwo: failed to sign a funding transfer")
	}
	hash, err := f.client.SendRawTransaction(ctx, raw)
	if err != nil {
		return errors.Wrapf(err, "evm nwo: failed to send a funding transfer to [%s]", to)
	}
	f.nonce++

	return f.await(ctx, hash, to)
}

// await waits for the transfer's receipt and checks it succeeded. A transfer that reverts leaves an
// account that looks configured and cannot pay, so it is worth the wait to fail here instead.
func (f *Funder) await(ctx context.Context, hash evmclient.Hash, to evmclient.Address) error {
	ctx, cancel := context.WithTimeout(ctx, fundingReceiptTimeout)
	defer cancel()

	for {
		receipt, err := f.client.GetTransactionReceipt(ctx, hash)
		if err != nil {
			return errors.Wrapf(err, "evm nwo: failed to read the receipt of the funding transfer to [%s]", to)
		}
		if receipt != nil {
			if receipt.Status != 1 {
				return errors.Errorf("evm nwo: the funding transfer to [%s] reverted", to)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			return errors.Errorf("evm nwo: the funding transfer to [%s] was not mined in %s", to, fundingReceiptTimeout)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// FundedIdentity generates a fresh identity under dir and funds it, so the node it belongs to can pay
// for its own transactions with a nonce sequence nobody else touches.
func (f *Funder) FundedIdentity(ctx context.Context, dir, name string, amount *big.Int) (Identity, error) {
	identity, err := GenerateIdentity(dir, name)
	if err != nil {
		return Identity{}, err
	}
	if err := f.Fund(ctx, identity.Address, amount); err != nil {
		return Identity{}, err
	}

	return identity, nil
}
