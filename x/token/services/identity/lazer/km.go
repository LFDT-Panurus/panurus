/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"context"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

const (
	IdentityType       = tdriver.LazerIdentityType
	IdentityTypeString = tdriver.LazerIdentityTypeString

	keystorePrefix = "lazer.sk."
)

// IdentityStoreService gives access to the signer info of the identities this
// key manager generated.
type IdentityStoreService interface {
	// GetSignerInfo returns the signer info bound to the given identity
	GetSignerInfo(ctx context.Context, id []byte) ([]byte, error)
}

// SignerInfo is what is needed to sign under a pseudonym, besides the wallet secret.
type SignerInfo struct {
	// OpeningSK is the opening of nymSK
	OpeningSK []byte `json:"opening_sk"`
	// Pres is the presentation of the pseudonym, it is part of each signature
	Pres []byte `json:"pres"`
}

// KeyManager generates and recognizes post-quantum anonymous identities.
type KeyManager struct {
	*Deserializer

	conf *Config

	// user state, protected by mu: presentation generation mutates it
	mu    sync.Mutex
	user  *anoncreds.User
	usk   []byte
	attrs [][]byte

	identityStore IdentityStoreService
}

// NewKeyManager creates a key manager from the configuration. The wallet secret
// is taken from the configuration (and then stored in the keystore), or loaded
// from the keystore through the SKI of the configuration. A configuration
// without signer yields a remote key manager that can only verify.
func NewKeyManager(conf *Config, keystore idriver.Keystore, identityStore IdentityStoreService) (*KeyManager, error) {
	if conf == nil {
		return nil, errors.New("no lazer config provided")
	}
	if err := conf.validate(nil); err != nil {
		return nil, err
	}
	des, err := NewDeserializer(conf.IPK)
	if err != nil {
		return nil, err
	}
	km := &KeyManager{Deserializer: des, conf: conf, identityStore: identityStore}
	if conf.Signer == nil {
		logger.Debugf("lazer config [%s] has no signer, it will be considered remote", conf.Name)

		return km, nil
	}

	secret := conf.Signer.Sk
	switch {
	case len(secret) != 0:
		ski := secretKeyIdentifier(conf.IPK, secret[:anoncreds.AttrLen])
		if keystore != nil {
			if err := keystore.Put(keystorePrefix+hex.EncodeToString(ski), secret); err != nil {
				return nil, errors.Wrap(err, "failed to store the wallet secret")
			}
		}
		conf.Signer.Ski = ski
	case len(conf.Signer.Ski) != 0 && keystore != nil:
		if err := keystore.Get(keystorePrefix+hex.EncodeToString(conf.Signer.Ski), &secret); err != nil {
			return nil, errors.Wrapf(err, "failed to retrieve the wallet secret with ski [%s]", hex.EncodeToString(conf.Signer.Ski))
		}
	default:
		logger.Debugf("lazer config [%s] has no secret, it will be considered remote", conf.Name)

		return km, nil
	}
	if len(secret) != anoncreds.AttrLen+anoncreds.MaxLenUserState() {
		return nil, errors.New("invalid wallet secret")
	}
	if len(conf.Signer.Cred) == 0 {
		return nil, errors.New("no credential found")
	}

	km.usk = append([]byte(nil), secret[:anoncreds.AttrLen]...)
	km.attrs = attributes(km.usk, conf.Signer.OU, conf.Signer.EnrollmentID, conf.Signer.RevocationHandle, conf.Signer.Role)
	if km.user, err = anoncreds.NewUser(conf.IPK); err != nil {
		return nil, err
	}
	if err := km.user.Import(secret[anoncreds.AttrLen:], km.attrs); err != nil {
		km.user.Clear()

		return nil, errors.Wrap(err, "failed to import the user state")
	}
	if err := km.user.CredVerify(conf.Signer.Cred); err != nil {
		km.user.Clear()

		return nil, errors.Wrap(err, "credential is not cryptographically valid")
	}

	return km, nil
}

