/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
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

// SignerInfoVersionV2 is the only supported version of SignerInfo.
const SignerInfoVersionV2 = 2

// SignerInfo is what is needed to sign under a pseudonym, besides the wallet
// secret. It is kept by the identity store, which is not a secret store.
//
// The opening of nymSK is encrypted: together with nymSK it reveals usk (nymSK
// is linear in usk and its opening), that is, it lets its holder sign under
// every pseudonym of the wallet. EncOpeningSK is nonce || AES-256-GCM(opening),
// with a key derived from the wallet secret (see signerInfoKey) and, as
// additional data, a hash of the identity, NymRH and Pres (see
// signerInfoAD), so that a signer info altered in the store is refused
// instead of producing signatures that do not verify. nymSK is not stored: it
// is recomputed from usk and the opening.
type SignerInfo struct {
	// Version is SignerInfoVersionV2
	Version int `json:"version"`
	// EncOpeningSK is the encrypted opening of nymSK
	EncOpeningSK []byte `json:"enc_opening_sk"`
	// NymRH is the commitment to the revocation handle, part of each signature
	NymRH []byte `json:"nym_rh"`
	// Pres is the presentation of the pseudonym, part of each signature
	Pres []byte `json:"pres"`
}

const (
	// gcmNonceLen is the length of the AES-GCM nonce prefixed to an encrypted opening.
	gcmNonceLen = 12
	// gcmTagLen is the length of the AES-GCM tag of an encrypted opening.
	gcmTagLen = 16
)

// maxSignerInfoLen bounds the size of a signer info before parsing it.
const maxSignerInfoLen = 1 << 17

// decodeSignerInfo parses a signer info strictly: the input must be the
// encoding json.Marshal produces, and the version and lengths must be valid.
func decodeSignerInfo(raw []byte) (*SignerInfo, error) {
	if len(raw) == 0 || len(raw) > maxSignerInfoLen {
		return nil, errors.Errorf("invalid signer info length [%d]", len(raw))
	}
	info := &SignerInfo{}
	if err := canonicalUnmarshal(raw, info); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal the signer info")
	}
	if info.Version != SignerInfoVersionV2 {
		return nil, errors.Errorf("unsupported signer info version [%d]", info.Version)
	}
	if len(info.EncOpeningSK) != gcmNonceLen+anoncreds.MaxLenOpening()+gcmTagLen {
		return nil, errors.New("invalid encrypted opening in the signer info")
	}
	if len(info.NymRH) != maxNymLen {
		return nil, errors.New("invalid nymRH in the signer info")
	}
	if len(info.Pres) == 0 || len(info.Pres) > anoncreds.MaxLenPres() {
		return nil, errors.New("invalid presentation in the signer info")
	}

	return info, nil
}

// signerInfoAD is the additional data of the encrypted opening of the signer
// info of the identity id. id and nymRH have a fixed length.
func signerInfoAD(id, nymRH, pres []byte) []byte {
	h := sha256.New()
	h.Write([]byte(signerInfoADDomain))
	h.Write(id)
	h.Write(nymRH)
	h.Write(pres)

	return h.Sum(nil)
}

// signerInfoAEAD returns the AEAD that protects the openings at rest.
func signerInfoAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create the signer info cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create the signer info cipher")
	}

	return aead, nil
}

// sealOpening encrypts an opening under key with the additional data ad.
func sealOpening(key, ad, opening []byte) ([]byte, error) {
	aead, err := signerInfoAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcmNonceLen, gcmNonceLen+len(opening)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.Wrap(err, "failed to sample the nonce")
	}

	return aead.Seal(nonce, nonce, opening, ad), nil
}

// openOpening decrypts an opening sealed by sealOpening.
func openOpening(key, ad, enc []byte) ([]byte, error) {
	aead, err := signerInfoAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(enc) < gcmNonceLen {
		return nil, errors.New("invalid encrypted opening")
	}
	opening, err := aead.Open(nil, enc[:gcmNonceLen], enc[gcmNonceLen:], ad)
	if err != nil {
		return nil, errors.New("failed to decrypt the opening: it is not of this wallet, or not of this identity")
	}

	return opening, nil
}

