/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package x509

import (
	"bytes"
	"container/list"
	"crypto/x509"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// OwnerChainChecker checks x509 owner certificates against the x509 owner CAs of the public parameters,
// and binds audit info to them.
type OwnerChainChecker struct {
	cas *crypto.OwnerCAs
	// exempt holds the raw x509 certificates that skip the chain and leaf-CA rules.
	exempt map[string]struct{}
	// trusted caches the certificates that passed check, keyed by their raw bytes.
	trusted *certLRU
}

// trustedCacheSize bounds the number of certificates the checker caches.
const trustedCacheSize = 1024

// NewOwnerChainChecker returns a checker for the PEM owner roots and intermediates.
// The x509 entries of exempt, typed identities such as the issuers and auditors of the public parameters,
// skip the chain and leaf-CA rules; their audit info binding is still checked.
// It returns nil when roots is empty.
func NewOwnerChainChecker(roots, intermediates [][]byte, exempt []driver.Identity) (*OwnerChainChecker, error) {
	cas, err := crypto.NewOwnerCAs(roots, intermediates)
	if err != nil {
		return nil, errors.Wrap(err, "invalid x509 owner certificates")
	}
	if cas == nil {
		return nil, nil
	}
	ex := make(map[string]struct{}, len(exempt))
	for _, id := range exempt {
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil || ti.Type != IdentityType {
			// not a typed x509 identity: there is no x509 certificate to exempt
			continue
		}
		ex[string(ti.Identity)] = struct{}{}
	}

	return &OwnerChainChecker{cas: cas, exempt: ex, trusted: newCertLRU(trustedCacheSize)}, nil
}

// check parses raw strictly and, unless it is exempt, verifies it chains to an owner root.
// Only certificates that pass are cached, so untrusted input cannot fill the cache.
func (c *OwnerChainChecker) check(raw []byte) (*x509.Certificate, error) {
	if cert, ok := c.trusted.get(string(raw)); ok {
		return cert, nil
	}
	cert, err := c.verify(raw)
	if err != nil {
		return nil, err
	}
	c.trusted.add(string(raw), cert)

	return cert, nil
}

// verify parses raw strictly and, unless it is exempt, verifies it chains to an owner root.
func (c *OwnerChainChecker) verify(raw []byte) (*x509.Certificate, error) {
	cert, err := crypto.ParseOwnerCertificate(raw)
	if err != nil {
		return nil, errors.WithMessage(errors.Join(identity.ErrUntrustedIdentity, err), "invalid x509 owner certificate")
	}
	if _, ok := c.exempt[string(raw)]; ok {
		return cert, nil
	}
	if err := c.cas.VerifyOwner(cert); err != nil {
		return nil, errors.Join(identity.ErrUntrustedIdentity, err)
	}

	return cert, nil
}

// bind checks raw and that eid and rh are the enrollment ID and revocation handle of its certificate.
func (c *OwnerChainChecker) bind(raw []byte, eid string, rh []byte) error {
	cert, err := c.check(raw)
	if err != nil {
		return err
	}

	return bindCert(cert, eid, rh)
}

// bindCert checks that eid and rh are the enrollment ID and revocation handle of cert.
func bindCert(cert *x509.Certificate, eid string, rh []byte) error {
	if certEID := crypto.EnrollmentIDFromCert(cert); certEID != eid {
		return errors.Wrapf(identity.ErrUntrustedIdentity, "expected enrollment ID [%s], got [%s]", eid, certEID)
	}
	certRH, err := crypto.RevocationHandleFromCert(cert)
	if err != nil {
		return errors.WithMessage(errors.Join(identity.ErrUntrustedIdentity, err), "failed to get revocation handle")
	}
	if !bytes.Equal(certRH, rh) {
		return errors.Wrap(identity.ErrUntrustedIdentity, "revocation handle does not match the certificate")
	}

	return nil
}

// certLRU is a thread-safe, bounded LRU cache of parsed certificates.
type certLRU struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	items    map[string]*list.Element
}

// certEntry is a certLRU element.
type certEntry struct {
	key  string
	cert *x509.Certificate
}

func newCertLRU(capacity int) *certLRU {
	return &certLRU{capacity: capacity, order: list.New(), items: make(map[string]*list.Element, capacity)}
}

func (l *certLRU) get(key string) (*x509.Certificate, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	elem, ok := l.items[key]
	if !ok {
		return nil, false
	}
	l.order.MoveToFront(elem)

	return elem.Value.(*certEntry).cert, true
}

func (l *certLRU) add(key string, cert *x509.Certificate) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if elem, ok := l.items[key]; ok {
		l.order.MoveToFront(elem)

		return
	}
	l.items[key] = l.order.PushFront(&certEntry{key: key, cert: cert})
	if l.order.Len() > l.capacity {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.items, oldest.Value.(*certEntry).key)
	}
}