// Close releases the C state held by the key manager.
func (k *KeyManager) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.user != nil {
		k.user.Clear()
		k.user = nil
	}
	k.pv.Close()
}

// Config returns the configuration, without secret material.
func (k *KeyManager) Config() *Config { return k.conf }

// IsRemote returns true if the wallet secret is not available locally.
func (k *KeyManager) IsRemote() bool { return k.user == nil }

// Anonymous returns true: identities are pseudonymous.
func (k *KeyManager) Anonymous() bool { return true }

// IdentityType returns the type of the identities.
func (k *KeyManager) IdentityType() idriver.IdentityType { return IdentityType }

// EnrollmentID returns the enrollment id of the wallet.
func (k *KeyManager) EnrollmentID() string {
	if k.conf.Signer == nil {
		return ""
	}

	return k.conf.Signer.EnrollmentID
}

// String returns a string representation of the key manager.
func (k *KeyManager) String() string {
	return fmt.Sprintf("Lazer KeyManager [%s]", utils.Hashable(k.conf.IPK).String())
}

// Identity generates a fresh pseudonym. If auditInfo is passed, the new
// pseudonym commits to the enrollment id and revocation handle with the
// openings of auditInfo, so that nymEID and nymRH are reused.
func (k *KeyManager) Identity(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
	if k.IsRemote() {
		return nil, errors.New("remote key manager cannot generate identities")
	}
	eid, rh := k.conf.Signer.EnrollmentID, k.conf.Signer.RevocationHandle
	attrEID, attrRH := k.attrs[IdxEID], k.attrs[IdxRH]

	var comEID, comRH *anoncreds.Commitment
	var err error
	if len(auditInfo) != 0 {
		ai, err := DeserializeAuditInfo(auditInfo)
		if err != nil {
			return nil, errors.Wrap(err, "failed to deserialize the passed audit info")
		}
		if ai.EID != eid || ai.RH != rh {
			return nil, errors.New("the passed audit info is for another wallet")
		}
		if comEID, err = anoncreds.CommitWithOpening(attrEID, ai.OpeningEID); err != nil {
			return nil, err
		}
		if comRH, err = anoncreds.CommitWithOpening(attrRH, ai.OpeningRH); err != nil {
			comEID.Clear()

			return nil, err
		}
	} else {
		if comEID, err = anoncreds.Commit(attrEID); err != nil {
			return nil, err
		}
		if comRH, err = anoncreds.Commit(attrRH); err != nil {
			comEID.Clear()

			return nil, err
		}
	}
	defer comEID.Clear()
	defer comRH.Clear()
	comSK, err := anoncreds.Commit(k.usk)
	if err != nil {
		return nil, err
	}
	defer comSK.Clear()

	k.mu.Lock()
	var pres []byte
	if k.user != nil {
		pres, err = k.user.PresGen(k.conf.Signer.Cred, nil, []*anoncreds.Commitment{comSK, comEID, comRH}, idxCom, k.pv.ctx)
	} else {
		err = errors.New("key manager closed")
	}
	k.mu.Unlock()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate the presentation")
	}

	sid := &SerializedIdentity{NymSK: comSK.Nym, NymEID: comEID.Nym, NymRH: comRH.Nym}
	id, err := sid.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize the identity")
	}
	ai := &AuditInfo{EID: eid, RH: rh, OpeningEID: comEID.Opening(), OpeningRH: comRH.Opening()}
	aiRaw, err := ai.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize the audit info")
	}
	signerInfo := &SignerInfo{OpeningSK: comSK.Opening(), Pres: pres}
	signerInfoRaw, err := json.Marshal(signerInfo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize the signer info")
	}
	logger.DebugfContext(ctx, "new lazer identity generated with [%s:%s]", eid, utils.Hashable(rh))

	signer := &Signer{usk: k.usk, opening: signerInfo.OpeningSK, pres: pres}

	return &idriver.IdentityDescriptor{
		Identity:   id,
		AuditInfo:  aiRaw,
		Signer:     signer,
		SignerInfo: signerInfoRaw,
		Verifier:   &Verifier{id: sid, pv: k.pv},
	}, nil
}