// KeyManager generates and recognizes post-quantum anonymous identities.
type KeyManager struct {
	*Deserializer

	conf *Config

	// remote is set at construction and never changes: the wallet secret is not available
	remote bool
	// usk, attrs, siKey and aiKey are set at construction and never change
	usk   []byte
	attrs [][]byte
	// siKey is the key that encrypts the signer infos at rest
	siKey []byte
	// aiKey is the key that encrypts the opening of nymEID in the audit infos
	aiKey []byte

	// user generates the presentations, nil for a remote key manager. It is
	// safe for concurrent presentations, limiter bounds how many run at once.
	user    *anoncreds.User
	limiter *ProverLimiter
	// done is closed by Close, releasing the calls waiting for the limiter
	done chan struct{}

	// mu protects closed
	mu     sync.Mutex
	closed bool

	identityStore IdentityStoreService
}

// KeyManagerOption configures a key manager.
type KeyManagerOption func(*keyManagerOptions)

type keyManagerOptions struct {
	limiter *ProverLimiter
}

// WithProverLimiter makes the key manager share limiter with other key
// managers, bounding the presentations generated at the same time by all of
// them. By default each key manager has its own limiter of DefaultProverLimit.
func WithProverLimiter(limiter *ProverLimiter) KeyManagerOption {
	return func(o *keyManagerOptions) { o.limiter = limiter }
}

// NewKeyManager creates a key manager from the configuration. The wallet secret
// is taken from the configuration (and then stored in the keystore), or loaded
// from the keystore through the SKI of the configuration. A configuration
// without signer yields a remote key manager that can only verify.
func NewKeyManager(conf *Config, keystore idriver.Keystore, identityStore IdentityStoreService, opts ...KeyManagerOption) (*KeyManager, error) {
	options := keyManagerOptions{}
	for _, opt := range opts {
		opt(&options)
	}
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
	km := &KeyManager{Deserializer: des, conf: conf, identityStore: identityStore, remote: true, done: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			des.Close()
		}
	}()
	if conf.Signer == nil {
		logger.Debugf("lazer config [%s] has no signer, it will be considered remote", conf.Name)
		ok = true

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
		ok = true

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
	if km.siKey, err = signerInfoKey(conf.IPK, secret); err != nil {
		return nil, errors.Wrap(err, "failed to derive the signer info key")
	}
	if km.aiKey, err = auditKey(conf.IPK, secret); err != nil {
		return nil, errors.Wrap(err, "failed to derive the audit info key")
	}
	user, err := anoncreds.NewUser(conf.IPK)
	if err != nil {
		return nil, err
	}
	if err := user.Import(secret[anoncreds.AttrLen:], km.attrs); err != nil {
		user.Clear()

		return nil, errors.Wrap(err, "failed to import the user state")
	}
	if err := user.CredVerify(conf.Signer.Cred); err != nil {
		user.Clear()

		return nil, errors.Wrap(err, "credential is not cryptographically valid")
	}
	km.user = user
	km.limiter = options.limiter
	if km.limiter == nil {
		km.limiter = NewProverLimiter(0)
	}
	km.remote = false
	ok = true

	return km, nil
}

// Close releases the C state held by the key manager. It is idempotent and
// safe to call concurrently with the other methods; afterwards Identity and
// the signer deserializers fail, and so do verifications. Identity calls
// waiting for the limiter fail; Close waits for all the presentations in
// progress to complete. Signers already returned keep working: they own no C
// state.
func (k *KeyManager) Close() {
	k.mu.Lock()
	first := !k.closed
	k.closed = true
	k.mu.Unlock()
	if first && k.done != nil {
		close(k.done)
	}
	// waits for the presentations in progress
	k.user.Clear()
	k.pv.Close()
}

// isClosed reports whether Close was called.
func (k *KeyManager) isClosed() bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.closed
}

// Config returns the configuration, without secret material.
func (k *KeyManager) Config() *Config { return k.conf }

// IsRemote returns true if the wallet secret is not available locally. It does
// not change over the lifetime of the key manager, Close included.
func (k *KeyManager) IsRemote() bool { return k.remote }

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

