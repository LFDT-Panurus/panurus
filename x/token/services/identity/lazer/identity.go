/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"encoding/asn1"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// verifierPoolSize is the number of idle verifier states kept around.
	verifierPoolSize = 4
)

var (
	// maxNymLen is the length of an encoded nym.
	maxNymLen = anoncreds.MaxLenNym()
	// maxIdentityLen bounds the size of a serialized identity before parsing it.
	maxIdentityLen = 3*maxNymLen + 64
)

// SerializedIdentity is the pseudonym published on the ledger: commitments to
// the user secret, the enrollment id and the revocation handle.
type SerializedIdentity struct {
	NymSK  []byte
	NymEID []byte
	NymRH  []byte
}

// Signature is a signature under a pseudonym: a presentation proving that the
// nyms of the pseudonym are bound to a valid credential, and a signature of
// knowledge of the opening of nymSK bound to the message.
type Signature struct {
	Pres []byte
	Sok  []byte
}

// Bytes serializes the identity.
func (i *SerializedIdentity) Bytes() ([]byte, error) {
	return asn1.Marshal(*i)
}

// UnmarshalIdentity parses and validates a serialized identity.
func UnmarshalIdentity(raw []byte) (*SerializedIdentity, error) {
	if len(raw) == 0 || len(raw) > maxIdentityLen {
		return nil, errors.Errorf("invalid identity length [%d]", len(raw))
	}
	id := &SerializedIdentity{}
	rest, err := asn1.Unmarshal(raw, id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal identity")
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing data after identity")
	}
	for _, n := range [][]byte{id.NymSK, id.NymEID, id.NymRH} {
		if len(n) != maxNymLen {
			return nil, errors.Errorf("invalid nym length [%d]", len(n))
		}
	}

	return id, nil
}

func unmarshalSignature(raw []byte) (*Signature, error) {
	if len(raw) == 0 || len(raw) > anoncreds.MaxLenPres()+anoncreds.MaxLenSok()+64 {
		return nil, errors.Errorf("invalid signature length [%d]", len(raw))
	}
	sig := &Signature{}
	rest, err := asn1.Unmarshal(raw, sig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal signature")
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing data after signature")
	}

	return sig, nil
}

// presVerifier verifies presentations and signatures under an issuer public
// key. It is safe for concurrent use: the C verifier states are not, hence a
// small pool of them is kept. Nothing is cached: every signature carries a
// presentation, and every verification checks it.
//
// After Close the pool is empty and stays empty: verifications fail, and
// verifier states still in use are released when they are given back.
type presVerifier struct {
	ipk []byte
	ctx []byte

	// poolMu protects idle and closed
	poolMu sync.Mutex
	idle   []*anoncreds.Verifier
	closed bool
}

func newPresVerifier(ipk []byte) (*presVerifier, error) {
	// fail early if the public key is not usable
	v, err := anoncreds.NewVerifier(ipk)
	if err != nil {
		return nil, errors.Wrap(err, "invalid issuer public key")
	}

	return &presVerifier{
		ipk:  append([]byte(nil), ipk...),
		ctx:  identityContext(ipk),
		idle: []*anoncreds.Verifier{v},
	}, nil
}

// Close releases the C state of the pooled verifiers. It is idempotent.
func (p *presVerifier) Close() {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.closed = true
	for _, v := range p.idle {
		v.Clear()
	}
	p.idle = nil
}

func (p *presVerifier) acquire() (*anoncreds.Verifier, error) {
	p.poolMu.Lock()
	if p.closed {
		p.poolMu.Unlock()

		return nil, errors.New("verifier closed")
	}
	if n := len(p.idle); n > 0 {
		v := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.poolMu.Unlock()

		return v, nil
	}
	p.poolMu.Unlock()

	return anoncreds.NewVerifier(p.ipk)
}

func (p *presVerifier) release(v *anoncreds.Verifier) {
	p.poolMu.Lock()
	if !p.closed && len(p.idle) < verifierPoolSize {
		p.idle = append(p.idle, v)
		p.poolMu.Unlock()

		return
	}
	p.poolMu.Unlock()
	v.Clear()
}

// verifyPres checks that pres proves that the nyms of id commit to the
// attributes (usk, eid, rh) of a credential issued by the issuer.
func (p *presVerifier) verifyPres(id *SerializedIdentity, pres []byte) error {
	v, err := p.acquire()
	if err != nil {
		return err
	}
	defer p.release(v)
	nyms := [][]byte{id.NymSK, id.NymEID, id.NymRH}
	if err := v.PresVer(zeroAttributes(), nil, nyms, idxCom, pres, p.ctx); err != nil {
		return errors.Wrap(err, "invalid presentation")
	}

	return nil
}

// verify checks sigma as a signature of msg under the identity id.
func (p *presVerifier) verify(id *SerializedIdentity, msg, sigma []byte) error {
	sig, err := unmarshalSignature(sigma)
	if err != nil {
		return err
	}
	if err := p.verifyPres(id, sig.Pres); err != nil {
		return err
	}
	if err := anoncreds.SokVerify(id.NymSK, signatureContext(msg), sig.Sok); err != nil {
		return errors.Wrap(err, "invalid signature")
	}

	return nil
}

// Verifier verifies signatures under a pseudonym.
type Verifier struct {
	id *SerializedIdentity
	pv *presVerifier
}

var _ driver.Verifier = (*Verifier)(nil)

// Verify checks that sigma is a valid signature of message.
func (v *Verifier) Verify(message, sigma []byte) error {
	return v.pv.verify(v.id, message, sigma)
}