// DeserializeVerifier returns the verifier of the given pseudonym.
func (k *KeyManager) DeserializeVerifier(ctx context.Context, raw []byte) (tdriver.Verifier, error) {
	return k.Deserializer.DeserializeVerifier(ctx, raw)
}

// DeserializeSigner rebuilds the signer of an identity generated by this key manager.
func (k *KeyManager) DeserializeSigner(ctx context.Context, raw []byte) (tdriver.Signer, error) {
	return k.deserializeSigner(ctx, raw)
}

// DeserializeSignerNoProbe is DeserializeSigner: rebuilding the signer already
// checks that the secrets match the identity, there is no costly probe to skip.
func (k *KeyManager) DeserializeSignerNoProbe(ctx context.Context, raw []byte) (tdriver.Signer, error) {
	return k.deserializeSigner(ctx, raw)
}

// DeserializeSigningIdentity deserializes a signing identity from the given raw bytes.
func (k *KeyManager) DeserializeSigningIdentity(ctx context.Context, raw []byte) (tdriver.SigningIdentity, error) {
	s, err := k.deserializeSigner(ctx, raw)
	if err != nil {
		return nil, err
	}

	return &signingIdentity{Signer: s, id: raw}, nil
}

func (k *KeyManager) deserializeSigner(ctx context.Context, raw []byte) (*Signer, error) {
	if k.IsRemote() {
		return nil, errors.New("no local wallet secret for this key manager")
	}
	if k.identityStore == nil {
		return nil, errors.New("no identity store available")
	}
	id, err := UnmarshalIdentity(raw)
	if err != nil {
		return nil, err
	}
	infoRaw, err := k.identityStore.GetSignerInfo(ctx, raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve the signer info")
	}
	if len(infoRaw) == 0 {
		return nil, errors.Errorf("no signer info found for identity [%s]", tdriver.Identity(raw).UniqueID())
	}
	info := &SignerInfo{}
	dec := json.NewDecoder(bytes.NewReader(infoRaw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(info); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal the signer info")
	}
	if len(info.Pres) == 0 || len(info.Pres) > anoncreds.MaxLenPres() {
		return nil, errors.New("invalid presentation in the signer info")
	}
	// the secrets must open nymSK: this is what ties the identity to this wallet
	com, err := anoncreds.CommitWithOpening(k.usk, info.OpeningSK)
	if err != nil {
		return nil, errors.Wrap(err, "invalid signer info")
	}
	defer com.Clear()
	if string(com.Nym) != string(id.NymSK) {
		return nil, errors.New("the identity does not belong to this key manager")
	}

	return &Signer{usk: k.usk, opening: info.OpeningSK, pres: info.Pres}, nil
}

// Info returns a string that includes the given identity. If auditInfo is
// provided, the identity is verified against it and the enrollment id is added.
func (k *KeyManager) Info(ctx context.Context, raw []byte, auditInfo []byte) (string, error) {
	return k.Deserializer.Info(ctx, raw, auditInfo)
}

// Signer signs under a pseudonym.
type Signer struct {
	usk     []byte
	opening []byte
	pres    []byte
}

var _ tdriver.Signer = (*Signer)(nil)

// Sign signs message.
func (s *Signer) Sign(message []byte) ([]byte, error) {
	// the C commitment is rebuilt and released here, nothing C-owned outlives the call
	com, err := anoncreds.CommitWithOpening(s.usk, s.opening)
	if err != nil {
		return nil, err
	}
	defer com.Clear()
	sok, err := com.SokProve(signatureContext(message), s.usk)
	if err != nil {
		return nil, err
	}

	return asn1.Marshal(Signature{Pres: s.pres, Sok: sok})
}

type signingIdentity struct {
	*Signer
	id []byte
}

// Serialize returns the serialized identity.
func (s *signingIdentity) Serialize() ([]byte, error) { return s.id, nil }