// Identity generates a fresh pseudonym, the commitment nymEID to the
// enrollment id. If auditInfo is passed, it must be an audit info of this
// wallet: the new pseudonym reuses its openings of nymEID and nymRH, so it is
// the same nymEID (as in idemixnym), with a fresh nymSK and presentation.
func (k *KeyManager) Identity(ctx context.Context, auditInfo []byte) (*idriver.IdentityDescriptor, error) {
	if k.IsRemote() {
		return nil, errors.New("remote key manager cannot generate identities")
	}
	if k.isClosed() {
		return nil, errClosed
	}
	eid, rh := k.conf.Signer.EnrollmentID, k.conf.Signer.RevocationHandle
	attrEID := k.attrs[IdxEID]

	comEID, comRH, err := k.eidRHCommitments(auditInfo)
	if err != nil {
		return nil, err
	}
	defer comEID.Clear()
	defer comRH.Clear()
	comSK, err := anoncreds.Commit(k.usk)
	if err != nil {
		return nil, err
	}
	defer comSK.Clear()

	// the wallet user is read-only: presentations run concurrently, bounded by the limiter
	pres, err := k.present(ctx, []*anoncreds.Commitment{comSK, comEID, comRH})
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate the presentation")
	}

	id := comEID.Nym
	eidProof, err := comEID.EqOpeningProve(eidProofContext(k.conf.IPK, id, attrEID), attrEID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to prove the enrollment id of nymEID")
	}
	openingEID, err := comEID.Opening()
	if err != nil {
		return nil, errors.Wrap(err, "failed to export the opening of nymEID")
	}
	defer clear(openingEID)
	openingRH, err := comRH.Opening()
	if err != nil {
		return nil, errors.Wrap(err, "failed to export the opening of nymRH")
	}
	ai := &AuditInfo{EID: eid, RH: rh, NymEID: bytes.Clone(id), NymSK: comSK.Nym, NymRH: comRH.Nym, OpeningRH: openingRH, EIDProof: eidProof, Pres: pres}
	if ai.EncOpeningEID, err = sealOpening(k.aiKey, ai.auditOpeningAD(), openingEID); err != nil {
		return nil, err
	}
	aiRaw, err := ai.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "failed to serialize the audit info")
	}
	openingSK, err := comSK.Opening()
	if err != nil {
		return nil, errors.Wrap(err, "failed to export the opening of nymSK")
	}
	encOpening, err := sealOpening(k.siKey, signerInfoAD(id, comRH.Nym, pres), openingSK)
	if err != nil {
		clear(openingSK)

		return nil, err
	}
	signerInfoRaw, err := json.Marshal(&SignerInfo{Version: SignerInfoVersionV2, EncOpeningSK: encOpening, NymRH: comRH.Nym, Pres: pres})
	if err != nil {
		clear(openingSK)

		return nil, errors.Wrap(err, "failed to serialize the signer info")
	}
	logger.DebugfContext(ctx, "new lazer identity generated with [%s:%s]", eid, utils.Hashable(rh))

	signer := &Signer{usk: k.usk, opening: openingSK, nymSK: comSK.Nym, nymRH: comRH.Nym, pres: pres}

	return &idriver.IdentityDescriptor{
		Identity:   id,
		AuditInfo:  aiRaw,
		Signer:     signer,
		SignerInfo: signerInfoRaw,
		Verifier:   &Verifier{nymEID: bytes.Clone(id), pv: k.pv},
	}, nil
}

