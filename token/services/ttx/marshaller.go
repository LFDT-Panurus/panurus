/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/asn1"
	"sort"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.uber.org/zap/zapcore"
)

var (
	ErrNetworkNotSet   = errors.New("network not set")
	ErrNamespaceNotSet = errors.New("namespace not set")
)

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func MarshalMeta(v map[string][]byte) ([]byte, error) {
	metaSer := metaSer{
		Keys: make([]string, len(v)),
		Vals: make([][]byte, len(v)),
	}

	i := 0
	for k := range v {
		metaSer.Keys[i] = k
		i++
	}
	i = 0
	sort.Strings(metaSer.Keys)
	for _, key := range metaSer.Keys {
		metaSer.Vals[i] = v[key]
		i++
	}

	return asn1.Marshal(metaSer)
}

func UnmarshalMeta(raw []byte) (map[string][]byte, error) {
	var metaSer metaSer
	_, err := asn1.Unmarshal(raw, &metaSer)
	if err != nil {
		return nil, err
	}
	v := make(map[string][]byte, len(metaSer.Keys))
	for i, k := range metaSer.Keys {
		v[k] = metaSer.Vals[i]
	}

	return v, nil
}

type metaSer struct {
	Keys []string
	Vals [][]byte
}

type GetNetworkFunc = func(network string, channel string) (dep.Network, error)

type TransactionSer struct {
	Nonce        []byte
	Creator      []byte
	ID           string
	Network      string
	Channel      string
	Namespace    string
	Signer       []byte
	Transient    []byte
	TokenRequest []byte
	Envelope     []byte
}

func marshal(t *Transaction, eIDs ...string) ([]byte, error) {
	// sanity checks
	if len(t.Network()) == 0 {
		return nil, ErrNetworkNotSet
	}
	if len(t.Namespace()) == 0 {
		return nil, ErrNamespaceNotSet
	}

	var err error

	var transientRaw []byte
	if len(t.Transient) != 0 {
		transientRaw, err = MarshalMeta(t.Transient)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal transient")
		}
	}

	var tokenRequestRaw []byte
	if t.TokenRequest != nil {
		req := t.TokenRequest
		// If eIDs are specified, we only marshal the metadata for the passed eIDs
		if len(eIDs) != 0 {
			req, err = t.TokenRequest.FilterMetadataBy(t.Context, eIDs...)
			if err != nil {
				return nil, errors.Wrap(err, "failed to filter metadata")
			}
		}
		tokenRequestRaw, err = req.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal token request")
		}
	}

	var envRaw []byte
	if t.Envelope != nil {
		envRaw, err = t.Envelope.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal envelope")
		}
		if logger.IsEnabledFor(zapcore.DebugLevel) {
			logger.Debugf("transaction envelope [%s]", utils.Hashable(t.Envelope.String()))
		}
	}

	res, err := asn1.Marshal(TransactionSer{
		Nonce:        t.TxID.Nonce,
		Creator:      t.TxID.Creator,
		ID:           t.Payload.ID,
		Network:      t.tmsID.Network,
		Channel:      t.tmsID.Channel,
		Namespace:    t.tmsID.Namespace,
		Signer:       t.Signer,
		Transient:    transientRaw,
		TokenRequest: tokenRequestRaw,
		Envelope:     envRaw,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal transaction")
	}

	return res, nil
}

func unmarshal(getNetwork GetNetworkFunc, p *Payload, raw []byte) error {
	var ser TransactionSer
	if _, err := asn1.Unmarshal(raw, &ser); err != nil {
		return errors.Wrapf(err, "failed unmarshalling transaction [%s]", string(raw))
	}
	// sanity checks
	if len(ser.Network) == 0 {
		return ErrNetworkNotSet
	}
	if len(ser.Namespace) == 0 {
		return ErrNamespaceNotSet
	}

	p.TxID.Nonce = ser.Nonce
	p.TxID.Creator = ser.Creator
	p.ID = ser.ID
	p.tmsID = token.TMSID{
		Network:   ser.Network,
		Channel:   ser.Channel,
		Namespace: ser.Namespace,
	}
	p.Signer = ser.Signer
	p.Transient = make(map[string][]byte)
	if len(ser.Transient) != 0 {
		meta, err := UnmarshalMeta(ser.Transient)
		if err != nil {
			return errors.Wrap(err, "failed unmarshalling transient")
		}
		p.Transient = meta
	}
	if len(ser.TokenRequest) != 0 {
		if err := p.TokenRequest.FromBytes(ser.TokenRequest); err != nil {
			return errors.Wrap(err, "failed unmarshalling token request")
		}
	}
	if p.Envelope == nil {
		nws, err := getNetwork(p.tmsID.Network, p.tmsID.Channel)
		if err != nil {
			return err
		}
		p.Envelope = nws.NewEnvelope()
	}
	if len(ser.Envelope) != 0 {
		if err := p.Envelope.FromBytes(ser.Envelope); err != nil {
			return errors.Wrapf(err, "failed unmarshalling envelope [%d]", len(ser.Envelope))
		}
	}

	return nil
}
