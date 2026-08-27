/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/asn1"
	"sort"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/network"
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
	rest, err := asn1.Unmarshal(raw, &metaSer)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.Errorf("invalid transient metadata: trailing data [%d] bytes", len(rest))
	}
	if len(metaSer.Keys) != len(metaSer.Vals) {
		return nil, errors.Errorf(
			"invalid transient metadata: key/value count mismatch [%d]!=[%d]",
			len(metaSer.Keys),
			len(metaSer.Vals),
		)
	}
	v := make(map[string][]byte, len(metaSer.Keys))
	for i, k := range metaSer.Keys {
		if _, ok := v[k]; ok {
			return nil, errors.Errorf("invalid transient metadata: duplicate key [%s]", k)
		}
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

func marshal(ctx context.Context, t *Transaction, eIDs ...string) ([]byte, error) {
	// sanity checks
	if len(t.Network()) == 0 {
		return nil, ErrNetworkNotSet
	}
	if len(t.Namespace()) == 0 {
		return nil, ErrNamespaceNotSet
	}

	transientRaw, err := marshalTransient(t.Transient)
	if err != nil {
		return nil, err
	}

	tokenRequestRaw, err := marshalTokenRequest(ctx, t.TokenRequest, eIDs)
	if err != nil {
		return nil, err
	}

	envRaw, err := marshalEnvelope(t.Envelope)
	if err != nil {
		return nil, err
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

// marshalTransient serializes transient, or returns nil if it's empty.
func marshalTransient(transient network.TransientMap) ([]byte, error) {
	if len(transient) == 0 {
		return nil, nil
	}

	raw, err := MarshalMeta(transient)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal transient")
	}

	return raw, nil
}

// marshalTokenRequest serializes req, or returns nil if it's nil. If eIDs are
// specified, only the metadata for those eIDs is marshaled.
func marshalTokenRequest(ctx context.Context, req *token.Request, eIDs []string) ([]byte, error) {
	if req == nil {
		return nil, nil
	}

	if len(eIDs) != 0 {
		var err error
		req, err = req.FilterMetadataBy(ctx, eIDs...)
		if err != nil {
			return nil, errors.Wrap(err, "failed to filter metadata")
		}
	}

	raw, err := req.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal token request")
	}

	return raw, nil
}

// marshalEnvelope serializes env, or returns nil if it's nil.
func marshalEnvelope(env *network.Envelope) ([]byte, error) {
	if env == nil {
		return nil, nil
	}

	raw, err := env.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal envelope")
	}
	if logger.IsEnabledFor(zapcore.DebugLevel) {
		logger.Debugf("transaction envelope [%s]", utils.Hashable(env.String()))
	}

	return raw, nil
}

func unmarshal(getNetwork GetNetworkFunc, p *Payload, raw []byte) error {
	var ser TransactionSer
	rest, err := asn1.Unmarshal(raw, &ser)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling transaction")
	}
	if len(rest) != 0 {
		return errors.Errorf("failed unmarshalling transaction: trailing data [%d] bytes", len(rest))
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

	if err := unmarshalTransientInto(p, ser.Transient); err != nil {
		return err
	}
	if len(ser.TokenRequest) != 0 {
		if err := p.TokenRequest.FromBytes(ser.TokenRequest); err != nil {
			return errors.Wrap(err, "failed unmarshalling token request")
		}
	}

	return unmarshalEnvelopeInto(getNetwork, p, ser.Envelope)
}

// unmarshalTransientInto populates p.Transient from raw, defaulting to an
// empty (non-nil) map when raw is empty.
func unmarshalTransientInto(p *Payload, raw []byte) error {
	p.Transient = make(map[string][]byte)
	if len(raw) == 0 {
		return nil
	}

	meta, err := UnmarshalMeta(raw)
	if err != nil {
		return errors.Wrap(err, "failed unmarshalling transient")
	}
	p.Transient = meta

	return nil
}

// unmarshalEnvelopeInto ensures p.Envelope exists (building a fresh one for
// p's network/channel if it's still nil), then unmarshals raw into it if raw
// carries any bytes.
func unmarshalEnvelopeInto(getNetwork GetNetworkFunc, p *Payload, raw []byte) error {
	if p.Envelope == nil {
		nws, err := getNetwork(p.tmsID.Network, p.tmsID.Channel)
		if err != nil {
			return err
		}
		p.Envelope = nws.NewEnvelope()
	}
	if len(raw) == 0 {
		return nil
	}

	if err := p.Envelope.FromBytes(raw); err != nil {
		return errors.Wrapf(err, "failed unmarshalling envelope [%d]", len(raw))
	}

	return nil
}