// eidRHCommitments returns the commitments to the enrollment id and the
// revocation handle of the wallet: fresh ones, or those of the audit info
// auditInfo of this wallet. The caller clears them.
func (k *KeyManager) eidRHCommitments(auditInfo []byte) (*anoncreds.Commitment, *anoncreds.Commitment, error) {
	attrEID, attrRH := k.attrs[IdxEID], k.attrs[IdxRH]
	if len(auditInfo) == 0 {
		comEID, err := anoncreds.Commit(attrEID)
		if err != nil {
			return nil, nil, err
		}
		comRH, err := anoncreds.Commit(attrRH)
		if err != nil {
			comEID.Clear()

			return nil, nil, err
		}

		return comEID, comRH, nil
	}

	ai, err := DeserializeAuditInfo(auditInfo)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to deserialize the passed audit info")
	}
	if ai.EID != k.conf.Signer.EnrollmentID || ai.RH != k.conf.Signer.RevocationHandle {
		return nil, nil, errors.New("the passed audit info is for another wallet")
	}
	// only this wallet decrypts the opening, and only for the nymEID, EID and RH of the audit info
	openingEID, err := openOpening(k.aiKey, ai.auditOpeningAD(), ai.EncOpeningEID)
	if err != nil {
		return nil, nil, errors.New("the passed audit info is for another wallet, or was altered")
	}
	defer clear(openingEID)
	comEID, err := anoncreds.CommitWithOpening(attrEID, openingEID)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(comEID.Nym, ai.NymEID) {
		comEID.Clear()

		return nil, nil, errors.New("the opening of the passed audit info does not open its nymEID")
	}
	comRH, err := anoncreds.CommitWithOpening(attrRH, ai.OpeningRH)
	if err != nil {
		comEID.Clear()

		return nil, nil, err
	}
	if !bytes.Equal(comRH.Nym, ai.NymRH) {
		comEID.Clear()
		comRH.Clear()

		return nil, nil, errors.New("the opening of the passed audit info does not open its nymRH")
	}

	return comEID, comRH, nil
}

// present generates the presentation of the commitments coms. The limiter
// slot is given back even if PresGen panics: the limiter may be shared with
// other key managers.
func (k *KeyManager) present(ctx context.Context, coms []*anoncreds.Commitment) ([]byte, error) {
	if err := k.limiter.acquire(ctx, k.done); err != nil {
		return nil, err
	}
	defer k.limiter.release()
	pres, err := k.user.PresGen(k.conf.Signer.Cred, nil, coms, idxCom, k.pv.ctx)
	if err != nil && k.isClosed() {
		return nil, errClosed
	}

	return pres, err
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
	if k.isClosed() {
		return nil, errClosed
	}
	if k.identityStore == nil {
		return nil, errors.New("no identity store available")
	}
	if err := checkIdentity(raw); err != nil {
		return nil, err
	}
	infoRaw, err := k.identityStore.GetSignerInfo(ctx, raw)
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve the signer info")
	}
	if len(infoRaw) == 0 {
		return nil, errors.Errorf("no signer info found for identity [%s]", tdriver.Identity(raw).UniqueID())
	}
	info, err := decodeSignerInfo(infoRaw)
	if err != nil {
		return nil, err
	}
	// only the wallet secret decrypts the opening, and only for this identity, nymRH and presentation
	opening, err := openOpening(k.siKey, signerInfoAD(raw, info.NymRH, info.Pres), info.EncOpeningSK)
	if err != nil {
		return nil, err
	}
	// nymSK is recomputed from usk and the opening
	com, err := anoncreds.CommitWithOpening(k.usk, opening)
	if err != nil {
		return nil, errors.Wrap(err, "invalid signer info")
	}
	defer com.Clear()

	return &Signer{usk: k.usk, opening: opening, nymSK: com.Nym, nymRH: info.NymRH, pres: info.Pres}, nil
}

// Info returns a string that includes the given identity. If auditInfo is
// provided, the identity is verified against it and the enrollment id is added.
func (k *KeyManager) Info(ctx context.Context, raw []byte, auditInfo []byte) (string, error) {
	return k.Deserializer.Info(ctx, raw, auditInfo)
}

// Signer signs under a pseudonym. It holds the opening of nymSK in memory
// only, in plaintext, and the public parts of its signatures.
type Signer struct {
	usk     []byte
	opening []byte
	nymSK   []byte
	nymRH   []byte
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

	return asn1.Marshal(Signature{NymSK: s.nymSK, NymRH: s.nymRH, Pres: s.pres, Sok: sok})
}

type signingIdentity struct {
	*Signer
	id []byte
}

// Serialize returns the serialized identity.
func (s *signingIdentity) Serialize() ([]byte, error) { return s.id, nil }
