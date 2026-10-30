/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
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

// maxNymLen is the length of an encoded nym, and of an identity.
var maxNymLen = anoncreds.MaxLenNym()

// An identity (the pseudonym published on the ledger) is the commitment nymEID
// to the enrollment id, nothing else, as in idemixnym. The signatures carry the
// commitments to the user secret (nymSK) and to the revocation handle (nymRH)
// and the presentation that binds the three to a credential.

// checkIdentity checks that raw has the length of a nym. The nym itself is
// decoded, canonically, by every proof verification that uses it.
func checkIdentity(raw []byte) error {
	if len(raw) != maxNymLen {
		return errors.Errorf("invalid identity length [%d]", len(raw))
	}

	return nil
}

// Signature is a signature under a pseudonym nymEID: the commitments nymSK and
// nymRH, a presentation proving that (nymSK, nymEID, nymRH) commit to the
// (usk, eid, rh) attributes of a credential of the issuer, and a signature of
// knowledge of the opening of nymSK bound to the message.
type Signature struct {
	NymSK []byte
	NymRH []byte
	Pres  []byte
	Sok   []byte
}

// maxSignatureLen bounds the size of a signature before parsing it.
var maxSignatureLen = anoncreds.MaxLenPres() + anoncreds.MaxLenSok() + 2*maxNymLen + 64

// unmarshalSignature parses a signature strictly: it must be the DER encoding
// of a Signature (encoding/asn1 alone accepts extra elements in the sequence)
// with nyms of the right length and non-empty bounded proofs.
func unmarshalSignature(raw []byte) (*Signature, error) {
	if len(raw) == 0 || len(raw) > maxSignatureLen {
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
	again, err := asn1.Marshal(*sig)
	if err != nil || !bytes.Equal(again, raw) {
		return nil, errors.New("non-canonical signature encoding")
	}
	if len(sig.NymSK) != maxNymLen || len(sig.NymRH) != maxNymLen {
		return nil, errors.New("invalid nym length in signature")
	}
	if len(sig.Pres) == 0 || len(sig.Pres) > anoncreds.MaxLenPres() || len(sig.Sok) == 0 || len(sig.Sok) > anoncreds.MaxLenSok() {
		return nil, errors.New("invalid proof length in signature")
	}

	return sig, nil
}

// presVerifier verifies presentations and signatures under an issuer public
// key. It is safe for concurrent use: a C verifier state holds the statement
// of the verification in progress, hence a small pool of them is kept (they
// borrow the public matrices shared by the process, so each one is small).
// Nothing is cached: every signature carries a presentation, and every
// verification checks it.
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

// verifyPres checks that pres proves that nymSK, nymEID and nymRH commit to
// the attributes (usk, eid, rh) of a credential issued by the issuer.
func (p *presVerifier) verifyPres(nymSK, nymEID, nymRH, pres []byte) error {
	v, err := p.acquire()
	if err != nil {
		return err
	}
	defer p.release(v)
	nyms := [][]byte{nymSK, nymEID, nymRH}
	if err := v.PresVer(zeroAttributes(), nil, nyms, idxCom, pres, p.ctx); err != nil {
		return errors.Wrap(err, "invalid presentation")
	}

	return nil
}

// verifyEID checks that proof proves that nymEID commits to the enrollment id eid.
func (p *presVerifier) verifyEID(nymEID []byte, eid string, proof []byte) error {
	attr := hashAttr(IdxEID, eid)

	return anoncreds.EqOpeningVerify(nymEID, attr, eidProofContext(p.ipk, nymEID, attr), proof)
}

// verify checks sigma as a signature of msg under the identity nymEID. The
// signature of knowledge, the cheaper check, comes first.
func (p *presVerifier) verify(nymEID, msg, sigma []byte) error {
	sig, err := unmarshalSignature(sigma)
	if err != nil {
		return err
	}
	if err := anoncreds.SokVerify(sig.NymSK, signatureContext(msg), sig.Sok); err != nil {
		return errors.Wrap(err, "invalid signature")
	}

	return p.verifyPres(sig.NymSK, nymEID, sig.NymRH, sig.Pres)
}

// Verifier verifies signatures under a pseudonym.
type Verifier struct {
	nymEID []byte
	pv     *presVerifier
}

var _ driver.Verifier = (*Verifier)(nil)

// Verify checks that sigma is a valid signature of message.
func (v *Verifier) Verify(message, sigma []byte) error {
	return v.pv.verify(v.nymEID, message, sigma)
}
