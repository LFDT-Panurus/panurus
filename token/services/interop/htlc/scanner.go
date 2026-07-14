/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"bytes"
	"crypto"
	"encoding/base64"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	ScanForPreImageStartingTransaction = "htlc.ScanForPreImage.StartingTransaction"
	StopScanningOnLastTransaction      = "htlc.ScanForPreImage.StopOnLastTransaction"
	True                               = "true"
)

// WithStartingTransaction sets the network name
func WithStartingTransaction(txID string) token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = map[string]any{}
		}
		o.Params[ScanForPreImageStartingTransaction] = txID

		return nil
	}
}

// WithStopOnLastTransaction stops the scan when the last transaction is reached.
// When this is not set, the scan will wait until timeout or until the key is found.
func WithStopOnLastTransaction() token.ServiceOption {
	return func(o *token.ServiceOptions) error {
		if o.Params == nil {
			o.Params = map[string]any{}
		}
		o.Params[StopScanningOnLastTransaction] = True

		return nil
	}
}

// ScanForPreImage scans the ledger for a preimage of the passed image, taking into account the timeout
func ScanForPreImage(sp token.ServiceProvider, image []byte, hashFunc crypto.Hash, hashEncoding encoding.Encoding, timeout time.Duration, opts ...token.ServiceOption) ([]byte, error) {
	logger.Debugf("scanning for preimage of [%s] with timeout [%s]", base64.StdEncoding.EncodeToString(image), timeout)

	if !hashFunc.Available() {
		return nil, errors.Errorf("passed hash function is not available [%d]", hashFunc)
	}
	if !hashEncoding.Available() {
		return nil, errors.Errorf("passed hash endcoding is not available [%d]", hashEncoding)
	}

	tms, err := token.GetManagementService(sp, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get management service")
	}
	network := network.GetInstance(sp, tms.Network(), tms.Channel())
	if network == nil {
		return nil, errors.Errorf("cannot find network [%s:%s]", tms.Namespace(), tms.Channel())
	}

	claimKey := ClaimKey(image)
	preImage, err := network.LookupTransferMetadataKey(tms.Namespace(), claimKey, timeout, opts...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to lookup key [%s]", claimKey)
	}
	recomputedImage, err := (&HashInfo{
		HashFunc:     hashFunc,
		HashEncoding: hashEncoding,
	}).Image(preImage)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to compute image of [%x]", preImage)
	}
	if !bytes.Equal(image, recomputedImage) {
		return nil, errors.Errorf("pre-image on the ledger does not match the passed image [%x!=%x]", image, recomputedImage)
	}

	return preImage, nil
}
