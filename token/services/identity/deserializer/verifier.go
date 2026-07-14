/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deserializer

import (
	"context"
	errors2 "errors"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

type TypedVerifierDeserializer = idriver.TypedVerifierDeserializer

type TypedVerifierDeserializerMultiplex struct {
	deserializers map[idriver.IdentityType][]idriver.TypedVerifierDeserializer
}

func NewTypedVerifierDeserializerMultiplex() *TypedVerifierDeserializerMultiplex {
	return &TypedVerifierDeserializerMultiplex{deserializers: map[idriver.IdentityType][]TypedVerifierDeserializer{}}
}

func (v *TypedVerifierDeserializerMultiplex) AddTypedVerifierDeserializer(typ idriver.IdentityType, d TypedVerifierDeserializer) {
	_, ok := v.deserializers[typ]
	if !ok {
		v.deserializers[typ] = []TypedVerifierDeserializer{d}

		return
	}
	v.deserializers[typ] = append(v.deserializers[typ], d)
}

func (v *TypedVerifierDeserializerMultiplex) DeserializeVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	si, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}
	dess, ok := v.deserializers[si.Type]
	if !ok {
		return nil, errors.Errorf("no deserializer found for [%v]", si.Type)
	}
	logger.DebugfContext(ctx, "deserializing [%s] with type [%v]", id, si.Type)
	var errs []error
	for _, deserializer := range dess {
		verifier, err := deserializer.DeserializeVerifier(ctx, si.Type, si.Identity)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return verifier, nil
	}

	return nil, errors.Wrapf(errors2.Join(errs...), "failed to deserialize verifier for [%v]", si.Type)
}

func (v *TypedVerifierDeserializerMultiplex) Recipients(id driver.Identity) ([]driver.Identity, error) {
	if id.IsNone() {
		return nil, nil
	}
	si, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}
	dess, ok := v.deserializers[si.Type]
	if !ok {
		return nil, errors.Errorf("no deserializer found for [%v]", si.Type)
	}

	var errs []error
	for _, deserializer := range dess {
		ids, err := deserializer.Recipients(id, si.Type, si.Identity)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return ids, nil
	}

	return nil, errors.Wrapf(errors2.Join(errs...), "failed to deserializer recipients for [%s]", si.Type)
}

func (v *TypedVerifierDeserializerMultiplex) GetAuditInfoMatcher(ctx context.Context, id driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	if id.IsNone() {
		return nil, nil
	}
	si, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}
	matcher, err := v.getMatcher(ctx, si.Type, id, auditInfo)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting matcher for [%s]", si.Type)
	}

	return &TypedAuditInfoMatcher{matcher: matcher}, nil
}

func (v *TypedVerifierDeserializerMultiplex) getMatcher(ctx context.Context, idType idriver.IdentityType, id driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	dess, ok := v.deserializers[idType]
	if !ok {
		return nil, errors.Errorf("no deserializer found for [%v]", idType)
	}

	var errs []error
	for _, deserializer := range dess {
		matcher, err := deserializer.GetAuditInfoMatcher(ctx, id, auditInfo)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return matcher, nil
	}

	return nil, errors.Wrapf(errors2.Join(errs...), "failed to find a valid owner matcher for [%s]", idType)
}

func (v *TypedVerifierDeserializerMultiplex) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	// match identity and audit info
	recipient, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return errors.Wrapf(err, "failed to unmarshal identity [%s]", id)
	}
	matcher, err := v.getMatcher(ctx, recipient.Type, id, ai)
	if err != nil {
		return errors.Wrapf(err, "failed getting audit info matcher for [%s]", id)
	}
	err = matcher.Match(ctx, recipient.Identity)
	if err != nil {
		return errors.Wrapf(err, "failed to match identity to audit infor for [%s]:[%s]", id, utils.Hashable(ai))
	}

	return nil
}

func (v *TypedVerifierDeserializerMultiplex) GetAuditInfo(ctx context.Context, id driver.Identity, p driver.AuditInfoProvider) ([]byte, error) {
	si, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}
	dess, ok := v.deserializers[si.Type]
	if !ok {
		return nil, errors.Errorf("no deserializer found for [%v]", si.Type)
	}
	var errs []error
	for _, deserializer := range dess {
		info, err := deserializer.GetAuditInfo(ctx, id, si.Type, si.Identity, p)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		return info, nil
	}

	return nil, errors.Wrapf(errors2.Join(errs...), "failed to find a valid deserializer for audit info for [%s]", si.Type)
}

type TypedIdentityVerifierDeserializer struct {
	driver.VerifierDeserializer
	driver.MatcherDeserializer
}

func NewTypedIdentityVerifierDeserializer(verifierDeserializer driver.VerifierDeserializer, matcherDeserializer driver.MatcherDeserializer) *TypedIdentityVerifierDeserializer {
	return &TypedIdentityVerifierDeserializer{VerifierDeserializer: verifierDeserializer, MatcherDeserializer: matcherDeserializer}
}

func (t *TypedIdentityVerifierDeserializer) DeserializeVerifier(ctx context.Context, typ identity.Type, raw []byte) (driver.Verifier, error) {
	return t.VerifierDeserializer.DeserializeVerifier(ctx, raw)
}

func (t *TypedIdentityVerifierDeserializer) Recipients(id driver.Identity, typ identity.Type, raw []byte) ([]driver.Identity, error) {
	return []driver.Identity{id}, nil
}

func (t *TypedIdentityVerifierDeserializer) GetAuditInfo(ctx context.Context, id driver.Identity, typ identity.Type, raw []byte, p driver.AuditInfoProvider) ([]byte, error) {
	auditInfo, err := p.GetAuditInfo(ctx, id)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting audit info for recipient identity [%s]", id)
	}

	return auditInfo, nil
}

func (t *TypedIdentityVerifierDeserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return t.MatcherDeserializer.GetAuditInfoMatcher(ctx, owner, auditInfo)
}
